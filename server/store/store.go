package store

import (
	"context"
	"embed"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema embed.FS

type DB struct{ *pgxpool.Pool }

func Open(dsn string) (*DB, error) {
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, err
	}
	return &DB{p}, nil
}

func (db *DB) Close(ctx context.Context)      { db.Pool.Close() }
func (db *DB) Ping(ctx context.Context) error { return db.Pool.Ping(ctx) }

func Migrate(ctx context.Context, db *DB) error {
	sqlBytes, err := schema.ReadFile("schema.sql")
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, string(sqlBytes))
	return err
}

/* -----------------------------
   Minimal write helpers
------------------------------*/

// Upsert a bot and return its id.
func (db *DB) UpsertBot(ctx context.Context, name, company string, reasoningEffort *string) (int64, error) {
	var id int64
	var re any
	if reasoningEffort != nil {
		v := strings.TrimSpace(*reasoningEffort)
		if v != "" {
			re = v
		}
	}
	err := db.QueryRow(ctx, `
        INSERT INTO bots(name, company, reasoning_effort)
        VALUES ($1,$2,$3)
        ON CONFLICT (name) DO UPDATE
          SET company = EXCLUDED.company,
              reasoning_effort = EXCLUDED.reasoning_effort
        RETURNING id
    `, name, company, re).Scan(&id)
	return id, err
}

// Ensure a bot_ratings row exists and fetch it.
func (db *DB) GetOrInitRatings(ctx context.Context, botID int64) (elo, gR, gRD, gSigma float64, matches, hands int, err error) {
	// Create if missing
	if _, e := db.Exec(ctx, `INSERT INTO bot_ratings(bot_id) VALUES ($1) ON CONFLICT (bot_id) DO NOTHING`, botID); e != nil {
		return 0, 0, 0, 0, 0, 0, e
	}
	// Read current
	err = db.QueryRow(ctx, `
		SELECT elo, g_rating, g_rd, g_sigma, matches, hands
		  FROM bot_ratings WHERE bot_id = $1
	`, botID).Scan(&elo, &gR, &gRD, &gSigma, &matches, &hands)
	return
}

// Persist final ratings and increment career counters.
func (db *DB) UpdateBotRatings(ctx context.Context, botID int64, elo, gR, gRD, gSigma float64, matchesInc, handsInc, judgeGoodInc, judgeTotalInc int) error {
	_, err := db.Exec(ctx, `
		UPDATE bot_ratings
		   SET elo = $2,
		       g_rating = $3,
		       g_rd = $4,
		       g_sigma = $5,
		       matches = matches + $6,
		       hands = hands + $7,
		       judge_good = judge_good + $8,
		       judge_total = judge_total + $9,
		       updated_at = now()
		 WHERE bot_id = $1
	`, botID, elo, gR, gRD, gSigma, matchesInc, handsInc, judgeGoodInc, judgeTotalInc)
	return err
}

type JudgeAccuracy struct {
	Good  int
	Total int
}

func (ja JudgeAccuracy) Ratio() float64 {
	if ja.Total <= 0 {
		return 0
	}
	return float64(ja.Good) / float64(ja.Total)
}

func (db *DB) GetJudgeAccuracy(ctx context.Context, botID int64) (good, total int, err error) {
	err = db.QueryRow(ctx, `
                SELECT judge_good, judge_total
                  FROM bot_ratings
                 WHERE bot_id = $1
        `, botID).Scan(&good, &total)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return
}

func (db *DB) MatchJudgeAccuracy(ctx context.Context, matchID int64) (map[int64]JudgeAccuracy, error) {
	return db.judgeAccuracy(ctx, " AND a.match_id = $1", matchID)
}

func (db *DB) AllJudgeAccuracy(ctx context.Context) (map[int64]JudgeAccuracy, error) {
	res, err := db.judgeAccuracy(ctx, "")
	if err != nil {
		return nil, err
	}
	if err := db.fillJudgeAccuracyFromRatings(ctx, res, nil); err != nil {
		return nil, err
	}
	return res, nil
}

