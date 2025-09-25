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
	"github.com/jackc/pgx/v5/pgtype"
	poker "github.com/paulhankin/poker"
)

// heroEquityExact computes exact showdown equity for hero hole cards against a
// random villain range given the current public board. The board must contain
// 3 (flop), 4 (turn), or 5 (river) cards. When the board is not complete, the
// function enumerates every possible runout to the river for both players. It
// returns the probability of hero winning (counting ties as half wins).
func heroEquityExact(heroHole, board []engine.Card) (float64, bool) {
	if len(heroHole) != 2 {
		return 0, false
	}
	if len(board) < 3 || len(board) > 5 {
		return 0, false
	}

	// Build deck and remove the known cards.
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
	for _, c := range heroHole {
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

	avail := make([]engine.Card, 0, len(deck))
	availPH := make([]poker.Card, 0, len(deck))
	for _, c := range deck {
		if used[c] {
			continue
		}
		avail = append(avail, c)
		availPH = append(availPH, toPH(c))
	}

	heroPH := []poker.Card{toPH(heroHole[0]), toPH(heroHole[1])}
	boardPH := make([]poker.Card, 0, len(board))
	for _, c := range board {
		boardPH = append(boardPH, toPH(c))
	}

	needed := 5 - len(board)
	if needed < 0 || needed > 2 {
		return 0, false
	}

	baseHeroLen := len(heroPH) + len(boardPH)
	baseVillainLen := 2 + len(boardPH)
	if baseHeroLen+needed != 7 || baseVillainLen+needed != 7 {
		return 0, false
	}

	var total, win, tie int64
	n := len(avail)
	if n < 2+needed {
		return 0, false
	}

	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			var hero7 [7]poker.Card
			copy(hero7[:len(heroPH)], heroPH)
			copy(hero7[len(heroPH):baseHeroLen], boardPH)

			var vill7 [7]poker.Card
			vill7[0] = availPH[i]
			vill7[1] = availPH[j]
			copy(vill7[2:baseVillainLen], boardPH)

			switch needed {
			case 0:
				heroScore := poker.Eval7(&hero7)
				villScore := poker.Eval7(&vill7)
				total++
				if heroScore > villScore {
					win++
				} else if heroScore == villScore {
					tie++
				}
			case 1:
				for k := 0; k < n; k++ {
					if k == i || k == j {
						continue
					}
					hero7[baseHeroLen] = availPH[k]
					vill7[baseVillainLen] = availPH[k]
					heroScore := poker.Eval7(&hero7)
					villScore := poker.Eval7(&vill7)
					total++
					if heroScore > villScore {
						win++
					} else if heroScore == villScore {
						tie++
					}
				}
			case 2:
				for k := 0; k < n; k++ {
					if k == i || k == j {
						continue
					}
					for l := k + 1; l < n; l++ {
						if l == i || l == j {
							continue
						}
						hero7[baseHeroLen] = availPH[k]
						hero7[baseHeroLen+1] = availPH[l]
						vill7[baseVillainLen] = availPH[k]
						vill7[baseVillainLen+1] = availPH[l]
						heroScore := poker.Eval7(&hero7)
						villScore := poker.Eval7(&vill7)
						total++
						if heroScore > villScore {
							win++
						} else if heroScore == villScore {
							tie++
						}
					}
				}
			}
		}
	}

	if total == 0 {
		return 0, false
	}
	eq := (float64(win) + 0.5*float64(tie)) / float64(total)
	return eq, true
}

// EvaluateMatchMC computes EV comparisons for each flop/turn/river decision
// and writes rows into action_eval with solver='MCJudge'. Facing-bet situations
// (to_call>0) compare call versus fold, while uncontested nodes score check
// versus a nominal 2/3-pot probe bet.
func EvaluateMatchMC(ctx context.Context, db *store.DB, matchID int64) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		active := db
		var (
			cleanup func()
			err     error
		)
		if attempt == 1 || active == nil {
			dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
			if dsn == "" {
				dsn = "postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable"
			}
			active, err = store.Open(dsn)
			if err != nil {
				return err
			}
			cleanup = func() { active.Close(ctx) }
		}

		err = evaluateMatchMCWithDB(ctx, active, matchID)
		if cleanup != nil {
			cleanup()
		}
		if err == nil {
			return nil
		}
		if !isConnBusyError(err) {
			return err
		}
		lastErr = err
		// Retry once with a fresh connection pool if the primary reported a busy connection.
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("conn busy")
}

