package judge

import (
    "context"
    "os"
    "strings"
    "time"

    "ai-thunderdome/server/engine"
    "ai-thunderdome/server/store"
    "math"
    poker "github.com/paulhankin/poker"
)

// EvaluateMatchMC computes river (exact) EV comparisons for each river decision
// and writes rows into action_eval with solver='MCJudge'.
// Minimal scope: only facing-bet decisions (to_call>0) on river; compares Call vs Fold.
func EvaluateMatchMC(ctx context.Context, db *store.DB, matchID int64) error {
    // Acquire a dedicated connection so work continues even if the pool closes soon after.
    conn, err := db.Acquire(ctx)
    if err != nil {
        // Fallback: if pool is closed, open a fresh one just for judging.
        dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
        if dsn == "" {
            dsn = "postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable"
        }
        fresh, e2 := store.Open(dsn)
        if e2 != nil { return err }
        defer fresh.Close(ctx)
        conn2, e3 := fresh.Acquire(ctx)
        if e3 != nil { return err }
        defer conn2.Release()
        conn = conn2
    } else {
        defer conn.Release()
    }

    // Fetch big blind size for epsilon scaling
    var bb int
    if err := conn.QueryRow(ctx, `SELECT bb FROM matches WHERE id = $1`, matchID).Scan(&bb); err != nil { return err }
    if bb <= 0 { bb = 100 }
    eps := 0.15 * float64(bb) // epsilon in chips

    type Row struct {
        ID int64
        HandID string
        ActorLabel string
        Pot int
        ToCall int
        Board []string
        SBHole []string
        BBHole []string
    }
    rows, err := conn.Query(ctx, `
        SELECT id, hand_id, actor_label, pot, to_call, board, sb_hole, bb_hole
          FROM action_logs
         WHERE match_id = $1 AND street = 'river'
         ORDER BY id
    `, matchID)
    if err != nil { return err }
    defer rows.Close()

    for rows.Next(){
        var r Row
        if err := rows.Scan(&r.ID, &r.HandID, &r.ActorLabel, &r.Pot, &r.ToCall, &r.Board, &r.SBHole, &r.BBHole); err != nil { return err }
        if len(r.Board) < 5 || len(r.SBHole) != 2 || len(r.BBHole) != 2 { continue }

        // Map actor label to seat for this hand id
        aIsSB := strings.HasSuffix(strings.ToUpper(r.HandID), "A")
        heroSeat := engine.SB
        if r.ActorLabel == "A" {
            if !aIsSB { heroSeat = engine.BB }
        } else { // label B
            if aIsSB { heroSeat = engine.BB } else { heroSeat = engine.SB }
        }
        // Hero/villain holes
        var heroHole []string
        if heroSeat == engine.SB { heroHole = r.SBHole } else { heroHole = r.BBHole }

        // Parse board + hero hole
        parse := func(s string) (engine.Card, bool) {
            if len(s) < 2 { return engine.Card{}, false }
            rnk := s[0]; suit := s[1]
            var rank int
            switch rnk { case 'A': rank=14; case 'K': rank=13; case 'Q': rank=12; case 'J': rank=11; case 'T': rank=10; default: if rnk>='2'&&rnk<='9' { rank=int(rnk-'0') } }
            if rank==0 { return engine.Card{}, false }
            if suit!='c'&&suit!='d'&&suit!='h'&&suit!='s' { return engine.Card{}, false }
            return engine.Card{Rank: rank, Suit: suit}, true
        }
        board := make([]engine.Card, 0, 5)
        for i:=0;i<5;i++{ if c,ok:=parse(r.Board[i]); ok { board = append(board,c) } }
        h1 := make([]engine.Card, 0, 2)
        for _, s := range heroHole { if c,ok:=parse(s); ok { h1 = append(h1,c) } }
        if len(board)!=5 || len(h1)!=2 { continue }

        // Build deck and enumerate villain combos (exact equity)
        deck := make([]engine.Card, 0, 52)
        suits := []byte{'c','d','h','s'}
        for _, su := range suits {
            for rnk:=2; rnk<=14; rnk++ { deck = append(deck, engine.Card{Rank:rnk, Suit:su}) }
        }
        used := map[engine.Card]bool{}
        for _, c := range board { used[c]=true }
        for _, c := range h1 { used[c]=true }

        // Build poker lib cards
        toPH := func(c engine.Card) poker.Card {
            var s poker.Suit
            switch c.Suit { case 'c': s=poker.Club; case 'd': s=poker.Diamond; case 'h': s=poker.Heart; default: s=poker.Spade }
            var rnk poker.Rank
            if c.Rank == 14 { rnk = poker.Rank(1) } else { rnk = poker.Rank(c.Rank) }
            pc, _ := poker.MakeCard(s, rnk); return pc
        }
        heroAllPH := make([]poker.Card, 0, 7)
        for _, c := range h1 { heroAllPH = append(heroAllPH, toPH(c)) }
        for _, c := range board { heroAllPH = append(heroAllPH, toPH(c)) }
        var a7 [7]poker.Card; copy(a7[:], heroAllPH)
        heroScore := poker.Eval7(&a7)

        var total int64
        var win, tie int64
        // enumerate pairs
        avail := make([]engine.Card, 0, len(deck))
        for _, c := range deck { if !used[c] { avail = append(avail, c) } }
        for i:=0;i<len(avail);i++{
            for j:=i+1;j<len(avail);j++{
                total++
                vAllPH := make([]poker.Card, 0, 7)
                vAllPH = append(vAllPH, toPH(avail[i]), toPH(avail[j]))
                for _, c := range board { vAllPH = append(vAllPH, toPH(c)) }
                var b7 [7]poker.Card; copy(b7[:], vAllPH)
                vScore := poker.Eval7(&b7)
                if heroScore < vScore { // lower is better
                    win++
                } else if heroScore == vScore {
                    tie++
                }
            }
        }
        if total == 0 { continue }
        eq := (float64(win) + 0.5*float64(tie)) / float64(total)

        P := float64(r.Pot)

        if r.ToCall > 0 {
            // Facing bet: call vs fold
            b := float64(r.ToCall)
            evFold := 0.0
            evCall := eq*(P+b) - (1.0-eq)*b

            bestAction := "call"
            bestTo := (*int)(nil)
            evBest := evCall
            if evFold > evBest { bestAction = "fold"; evBest = evFold }

            // chosen
            // Fetch chosen action and amount from the same row using the same connection
            var chosenAction string
            var chosenTo *int
            _ = conn.QueryRow(ctx, `SELECT LOWER(action), amount FROM action_logs WHERE id=$1`, r.ID).Scan(&chosenAction, &chosenTo)

            if chosenAction != "call" && chosenAction != "fold" { continue }
            evChosen := evFold
            if chosenAction == "call" { evChosen = evCall }

            gap := (evBest - evChosen) / float64(bb)
            isTop := (evBest-evChosen) <= eps
            t0 := time.Now()
            // Insert using the same connection to avoid pool-close races.
            var sv, abs, pol, evs any
            var bat, cat, evc, evb, gapv, prob, top, ms any
            if bestTo != nil { bat = *bestTo }
            if chosenTo != nil { cat = *chosenTo }
            evc = evChosen
            evb = evBest
            gapv = gap
            top = isTop
            // compute duration at end
            ms = int(time.Since(t0) / time.Millisecond)
            _, _ = conn.Exec(ctx, `
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
            `,
                r.ID, "MCJudge", sv, abs,
                pol, evs,
                bestAction, bat,
                chosenAction, cat,
                evc, evb, gapv, prob,
                top, ms,
            )
        } else {
            // Uncontested river: check vs bet (single size ~66% pot)
            b := math.Max(float64(bb), math.Round(0.66*P))
            F := 0.35 // assumed fold equity for 2/3 pot sizing
            evCheck := 0.0
            evBet := F*P + (1.0-F)*(eq*(P+2*b) - (1.0-eq)*b)
            bestAction := "raise" // represent bet as raise
            bestTo := (*int)(nil)
            evBest := evBet
            if evCheck > evBest { bestAction = "check"; evBest = evCheck }

            var chosenAction string
            var chosenTo *int
            _ = conn.QueryRow(ctx, `SELECT LOWER(action), amount FROM action_logs WHERE id=$1`, r.ID).Scan(&chosenAction, &chosenTo)

            if chosenAction != "check" && chosenAction != "raise" { continue }
            evChosen := evCheck
            if chosenAction == "raise" { evChosen = evBet }

            gap := (evBest - evChosen) / float64(bb)
            isTop := (evBest-evChosen) <= eps
            t0 := time.Now()
            // Insert using the same connection to avoid pool-close races.
            var sv, abs, pol, evs any
            var bat, cat, evc, evb, gapv, prob, top, ms any
            if bestTo != nil { bat = *bestTo }
            if chosenTo != nil { cat = *chosenTo }
            evc = evChosen
            evb = evBest
            gapv = gap
            top = isTop
            ms = int(time.Since(t0) / time.Millisecond)
            _, _ = conn.Exec(ctx, `
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
            `,
                r.ID, "MCJudge", sv, abs,
                pol, evs,
                bestAction, bat,
                chosenAction, cat,
                evc, evb, gapv, prob,
                top, ms,
            )
        }
    }
    return nil
}

// (strptr removed; no longer needed)