func (db *DB) SyncJudgeAccuracy(ctx context.Context, botIDs ...int64) error {
	ids := uniquePositiveInt64(botIDs)
	if len(ids) == 0 {
		return nil
	}
	res := make(map[int64]JudgeAccuracy, len(ids))
	for _, id := range ids {
		m, err := db.judgeAccuracy(ctx, " AND p.bot_id = $1", id)
		if err != nil {
			return err
		}
		for k, v := range m {
			res[k] = v
		}
	}
	if err := db.fillJudgeAccuracyFromRatings(ctx, res, ids); err != nil {
		return err
	}
	for _, id := range ids {
		acc, ok := res[id]
		if !ok {
			// Nothing new for this bot — keep existing values.
			continue
		}
		if acc.Total <= 0 {
			if good, total, err := db.GetJudgeAccuracy(ctx, id); err == nil && total > 0 {
				acc = JudgeAccuracy{Good: good, Total: total}
			}
		}
		if _, err := db.Exec(ctx, `
                    UPDATE bot_ratings
                       SET judge_good = $2,
                           judge_total = $3,
                           updated_at = now()
                     WHERE bot_id = $1
            `, id, acc.Good, acc.Total); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) judgeAccuracy(ctx context.Context, where string, args ...any) (map[int64]JudgeAccuracy, error) {
	query := `
                SELECT p.bot_id,
                       SUM(CASE WHEN e.is_top_action THEN 1 ELSE 0 END)::int AS good,
                       COUNT(*)::int AS total
                  FROM action_eval e
                  JOIN action_logs a ON a.id = e.action_log_id
                  JOIN match_participants p ON p.match_id = a.match_id AND p.label = a.actor_label
                 WHERE e.solver = 'MCJudge'` + where + `
                 GROUP BY p.bot_id`

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]JudgeAccuracy)
	for rows.Next() {
		var botID int64
		var good, total int
		if err := rows.Scan(&botID, &good, &total); err != nil {
			return nil, err
		}
		out[botID] = JudgeAccuracy{Good: good, Total: total}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *DB) fillJudgeAccuracyFromRatings(ctx context.Context, dest map[int64]JudgeAccuracy, filter []int64) error {
	var (
		rows pgx.Rows
		err  error
	)
	if len(filter) == 0 {
		rows, err = db.Query(ctx, `SELECT bot_id, judge_good, judge_total FROM bot_ratings`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var good, total int
			if err := rows.Scan(&id, &good, &total); err != nil {
				return err
			}
			if total <= 0 {
				continue
			}
			if existing, ok := dest[id]; !ok || existing.Total <= 0 {
				dest[id] = JudgeAccuracy{Good: good, Total: total}
			}
		}
		return rows.Err()
	}

	for _, id := range filter {
		if id <= 0 {
			continue
		}
		var good, total int
		if err := db.QueryRow(ctx, `SELECT judge_good, judge_total FROM bot_ratings WHERE bot_id = $1`, id).Scan(&good, &total); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return err
		}
		if total <= 0 {
			continue
		}
		if existing, ok := dest[id]; !ok || existing.Total <= 0 {
			dest[id] = JudgeAccuracy{Good: good, Total: total}
		}
	}
	return nil
}

func uniquePositiveInt64(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// Create a match row and return the id.
func (db *DB) CreateMatch(
	ctx context.Context,
	sb, bb, startStack, duelSeeds int,
	deckSeedBase int64,
	eloStart, eloK float64,
	eloPerHand, eloWeightByPot bool,
) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `
		INSERT INTO matches(
			sb, bb, start_stack, duel_seeds, deck_seed_base,
			elo_start, elo_k, elo_per_hand, elo_weight_by_pot
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id
	`, sb, bb, startStack, duelSeeds, deckSeedBase, eloStart, eloK, eloPerHand, eloWeightByPot).Scan(&id)
	return id, err
}

// Add a rating history point (stage=start|after_pair|end).
func (db *DB) InsertRatingPoint(
	ctx context.Context,
	matchID int64,
	stage string,
	pairIndex *int, // nil for start/end
	eloA, eloB float64,
	gAr, gArd, gAsigma float64,
	gBr, gBrd, gBsigma float64,
) error {
	var p any
	if pairIndex != nil {
		p = *pairIndex
	}
	_, err := db.Exec(ctx, `
        INSERT INTO rating_history(
            match_id, stage, pair_index,
            elo_a, elo_b,
            g_a_rating, g_a_rd, g_a_sigma,
            g_b_rating, g_b_rd, g_b_sigma
        )
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
    `, matchID, stage, p,
		eloA, eloB,
		gAr, gArd, gAsigma,
		gBr, gBrd, gBsigma,
	)
	return err
}

