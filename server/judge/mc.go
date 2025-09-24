package judge

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"time"

	"ai-thunderdome/server/engine"
	"ai-thunderdome/server/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	poker "github.com/paulhankin/poker"
)

type mcDBRunner struct {
	primary  *store.DB
	fallback *store.DB
}

func newMCDBRunner(primary *store.DB) *mcDBRunner {
	return &mcDBRunner{primary: primary}
}

func (r *mcDBRunner) Close(ctx context.Context) {
	if r.fallback != nil {
		r.fallback.Close(ctx)
		r.fallback = nil
	}
}

func (r *mcDBRunner) ensureFallback(ctx context.Context) error {
	if r.fallback != nil {
		return nil
	}
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		dsn = "postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable"
	}
	fresh, err := store.Open(dsn)
	if err != nil {
		return err
	}
	r.fallback = fresh
	return nil
}

func (r *mcDBRunner) use(ctx context.Context, fn func(*store.DB) error) error {
	if r.primary != nil {
		if err := fn(r.primary); err != nil {
			if isConnBusy(err) {
				r.primary = nil
			} else {
				return err
			}
		} else {
			return nil
		}
	}
	if err := r.ensureFallback(ctx); err != nil {
		return err
	}
	return fn(r.fallback)
}

func (r *mcDBRunner) QueryRow(ctx context.Context, query string, args []any, dest ...any) error {
	return r.use(ctx, func(db *store.DB) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
}

func (r *mcDBRunner) QueryRows(ctx context.Context, query string, args []any, handler func(pgx.Rows) error) error {
	return r.use(ctx, func(db *store.DB) error {
		rows, err := db.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if err := handler(rows); err != nil {
			return err
		}
		return rows.Err()
	})
}

func (r *mcDBRunner) ExecBatch(ctx context.Context, query string, args [][]any) error {
	if len(args) == 0 {
		return nil
	}
	return r.use(ctx, func(db *store.DB) error {
		batch := &pgx.Batch{}
		for _, a := range args {
			batch.Queue(query, a...)
		}
		br := db.SendBatch(ctx, batch)
		for range args {
			if _, err := br.Exec(); err != nil {
				br.Close()
				return err
			}
		}
		return br.Close()
	})
}

func isConnBusy(err error) bool {
	if err == nil {
		return false
	}
	var lock interface {
		error
		SafeToRetry() bool
	}
	if errors.As(err, &lock) {
		if strings.Contains(strings.ToLower(lock.Error()), "conn busy") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "conn busy")
}

// EvaluateMatchMC computes river (exact) EV comparisons for each river decision
// and writes rows into action_eval with solver='MCJudge'.
// Minimal scope: only facing-bet decisions (to_call>0) on river; compares Call vs Fold.
func EvaluateMatchMC(ctx context.Context, db *store.DB, matchID int64) error {
	runner := newMCDBRunner(db)
	defer runner.Close(ctx)

	var bb int
	if err := runner.QueryRow(ctx, `SELECT bb FROM matches WHERE id = $1`, []any{matchID}, &bb); err != nil {
		return err
	}
	if bb <= 0 {
		bb = 100
	}
	eps := 0.15 * float64(bb)

	type actionRow struct {
		ID         int64
		HandID     string
		ActorLabel string
		Pot        int
		ToCall     int
		Board      []string
		SBHole     []string
		BBHole     []string
		Action     string
		Amount     pgtype.Int4
	}

	rawRows := make([]actionRow, 0, 32)
	if err := runner.QueryRows(ctx, `
        SELECT id, hand_id, actor_label, pot, to_call, board, sb_hole, bb_hole, LOWER(action), amount
          FROM action_logs
         WHERE match_id = $1 AND street = 'river'
         ORDER BY id
    `, []any{matchID}, func(rows pgx.Rows) error {
		temp := make([]actionRow, 0, 32)
		for rows.Next() {
			var r actionRow
			if err := rows.Scan(&r.ID, &r.HandID, &r.ActorLabel, &r.Pot, &r.ToCall, &r.Board, &r.SBHole, &r.BBHole, &r.Action, &r.Amount); err != nil {
				return err
			}
			temp = append(temp, r)
		}
		rawRows = temp
		return nil
	}); err != nil {
		return err
	}

	if len(rawRows) == 0 {
		return nil
	}

	suits := []byte{'c', 'd', 'h', 's'}
	fullDeck := make([]engine.Card, 0, 52)
	for _, su := range suits {
		for rnk := 2; rnk <= 14; rnk++ {
			fullDeck = append(fullDeck, engine.Card{Rank: rnk, Suit: su})
		}
	}

	toPoker := func(c engine.Card) poker.Card {
		var s poker.Suit
		switch c.Suit {
		case 'c':
			s = poker.Club
		case 'd':
			s = poker.Diamond
		case 'h':
			s = poker.Heart
		default:
			s = poker.Spade
		}
		var rnk poker.Rank
		if c.Rank == 14 {
			rnk = poker.Rank(1)
		} else {
			rnk = poker.Rank(c.Rank)
		}
		card, _ := poker.MakeCard(s, rnk)
		return card
	}

	parseCard := func(s string) (engine.Card, bool) {
		if len(s) < 2 {
			return engine.Card{}, false
		}
		rnk := s[0]
		suit := s[1]
		var rank int
		switch rnk {
		case 'A':
			rank = 14
		case 'K':
			rank = 13
		case 'Q':
			rank = 12
		case 'J':
			rank = 11
		case 'T':
			rank = 10
		default:
			if rnk >= '2' && rnk <= '9' {
				rank = int(rnk - '0')
			}
		}
		if rank == 0 {
			return engine.Card{}, false
		}
		if suit != 'c' && suit != 'd' && suit != 'h' && suit != 's' {
			return engine.Card{}, false
		}
		return engine.Card{Rank: rank, Suit: suit}, true
	}

	const insertActionEvalSQL = `
               INSERT INTO action_eval(
                   action_log_id, solver, solver_version, abstraction,
                   policy_json, evs_json,
                   best_action, best_amount_to,
                   chosen_action, chosen_amount_to,
                   ev_chosen, ev_best, ev_gap_bb, correctness_prob,
                   is_top_action, compute_ms
               ) VALUES (
                   $1,$2,$3,$4,
                   $5,$6,
                   $7,$8,
                   $9,$10,
                   $11,$12,$13,$14,
                   $15,$16
               )
               ON CONFLICT (action_log_id) DO UPDATE SET
                   solver = EXCLUDED.solver,
                   solver_version = EXCLUDED.solver_version,
                   abstraction = EXCLUDED.abstraction,
                   policy_json = EXCLUDED.policy_json,
                   evs_json = EXCLUDED.evs_json,
                   best_action = EXCLUDED.best_action,
                   best_amount_to = EXCLUDED.best_amount_to,
                   chosen_action = EXCLUDED.chosen_action,
                   chosen_amount_to = EXCLUDED.chosen_amount_to,
                   ev_chosen = EXCLUDED.ev_chosen,
                   ev_best = EXCLUDED.ev_best,
                   ev_gap_bb = EXCLUDED.ev_gap_bb,
                   correctness_prob = EXCLUDED.correctness_prob,
                   is_top_action = EXCLUDED.is_top_action,
                   compute_ms = EXCLUDED.compute_ms
       `

	inserts := make([][]any, 0, len(rawRows))

	for _, r := range rawRows {
		if len(r.Board) < 5 || len(r.SBHole) != 2 || len(r.BBHole) != 2 {
			continue
		}

		aIsSB := strings.HasSuffix(strings.ToUpper(r.HandID), "A")
		heroSeat := engine.SB
		if r.ActorLabel == "A" {
			if !aIsSB {
				heroSeat = engine.BB
			}
		} else {
			if aIsSB {
				heroSeat = engine.BB
			} else {
				heroSeat = engine.SB
			}
		}

		var heroHole []string
		if heroSeat == engine.SB {
			heroHole = r.SBHole
		} else {
			heroHole = r.BBHole
		}

		board := make([]engine.Card, 0, 5)
		for i := 0; i < 5 && i < len(r.Board); i++ {
			if c, ok := parseCard(r.Board[i]); ok {
				board = append(board, c)
			}
		}
		if len(board) != 5 {
			continue
		}

		hole := make([]engine.Card, 0, 2)
		for _, s := range heroHole {
			if c, ok := parseCard(s); ok {
				hole = append(hole, c)
			}
		}
		if len(hole) != 2 {
			continue
		}

		start := time.Now()

		used := make(map[engine.Card]bool, 7)
		for _, c := range board {
			used[c] = true
		}
		for _, c := range hole {
			used[c] = true
		}

		boardPH := make([]poker.Card, 0, 5)
		for _, c := range board {
			boardPH = append(boardPH, toPoker(c))
		}

		var heroAll [7]poker.Card
		heroAll[0] = toPoker(hole[0])
		heroAll[1] = toPoker(hole[1])
		copy(heroAll[2:], boardPH)
		heroScore := poker.Eval7(&heroAll)

		avail := make([]engine.Card, 0, len(fullDeck))
		for _, c := range fullDeck {
			if !used[c] {
				avail = append(avail, c)
			}
		}

		var total int64
		var win, tie int64
		var combo [7]poker.Card
		copy(combo[2:], boardPH)
		for i := 0; i < len(avail); i++ {
			for j := i + 1; j < len(avail); j++ {
				total++
				combo[0] = toPoker(avail[i])
				combo[1] = toPoker(avail[j])
				vScore := poker.Eval7(&combo)
				if heroScore > vScore {
					win++
				} else if heroScore == vScore {
					tie++
				}
			}
		}
		if total == 0 {
			continue
		}

		eq := (float64(win) + 0.5*float64(tie)) / float64(total)
		P := float64(r.Pot)

		if r.ToCall > 0 {
			b := float64(r.ToCall)
			evFold := 0.0
			evCall := eq*(P+b) - b

			bestAction := "call"
			var bestTo *int
			evBest := evCall
			if evFold > evBest {
				bestAction = "fold"
				evBest = evFold
			}

			chosenAction := r.Action
			var chosenTo *int
			if r.Amount.Valid {
				v := int(r.Amount.Int32)
				chosenTo = &v
			}

			if chosenAction != "call" && chosenAction != "fold" {
				continue
			}

			evChosen := evFold
			if chosenAction == "call" {
				evChosen = evCall
			}

			gapChips := evBest - evChosen
			gap := gapChips / float64(bb)
			isTop := gapChips <= eps

			var bestToVal any
			if bestTo != nil {
				bestToVal = *bestTo
			}
			var chosenToVal any
			if chosenTo != nil {
				chosenToVal = *chosenTo
			}

			computeMS := int(time.Since(start) / time.Millisecond)
			inserts = append(inserts, []any{
				r.ID, "MCJudge", nil, nil,
				nil, nil,
				bestAction, bestToVal,
				chosenAction, chosenToVal,
				evChosen, evBest, gap, nil,
				isTop, computeMS,
			})
		} else {
			b := math.Max(float64(bb), math.Round(0.66*P))
			F := 0.35
			evCheck := 0.0
			evBet := F*P + (1.0-F)*(eq*(P+2*b)-b)

			bestAction := "raise"
			var bestTo *int
			evBest := evBet
			if evCheck > evBest {
				bestAction = "check"
				evBest = evCheck
			}

			chosenAction := r.Action
			var chosenTo *int
			if r.Amount.Valid {
				v := int(r.Amount.Int32)
				chosenTo = &v
			}

			if chosenAction != "check" && chosenAction != "raise" {
				continue
			}

			evChosen := evCheck
			if chosenAction == "raise" {
				evChosen = evBet
			}

			gapChips := evBest - evChosen
			gap := gapChips / float64(bb)
			isTop := gapChips <= eps

			var bestToVal any
			if bestTo != nil {
				bestToVal = *bestTo
			}
			var chosenToVal any
			if chosenTo != nil {
				chosenToVal = *chosenTo
			}

			computeMS := int(time.Since(start) / time.Millisecond)
			inserts = append(inserts, []any{
				r.ID, "MCJudge", nil, nil,
				nil, nil,
				bestAction, bestToVal,
				chosenAction, chosenToVal,
				evChosen, evBest, gap, nil,
				isTop, computeMS,
			})
		}
	}

	if len(inserts) == 0 {
		return nil
	}

	return runner.ExecBatch(ctx, insertActionEvalSQL, inserts)
}

// (strptr removed; no longer needed)