func evaluateMatchMCWithDB(ctx context.Context, db *store.DB, matchID int64) error {
	if db == nil {
		return errors.New("nil database")
	}

	// Fetch big blind size for epsilon scaling
	var bb int
	if err := db.QueryRow(ctx, `SELECT bb FROM matches WHERE id = $1`, matchID).Scan(&bb); err != nil {
		return err
	}
	if bb <= 0 {
		bb = 100
	}
	eps := 0.15 * float64(bb) // epsilon in chips

	type Row struct {
		ID         int64
		HandID     string
		ActorLabel string
		Street     string
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

	rows, err := db.Query(ctx, `
        SELECT id, hand_id, actor_label, street, pot, to_call, board, sb_hole, bb_hole, LOWER(action), amount
          FROM action_logs
         WHERE match_id = $1 AND street IN ('flop','turn','river')
         ORDER BY id
    `, matchID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var inserts [][]any

	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.HandID, &r.ActorLabel, &r.Street, &r.Pot, &r.ToCall, &r.Board, &r.SBHole, &r.BBHole, &r.Action, &r.Amount); err != nil {
			return err
		}
		if len(r.SBHole) != 2 || len(r.BBHole) != 2 {
			continue
		}

		// Map actor label to seat for this hand id
		aIsSB := strings.HasSuffix(strings.ToUpper(r.HandID), "A")
		heroSeat := engine.SB
		if r.ActorLabel == "A" {
			if !aIsSB {
				heroSeat = engine.BB
			}
		} else { // label B
			if aIsSB {
				heroSeat = engine.BB
			} else {
				heroSeat = engine.SB
			}
		}
		// Hero/villain holes
		var heroHole []string
		if heroSeat == engine.SB {
			heroHole = r.SBHole
		} else {
			heroHole = r.BBHole
		}

		// Parse board + hero hole
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
		board := make([]engine.Card, 0, len(r.Board))
		for _, s := range r.Board {
			if c, ok := parse(strings.TrimSpace(s)); ok {
				board = append(board, c)
			}
		}
		if len(board) < 3 || len(board) > 5 {
			continue
		}
		h1 := make([]engine.Card, 0, 2)
		for _, s := range heroHole {
			if c, ok := parse(s); ok {
				h1 = append(h1, c)
			}
		}
		if len(h1) != 2 {
			continue
		}

		start := time.Now()
		eq, ok := heroEquityExact(h1, board)
		if !ok {
			continue
		}

		P := float64(r.Pot)

		if r.ToCall > 0 {
			// Facing bet: call vs fold
			b := float64(r.ToCall)
			evFold := 0.0
			// Hero invests b to call. Eq captures win probability plus half of ties,
			// so eq*(P+b) is the expected return from the pot while the call always
			// costs the full b chips. Avoid discounting the loss by (1-eq) because eq
			// already prices the tie outcomes.
			evCall := eq*(P+b) - b

			bestAction := "call"
			bestTo := (*int)(nil)
			evBest := evCall
			if evFold > evBest {
				bestAction = "fold"
				evBest = evFold
			}

			// chosen
			// Use the action and amount captured in the main query for comparison.
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
			// Uncontested river: check vs bet (single size ~66% pot)
			b := math.Max(float64(bb), math.Round(0.66*P))
			F := 0.35 // assumed fold equity for 2/3 pot sizing
			evCheck := 0.0
			// When villain folds we earn +P; when called we reach a showdown with pot
			// P+2b and have already invested b ourselves.
			evBet := F*P + (1.0-F)*(eq*(P+2*b)-b)
			bestAction := "raise" // represent bet as raise
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
	if err := rows.Err(); err != nil {
		return err
	}

	if len(inserts) == 0 {
		return nil
	}

	for _, args := range inserts {
		if _, err := db.Exec(ctx, insertActionEvalSQL, args...); err != nil {
			return err
		}
	}
	return nil
}

func isConnBusyError(err error) bool {
	if err == nil {
		return false
	}
	type safeRetry interface {
		SafeToRetry() bool
		Error() string
	}
	var sr safeRetry
	if errors.As(err, &sr) && sr.Error() == "conn busy" {
		return true
	}
	return strings.Contains(err.Error(), "conn busy")
}

// (strptr removed; no longer needed)