// Insert participants (A & B) + tallies + hands atomically.
func (db *DB) InsertParticipantsAndTallies(
	ctx context.Context,
	matchID int64,
	// A
	labelA string, botA int64, nameA, compA string, reA *string,
	startA, endA, winsA int, handsA, handsASB, handsABB, netA int,
	// B
	labelB string, botB int64, nameB, compB string, reB *string,
	startB, endB, winsB int, handsB, handsBSB, handsBBB, netB int,
	// tallies
	checkA, callA, raiseA, foldA int,
	checkB, callB, raiseB, foldB int,
) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // safe if already committed

	// participants
	var reAParam any
	if reA != nil {
		v := strings.TrimSpace(*reA)
		if v != "" {
			reAParam = v
		}
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO match_participants(
            match_id, label, bot_id,
            name_snapshot, company_snapshot, reasoning_effort_snapshot,
            start_bank, end_bank, wins,
            hands_dealt, hands_sb, hands_bb, net_chips
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
    `, matchID, labelA, botA, nameA, compA, reAParam, startA, endA, winsA,
		handsA, handsASB, handsABB, netA); err != nil {
		return err
	}
	var reBParam any
	if reB != nil {
		v := strings.TrimSpace(*reB)
		if v != "" {
			reBParam = v
		}
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO match_participants(
            match_id, label, bot_id,
            name_snapshot, company_snapshot, reasoning_effort_snapshot,
            start_bank, end_bank, wins,
            hands_dealt, hands_sb, hands_bb, net_chips
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
    `, matchID, labelB, botB, nameB, compB, reBParam, startB, endB, winsB,
		handsB, handsBSB, handsBBB, netB); err != nil {
		return err
	}

	// tallies
	if _, err := tx.Exec(ctx, `
		INSERT INTO action_tallies(match_id, label, check_ct, call_ct, raise_ct, fold_ct)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, matchID, labelA, checkA, callA, raiseA, foldA); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO action_tallies(match_id, label, check_ct, call_ct, raise_ct, fold_ct)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, matchID, labelB, checkB, callB, raiseB, foldB); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (db *DB) CompleteMatch(ctx context.Context, matchID int64) error {
	_, err := db.Exec(ctx, `UPDATE matches SET ended_at = now() WHERE id = $1`, matchID)
	return err
}

// InsertActionLog records one action step for live viewers and auditing.
func (db *DB) InsertActionLog(
	ctx context.Context,
	matchID int64,
	pairIndex int,
	handID string,
	street string,
	actorLabel string,
	action string,
	amount *int,
	pot, curBet, toCall, minTo, maxTo int,
	sbStack, bbStack, sbCommitted, bbCommitted int,
	board []string,
	sbHole []string,
	bbHole []string,
) error {
	var amt any
	if amount != nil {
		amt = *amount
	}
	_, err := db.Exec(ctx, `
        INSERT INTO action_logs(
            match_id, pair_index, hand_id, street,
            actor_label, action, amount,
            pot, cur_bet, to_call, min_raise_to, max_raise_to,
            sb_stack, bb_stack, sb_committed, bb_committed,
            board, sb_hole, bb_hole
        ) VALUES (
            $1,$2,$3,$4,
            $5,$6,$7,
            $8,$9,$10,$11,$12,
            $13,$14,$15,$16,
            $17,$18,$19
        )
    `,
		matchID, pairIndex, handID, street,
		actorLabel, action, amt,
		pot, curBet, toCall, minTo, maxTo,
		sbStack, bbStack, sbCommitted, bbCommitted,
		board, sbHole, bbHole,
	)
	return err
}

// InsertActionEval records a solver evaluation for a specific action log id.
func (db *DB) InsertActionEval(
	ctx context.Context,
	actionLogID int64,
	solver string,
	solverVersion *string,
	abstraction *string,
	policyJSON any,
	evsJSON any,
	bestAction *string,
	bestAmountTo *int,
	chosenAction *string,
	chosenAmountTo *int,
	evChosen *float64,
	evBest *float64,
	evGapBB *float64,
	correctnessProb *float64,
	isTopAction *bool,
	computeMS *int,
) error {
	var sv, abs, ba, ca any
	if solverVersion != nil {
		sv = *solverVersion
	}
	if abstraction != nil {
		abs = *abstraction
	}
	if bestAction != nil {
		ba = *bestAction
	}
	if chosenAction != nil {
		ca = *chosenAction
	}
	var bat, cat, evc, evb, gap, prob, top, ms any
	if bestAmountTo != nil {
		bat = *bestAmountTo
	}
	if chosenAmountTo != nil {
		cat = *chosenAmountTo
	}
	if evChosen != nil {
		evc = *evChosen
	}
	if evBest != nil {
		evb = *evBest
	}
	if evGapBB != nil {
		gap = *evGapBB
	}
	if correctnessProb != nil {
		prob = *correctnessProb
	}
	if isTopAction != nil {
		top = *isTopAction
	}
	if computeMS != nil {
		ms = *computeMS
	}

	_, err := db.Exec(ctx, `
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
		actionLogID, solver, sv, abs,
		policyJSON, evsJSON,
		ba, bat,
		ca, cat,
		evc, evb, gap, prob,
		top, ms,
	)
	return err
}
