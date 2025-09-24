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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
	poker "github.com/paulhankin/poker"
)

type connSource struct {
	primary  *store.DB
	fallback *store.DB
}

func newConnSource(primary *store.DB) *connSource {
	return &connSource{primary: primary}
}

func (cs *connSource) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	var primaryErr error
	if cs.primary != nil {
		conn, err := cs.primary.Acquire(ctx)
		if err == nil {
			return conn, nil
		}
		primaryErr = err
		if !errors.Is(err, puddle.ErrClosedPool) {
			return nil, err
		}
	}

	if cs.fallback == nil {
		dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dsn == "" {
			dsn = "postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable"
		}
		fresh, err := store.Open(dsn)
		if err != nil {
			if primaryErr != nil {
				return nil, primaryErr
			}
			return nil, err
		}
		cs.fallback = fresh
	}

	conn, err := cs.fallback.Acquire(ctx)
	if err != nil {
		if primaryErr != nil {
			return nil, primaryErr
		}
		return nil, err
	}
	return conn, nil
}

func (cs *connSource) withConn(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	conn, err := cs.acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	return fn(conn)
}

func (cs *connSource) close(ctx context.Context) {
	if cs.fallback != nil {
		cs.fallback.Close(ctx)
	}
}

// EvaluateMatchMC computes river (exact) EV comparisons for each river decision
// and writes rows into action_eval with solver='MCJudge'.
// Minimal scope: only facing-bet decisions (to_call>0) on river; compares Call vs Fold.
func EvaluateMatchMC(ctx context.Context, db *store.DB, matchID int64) error {
	src := newConnSource(db)
	defer src.close(ctx)

	type Row struct {
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

	var (
		bb      int
		rows    []Row
		inserts [][]any
	)

	if err := src.withConn(ctx, func(conn *pgxpool.Conn) error {
		if err := conn.QueryRow(ctx, `SELECT bb FROM matches WHERE id = $1`, matchID).Scan(&bb); err != nil {
			return err
		}
		pgxRows, err := conn.Query(ctx, `
        SELECT id, hand_id, actor_label, pot, to_call, board, sb_hole, bb_hole, LOWER(action), amount
          FROM action_logs
         WHERE match_id = $1 AND street = 'river'
         ORDER BY id
    `, matchID)
		if err != nil {
			return err
		}
		defer pgxRows.Close()
		for pgxRows.Next() {
			var r Row
			if err := pgxRows.Scan(&r.ID, &r.HandID, &r.ActorLabel, &r.Pot, &r.ToCall, &r.Board, &r.SBHole, &r.BBHole, &r.Action, &r.Amount); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return pgxRows.Err()
	}); err != nil {
		return err
	}

	if bb <= 0 {
		bb = 100
	}
	eps := 0.15 * float64(bb)

	for _, r := range rows {
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

		parse := func(s string) (engine.Card, bool) {
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
		board := make([]engine.Card, 0, 5)
		for i := 0; i < 5; i++ {
			if c, ok := parse(r.Board[i]); ok {
				board = append(board, c)
			}
		}
		h1 := make([]engine.Card, 0, 2)
		for _, s := range heroHole {
			if c, ok := parse(s); ok {
				h1 = append(h1, c)
			}
		}
		if len(board) != 5 || len(h1) != 2 {
			continue
		}

		start := time.Now()

		deck := make([]engine.Card, 0, 52)
		suits := []byte{'c', 'd', 'h', 's'}
		for _, su := range suits {
			for rnk := 2; rnk <= 14; rnk++ {
				deck = append(deck, engine.Card{Rank: rnk, Suit: su})
			}
		}
		used := map[engine.Card]bool{}
		for _, c := range board {
			used[c] = true
		}
		for _, c := range h1 {
			used[c] = true
		}

		toPH := func(c engine.Card) poker.Card {
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
			pc, _ := poker.MakeCard(s, rnk)
			return pc
		}
		heroAllPH := make([]poker.Card, 0, 7)
		for _, c := range h1 {
			heroAllPH = append(heroAllPH, toPH(c))
		}
		for _, c := range board {
			heroAllPH = append(heroAllPH, toPH(c))
		}
		var a7 [7]poker.Card
		copy(a7[:], heroAllPH)
		heroScore := poker.Eval7(&a7)

		var total int64
		var win, tie int64
		avail := make([]engine.Card, 0, len(deck))
		for _, c := range deck {
			if !used[c] {
				avail = append(avail, c)
			}
		}
		for i := 0; i < len(avail); i++ {
			for j := i + 1; j < len(avail); j++ {
				total++
				vAllPH := make([]poker.Card, 0, 7)
				vAllPH = append(vAllPH, toPH(avail[i]), toPH(avail[j]))
				for _, c := range board {
					vAllPH = append(vAllPH, toPH(c))
				}
				var b7 [7]poker.Card
				copy(b7[:], vAllPH)
				vScore := poker.Eval7(&b7)
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
			bestTo := (*int)(nil)
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
			bestTo := (*int)(nil)
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

	return src.withConn(ctx, func(conn *pgxpool.Conn) error {
		batch := &pgx.Batch{}
		for _, args := range inserts {
			batch.Queue(insertActionEvalSQL, args...)
		}
		br := conn.SendBatch(ctx, batch)
		var execErr error
		for range inserts {
			if _, err := br.Exec(); err != nil {
				execErr = err
				break
			}
		}
		closeErr := br.Close()
		if execErr != nil {
			return execErr
		}
		return closeErr
	})
}

// (strptr removed; no longer needed)
