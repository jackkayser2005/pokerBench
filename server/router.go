// server/http.go
package main

import (
    "context"
    "embed"
    "encoding/json"
    "fmt"
    "io/fs"
    "net/http"
    "strings"
    "time"

    "ai-thunderdome/server/engine"
    "ai-thunderdome/server/store"
)

// embed the /web directory so index.html and assets ship in the binary
//
//go:embed web/*
var webFS embed.FS

func Router(db *store.DB) http.Handler {
	mux := http.NewServeMux()

	// Static files under /web/ and root redirect to leaderboard
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/web/", http.StripPrefix("/web/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/web/leaderboard.html", http.StatusFound)
	})

	// Health
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true})
	})

	// Latest match bundle
	mux.HandleFunc("/api/last-match", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		type Match struct {
			ID             int64      `json:"id"`
			CreatedAt      time.Time  `json:"created_at"`
			EndedAt        *time.Time `json:"ended_at"`
			SB             int        `json:"sb"`
			BB             int        `json:"bb"`
			StartStack     int        `json:"start_stack"`
			DuelSeeds      int        `json:"duel_seeds"`
			EloStart       float64    `json:"elo_start"`
			EloK           float64    `json:"elo_k"`
			EloPerHand     bool       `json:"elo_per_hand"`
			EloWeightByPot bool       `json:"elo_weight_by_pot"`
		}
		type Participant struct {
			Label   string  `json:"label"`
			Model   string  `json:"model"`
			Start   int     `json:"start_bank"`
			End     int     `json:"end_bank"`
			Wins    int     `json:"wins"`
			Company string  `json:"company"`
			REffort *string `json:"reasoning_effort"`
		}
		type Mix struct {
			Label    string `json:"label"`
			Model    string `json:"model"`
			Check    int    `json:"check_ct"`
			Call     int    `json:"call_ct"`
			Raise    int    `json:"raise_ct"`
			Fold     int    `json:"fold_ct"`
			Total    int    `json:"total_actions"`
			FoldPct  int    `json:"fold_pct"`
			CallPct  int    `json:"call_pct"`
			RaisePct int    `json:"raise_pct"`
			CheckPct int    `json:"check_pct"`
		}
		type Rating struct {
			Stage     string    `json:"stage"`      // start | after_pair | end
			PairIndex *int      `json:"pair_index"` // null for start/end
			EloA      float64   `json:"elo_a"`
			EloB      float64   `json:"elo_b"`
			GA        float64   `json:"g_a"`
			GB        float64   `json:"g_b"`
			CreatedAt time.Time `json:"created_at"`
		}

		type Payload struct {
			Match        Match         `json:"match"`
			Participants []Participant `json:"participants"`
			ActionMix    []Mix         `json:"action_mix"`
			Rating       []Rating      `json:"rating"`
		}

		// latest match
		var m Match
		err := db.QueryRow(ctx, `
            SELECT id, created_at, ended_at, sb, bb, start_stack, duel_seeds,
                   elo_start, elo_k, elo_per_hand, elo_weight_by_pot
              FROM matches
             ORDER BY id DESC
             LIMIT 1
        `).Scan(&m.ID, &m.CreatedAt, &m.EndedAt, &m.SB, &m.BB, &m.StartStack, &m.DuelSeeds,
			&m.EloStart, &m.EloK, &m.EloPerHand, &m.EloWeightByPot)
		if err != nil {
			http.Error(w, "no matches yet", http.StatusNotFound)
			return
		}

		// participants
		rows, err := db.Query(ctx, `
			SELECT label, name_snapshot, start_bank, end_bank, wins,
			       company_snapshot, reasoning_effort_snapshot
			FROM match_participants
			WHERE match_id = $1
			ORDER BY label
		`, m.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		parts := []Participant{}
		for rows.Next() {
			var p Participant
			if err := rows.Scan(&p.Label, &p.Model, &p.Start, &p.End, &p.Wins, &p.Company, &p.REffort); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			parts = append(parts, p)
		}

		// action mix via the view (has totals and percentages)
		rows2, err := db.Query(ctx, `
			SELECT label, model, check_ct, call_ct, raise_ct, fold_ct,
			       total_actions, fold_pct, call_pct, raise_pct, check_pct
			FROM v_match_action_mix
			WHERE match_id = $1
			ORDER BY label
		`, m.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows2.Close()
		mix := []Mix{}
		for rows2.Next() {
			var x Mix
			if err := rows2.Scan(&x.Label, &x.Model, &x.Check, &x.Call, &x.Raise, &x.Fold,
				&x.Total, &x.FoldPct, &x.CallPct, &x.RaisePct, &x.CheckPct); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			mix = append(mix, x)
		}

		// rating timeline
		rows3, err := db.Query(ctx, `
			SELECT stage, pair_index,
			       elo_a, elo_b,
			       g_a_rating, g_b_rating,
			       created_at
			FROM rating_history
			WHERE match_id = $1
			ORDER BY COALESCE(pair_index, 0), created_at
		`, m.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows3.Close()
		rating := []Rating{}
		for rows3.Next() {
			var r Rating
			if err := rows3.Scan(&r.Stage, &r.PairIndex, &r.EloA, &r.EloB, &r.GA, &r.GB, &r.CreatedAt); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			rating = append(rating, r)
		}

		writeJSON(w, Payload{
			Match:        m,
			Participants: parts,
			ActionMix:    mix,
			Rating:       rating,
		})
	})

	// Recent matches for history page
	mux.HandleFunc("/api/matches", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		type Row struct {
			ID        int64      `json:"id"`
			CreatedAt time.Time  `json:"created_at"`
			EndedAt   *time.Time `json:"ended_at"`
			SBA       int        `json:"sb"`
			BBA       int        `json:"bb"`
			Start     int        `json:"start_stack"`
			Seeds     int        `json:"duel_seeds"`
			ModelA    string     `json:"model_a"`
			ModelB    string     `json:"model_b"`
		}
		rows, err := db.Query(ctx, `
            SELECT m.id, m.created_at, m.ended_at, m.sb, m.bb, m.start_stack, m.duel_seeds,
                   MAX(CASE WHEN p.label='A' THEN p.name_snapshot END) AS model_a,
                   MAX(CASE WHEN p.label='B' THEN p.name_snapshot END) AS model_b
              FROM matches m
              LEFT JOIN match_participants p ON p.match_id = m.id
             GROUP BY m.id
             ORDER BY m.id DESC
             LIMIT 200
        `)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []Row{}
		for rows.Next() {
			var x Row
			if err := rows.Scan(&x.ID, &x.CreatedAt, &x.EndedAt, &x.SBA, &x.BBA, &x.Start, &x.Seeds, &x.ModelA, &x.ModelB); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			out = append(out, x)
		}
		writeJSON(w, map[string]any{"rows": out})
	})

	// Leaderboard: top bots by Elo (career stats, org)
	mux.HandleFunc("/api/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		type Row struct {
			BotID       int64     `json:"bot_id"`
			Model       string    `json:"model"`
			Company     string    `json:"company"`
			Elo         float64   `json:"elo"`
			Matches     int       `json:"matches"`
			Hands       int       `json:"hands"`
			Updated     time.Time `json:"updated_at"`
			CareerWins  int       `json:"career_wins"`
			CareerHands int       `json:"career_hands"`
			WinRatePct  int       `json:"win_rate_pct"`
			NetChips    int       `json:"net_chips"`
			Good        int       `json:"good"`
			Total       int       `json:"total"`
			Acc         float64   `json:"acc"`
		}
        rows, err := db.Query(ctx, `
            WITH summary AS (
                SELECT bot_id,
                       COALESCE(SUM(total_hand_wins),0) AS total_hand_wins,
                       COALESCE(SUM(total_hands),0)      AS total_hands,
                       COALESCE(SUM(total_net_chips),0)  AS total_net_chips,
                       ROUND(100.0 * COALESCE(SUM(total_hand_wins)::float / NULLIF(SUM(total_hands),0), 0)) AS win_rate_pct
                  FROM v_bot_summary
                 GROUP BY bot_id
            )
            SELECT c.id AS bot_id,
                   c.name AS model,
                   c.company AS company,
                   COALESCE(c.elo, 1500)         AS elo,
                   COALESCE(c.matches, 0)        AS matches,
                   COALESCE(c.hands, 0)          AS hands,
                   COALESCE(c.updated_at, now()) AS updated_at,
                   COALESCE(s.total_hand_wins, 0) AS career_wins,
                   COALESCE(s.total_hands, 0)     AS career_hands,
                   COALESCE(s.win_rate_pct, 0)    AS win_rate_pct,
                   COALESCE(s.total_net_chips, 0) AS net_chips,
                   COALESCE(ja.good, 0)           AS good,
                   COALESCE(ja.total, 0)          AS total,
                   COALESCE(ja.acc, 0)            AS acc
              FROM v_bot_career c
              LEFT JOIN summary s ON s.bot_id = c.id
              LEFT JOIN v_judge_accuracy ja ON ja.bot_id = c.id
             ORDER BY COALESCE(c.elo,1500) DESC, c.matches DESC, c.hands DESC
        `)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []Row{}
		for rows.Next() {
			var x Row
			if err := rows.Scan(&x.BotID, &x.Model, &x.Company, &x.Elo, &x.Matches, &x.Hands, &x.Updated, &x.CareerWins, &x.CareerHands, &x.WinRatePct, &x.NetChips, &x.Good, &x.Total, &x.Acc); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			out = append(out, x)
		}
		writeJSON(w, map[string]any{"rows": out})
	})

	// Judge accuracy (MCJudge): good/total and accuracy per bot
	mux.HandleFunc("/api/judge-accuracy", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		rows, err := db.Query(ctx, `
			SELECT p.bot_id,
			       SUM(CASE WHEN e.is_top_action THEN 1 ELSE 0 END)::int AS good,
			       COUNT(*)::int AS total
			  FROM action_eval e
			  JOIN action_logs a ON a.id = e.action_log_id
			  JOIN match_participants p ON p.match_id = a.match_id AND p.label = a.actor_label
			 WHERE e.solver = 'MCJudge'
			 GROUP BY p.bot_id
		`)
		if err != nil { http.Error(w, err.Error(), 500); return }
		defer rows.Close()
		type Row struct{ BotID int64 `json:"bot_id"`; Good int `json:"good"`; Total int `json:"total"`; Acc float64 `json:"acc"` }
		var out []Row
		for rows.Next(){
			var x Row
			if err := rows.Scan(&x.BotID, &x.Good, &x.Total); err != nil { http.Error(w, err.Error(), 500); return }
			if x.Total > 0 { x.Acc = float64(x.Good)/float64(x.Total) }
			out = append(out, x)
		}
		writeJSON(w, map[string]any{"rows": out})
	})

	// Bot details: career row + recent matches for a given bot id
	mux.HandleFunc("/api/bot", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			http.Error(w, "missing id", 400)
			return
		}
		var botID int64
		if _, err := fmt.Sscan(idStr, &botID); err != nil {
			http.Error(w, "bad id", 400)
			return
		}

		// Career row
		var career struct {
			BotID   int64     `json:"bot_id"`
			Model   string    `json:"model"`
			Company string    `json:"company"`
			Elo     float64   `json:"elo"`
			GRating float64   `json:"g_rating"`
			GRD     float64   `json:"g_rd"`
			GSigma  float64   `json:"g_sigma"`
			Matches int       `json:"matches"`
			Hands   int       `json:"hands"`
			Updated time.Time `json:"updated_at"`
		}
		err := db.QueryRow(ctx, `
            SELECT id, name, company,
                   COALESCE(elo,1500), COALESCE(g_rating,1500), COALESCE(g_rd,350), COALESCE(g_sigma,0.06),
                   COALESCE(matches,0), COALESCE(hands,0), COALESCE(updated_at, now())
              FROM v_bot_career WHERE id = $1
        `, botID).Scan(&career.BotID, &career.Model, &career.Company, &career.Elo, &career.GRating, &career.GRD, &career.GSigma, &career.Matches, &career.Hands, &career.Updated)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}

		// Recent matches for this bot
		type M struct {
			MatchID    int64      `json:"match_id"`
			CreatedAt  time.Time  `json:"created_at"`
			EndedAt    *time.Time `json:"ended_at"`
			Label      string     `json:"label"`
			OppModel   string     `json:"opponent_model"`
			StartBank  int        `json:"start_bank"`
			EndBank    int        `json:"end_bank"`
			Wins       int        `json:"wins"`
			HandsDealt int        `json:"hands"`
			NetChips   int        `json:"net_chips"`
			Fold       int        `json:"fold"`
			Call       int        `json:"call"`
			Raise      int        `json:"raise"`
			Check      int        `json:"check"`
		}
		rows, err := db.Query(ctx, `
            WITH me AS (
                SELECT p.match_id, p.label, p.start_bank, p.end_bank, p.wins, p.hands_dealt, p.net_chips,
                       m.created_at, m.ended_at,
                       (SELECT name_snapshot FROM match_participants op WHERE op.match_id=p.match_id AND op.label <> p.label) AS opponent_model
                  FROM match_participants p
                  JOIN matches m ON m.id = p.match_id
                 WHERE p.bot_id = $1
            )
            SELECT me.match_id, me.created_at, me.ended_at, me.label, me.opponent_model,
                   me.start_bank, me.end_bank, me.wins, me.hands_dealt, me.net_chips,
                   COALESCE(t.fold_ct,0), COALESCE(t.call_ct,0), COALESCE(t.raise_ct,0), COALESCE(t.check_ct,0)
              FROM me
              LEFT JOIN action_tallies t ON t.match_id = me.match_id AND t.label = me.label
             ORDER BY me.match_id DESC
             LIMIT 100
        `, botID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		var list []M
		for rows.Next() {
			var m M
			if err := rows.Scan(&m.MatchID, &m.CreatedAt, &m.EndedAt, &m.Label, &m.OppModel, &m.StartBank, &m.EndBank, &m.Wins, &m.HandsDealt, &m.NetChips, &m.Fold, &m.Call, &m.Raise, &m.Check); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			list = append(list, m)
		}
		writeJSON(w, map[string]any{"career": career, "matches": list})
	})

	// Aggregated action mix for a bot across all matches (for playstyle badges)
	mux.HandleFunc("/api/bot-style", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			http.Error(w, "missing id", 400)
			return
		}
		var botID int64
		if _, err := fmt.Sscan(idStr, &botID); err != nil {
			http.Error(w, "bad id", 400)
			return
		}

		// Sum action tallies across all matches for this bot
		var checkCT, callCT, raiseCT, foldCT int
		err := db.QueryRow(ctx, `
            SELECT COALESCE(SUM(t.check_ct),0) AS check_ct,
                   COALESCE(SUM(t.call_ct),0)  AS call_ct,
                   COALESCE(SUM(t.raise_ct),0) AS raise_ct,
                   COALESCE(SUM(t.fold_ct),0)  AS fold_ct
              FROM action_tallies t
              JOIN match_participants p ON p.match_id = t.match_id AND p.label = t.label
             WHERE p.bot_id = $1
        `, botID).Scan(&checkCT, &callCT, &raiseCT, &foldCT)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		total := checkCT + callCT + raiseCT + foldCT
		// Guard against division by zero
		pct := func(x int) int {
			if total == 0 {
				return 0
			}
			return int((float64(x)/float64(total))*100.0 + 0.5)
		}
		checkPct := pct(checkCT)
		callPct := pct(callCT)
		raisePct := pct(raiseCT)
		foldPct := pct(foldCT)

		// Heuristic playstyle classification
		style := "TAG"
		switch {
		case total == 0:
			style = "N/A"
		case foldPct >= 55 && raisePct < 20:
			style = "NIT"
		case raisePct >= 35 && foldPct <= 45:
			style = "LAG"
		case callPct >= 40 && raisePct <= 20:
			style = "FISH"
		case raisePct >= 22 && callPct <= 35 && foldPct <= 50:
			style = "TAG"
		default:
			style = "TAG"
		}

		writeJSON(w, map[string]any{
			"bot_id":    botID,
			"total":     total,
			"check_pct": checkPct,
			"call_pct":  callPct,
			"raise_pct": raisePct,
			"fold_pct":  foldPct,
			"style":     style,
		})
	})

	// Live SSE stream of action logs for a given match_id.
	mux.HandleFunc("/api/live", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		q := r.URL.Query()
		matchIDStr := q.Get("match_id")
		if matchIDStr == "" {
			http.Error(w, "missing match_id", 400)
			return
		}
		// Optional: start from id>since
		sinceStr := q.Get("since")

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", 500)
			return
		}

		// parse inputs
		var matchID int64
		_, err := fmt.Sscan(matchIDStr, &matchID)
		if err != nil {
			http.Error(w, "bad match_id", 400)
			return
		}
		var sinceID int64
		if sinceStr != "" {
			if _, e := fmt.Sscan(sinceStr, &sinceID); e != nil {
				sinceID = 0
			}
		}

		type Row struct {
			ID          int64     `json:"id"`
			PairIndex   int       `json:"pair_index"`
			HandID      string    `json:"hand_id"`
			Street      string    `json:"street"`
			ActorLabel  string    `json:"actor_label"`
			Action      string    `json:"action"`
			Amount      *int      `json:"amount"`
			Pot         int       `json:"pot"`
			CurBet      int       `json:"cur_bet"`
			ToCall      int       `json:"to_call"`
			MinRaiseTo  int       `json:"min_raise_to"`
			MaxRaiseTo  int       `json:"max_raise_to"`
			SBStack     int       `json:"sb_stack"`
			BBStack     int       `json:"bb_stack"`
			SBCommitted int       `json:"sb_committed"`
			BBCommitted int       `json:"bb_committed"`
			Board       []string  `json:"board"`
			SBHole      []string  `json:"sb_hole"`
			BBHole      []string  `json:"bb_hole"`
			CreatedAt   time.Time `json:"created_at"`
		}

		enc := json.NewEncoder(w)
		send := func(rows []Row) {
			for _, r := range rows {
				w.Write([]byte("event: action\n"))
				w.Write([]byte("data: "))
				_ = enc.Encode(r)
				w.Write([]byte("\n"))
			}
			flusher.Flush()
		}

		// tail loop
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := db.Query(ctx, `
                    SELECT id, pair_index, hand_id, street, actor_label, action, amount,
                           pot, cur_bet, to_call, min_raise_to, max_raise_to,
                           sb_stack, bb_stack, sb_committed, bb_committed,
                           board, created_at
                      FROM action_logs
                     WHERE match_id = $1 AND id > $2
                     ORDER BY id
                `, matchID, sinceID)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				var batch []Row
				for rows.Next() {
					var r Row
					if err := rows.Scan(&r.ID, &r.PairIndex, &r.HandID, &r.Street, &r.ActorLabel, &r.Action, &r.Amount,
						&r.Pot, &r.CurBet, &r.ToCall, &r.MinRaiseTo, &r.MaxRaiseTo,
						&r.SBStack, &r.BBStack, &r.SBCommitted, &r.BBCommitted,
						&r.Board, &r.CreatedAt); err != nil {
						rows.Close()
						http.Error(w, err.Error(), 500)
						return
					}
					batch = append(batch, r)
					sinceID = r.ID
				}
				rows.Close()
				if len(batch) > 0 {
					send(batch)
				}
			}
		}
	})

	// Win matrix: pairwise A vs B totals (wins/hands)
	mux.HandleFunc("/api/matrix", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		type Bot struct {
			ID      int64   `json:"id"`
			Name    string  `json:"name"`
			Company string  `json:"company"`
			Elo     float64 `json:"elo"`
		}
		bots := []Bot{}
		rows, err := db.Query(ctx, `
            SELECT id, name, company, COALESCE(elo,1500) AS elo
              FROM v_bot_career
             ORDER BY name
        `)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var b Bot
			if err := rows.Scan(&b.ID, &b.Name, &b.Company, &b.Elo); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			bots = append(bots, b)
		}
		type Pair struct {
			AID   int64 `json:"a_id"`
			BID   int64 `json:"b_id"`
			AWins int   `json:"a_wins"`
			BWins int   `json:"b_wins"`
			Hands int   `json:"hands"`
		}
		pairs := []Pair{}
		rows2, err := db.Query(ctx, `
            SELECT p1.bot_id AS a_id, p2.bot_id AS b_id,
                   SUM(p1.wins) AS a_wins,
                   SUM(p2.wins) AS b_wins,
                   SUM(p1.hands_dealt) AS hands
              FROM match_participants p1
              JOIN match_participants p2 ON p1.match_id = p2.match_id AND p1.label <> p2.label
             WHERE p1.bot_id < p2.bot_id
             GROUP BY p1.bot_id, p2.bot_id
             ORDER BY a_id, b_id
        `)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows2.Close()
		for rows2.Next() {
			var p Pair
			if err := rows2.Scan(&p.AID, &p.BID, &p.AWins, &p.BWins, &p.Hands); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			pairs = append(pairs, p)
		}
		writeJSON(w, map[string]any{"bots": bots, "pairs": pairs})
	})

	// Elo history across matches per bot (end-of-match Elo and label mapping)
	mux.HandleFunc("/api/elo-history", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		type Row struct {
			BotID   int64     `json:"bot_id"`
			Model   string    `json:"model"`
			Company string    `json:"company"`
			MatchID int64     `json:"match_id"`
			When    time.Time `json:"when"`
			Elo     float64   `json:"elo"`
		}
		rows, err := db.Query(ctx, `
            SELECT p.bot_id,
                   p.name_snapshot AS model,
                   p.company_snapshot AS company,
                   m.id AS match_id,
                   m.created_at,
                   CASE WHEN p.label = 'A' THEN rh.elo_a ELSE rh.elo_b END AS elo
              FROM rating_history rh
              JOIN matches m ON m.id = rh.match_id
              JOIN match_participants p ON p.match_id = m.id
             WHERE rh.stage = 'end'
             ORDER BY p.bot_id, m.created_at
        `)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []Row{}
		for rows.Next() {
			var x Row
			if err := rows.Scan(&x.BotID, &x.Model, &x.Company, &x.MatchID, &x.When, &x.Elo); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			out = append(out, x)
		}
		writeJSON(w, map[string]any{"rows": out})
	})

	// Fetch all action logs for a past match (non-live replay)
	mux.HandleFunc("/api/match-logs", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := r.URL.Query().Get("match_id")
		var matchID int64
		if _, err := fmt.Sscan(idStr, &matchID); err != nil {
			http.Error(w, "bad match_id", 400)
			return
		}
    type Row struct {
        ID          int64     `json:"id"`
        PairIndex   int       `json:"pair_index"`
        HandID      string    `json:"hand_id"`
        Street      string    `json:"street"`
        ActorLabel  string    `json:"actor_label"`
        Action      string    `json:"action"`
        Amount      *int      `json:"amount"`
        Pot         int       `json:"pot"`
        CurBet      int       `json:"cur_bet"`
        ToCall      int       `json:"to_call"`
        MinRaiseTo  int       `json:"min_raise_to"`
        MaxRaiseTo  int       `json:"max_raise_to"`
        SBStack     int       `json:"sb_stack"`
        BBStack     int       `json:"bb_stack"`
        SBCommitted int       `json:"sb_committed"`
        BBCommitted int       `json:"bb_committed"`
        Board       []string  `json:"board"`
        SBHole      []string  `json:"sb_hole"`
        BBHole      []string  `json:"bb_hole"`
        CreatedAt   time.Time `json:"created_at"`
        // Optional solver eval join
        Solver          *string  `json:"solver"`
        SolverVersion   *string  `json:"solver_version"`
        EvalBestAction  *string  `json:"eval_best_action"`
        EvalBestTo      *int     `json:"eval_best_to"`
        EvalGapBB       *float64 `json:"eval_gap_bb"`
        EvalCorrectProb *float64 `json:"eval_correct_prob"`
        EvalIsTop       *bool    `json:"eval_is_top"`
        // Server-enriched winner at end of hand
        WinnerSeat     *string  `json:"winner_seat,omitempty"`
    }
		rows, err := db.Query(ctx, `
            SELECT a.id, a.pair_index, a.hand_id, a.street, a.actor_label, a.action, a.amount,
                   a.pot, a.cur_bet, a.to_call, a.min_raise_to, a.max_raise_to,
                   a.sb_stack, a.bb_stack, a.sb_committed, a.bb_committed,
                   a.board, a.sb_hole, a.bb_hole, a.created_at,
                   e.solver, e.solver_version, e.best_action, e.best_amount_to, e.ev_gap_bb, e.correctness_prob, e.is_top_action
              FROM action_logs a
              LEFT JOIN action_eval e ON e.action_log_id = a.id
             WHERE a.match_id = $1
             ORDER BY a.id
        `, matchID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
    out := []Row{}
    for rows.Next() {
        var r Row
        if err := rows.Scan(&r.ID, &r.PairIndex, &r.HandID, &r.Street, &r.ActorLabel, &r.Action, &r.Amount,
            &r.Pot, &r.CurBet, &r.ToCall, &r.MinRaiseTo, &r.MaxRaiseTo,
            &r.SBStack, &r.BBStack, &r.SBCommitted, &r.BBCommitted,
            &r.Board, &r.SBHole, &r.BBHole, &r.CreatedAt,
            &r.Solver, &r.SolverVersion, &r.EvalBestAction, &r.EvalBestTo, &r.EvalGapBB, &r.EvalCorrectProb, &r.EvalIsTop); err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        out = append(out, r)
    }
    // Enrich end-of-hand rows with winner seat (showdown or fold)
    parseCard := func(s string) (engine.Card, bool) {
        if len(s) < 2 {
            return engine.Card{}, false
        }
        rankCh := s[0]
        suitCh := s[1]
        var rank int
        switch rankCh {
        case 'A': rank = 14
        case 'K': rank = 13
        case 'Q': rank = 12
        case 'J': rank = 11
        case 'T': rank = 10
        default:
            if rankCh >= '2' && rankCh <= '9' { rank = int(rankCh - '0') }
        }
        if rank == 0 { return engine.Card{}, false }
        if suitCh != 'c' && suitCh != 'd' && suitCh != 'h' && suitCh != 's' { return engine.Card{}, false }
        return engine.Card{Rank: rank, Suit: suitCh}, true
    }
    computeShowdown := func(r Row) *string {
        if len(r.Board) < 5 || len(r.SBHole) != 2 || len(r.BBHole) != 2 { return nil }
        toCards := func(ss []string) ([]engine.Card, bool) {
            cs := make([]engine.Card, 0, len(ss))
            for _, s := range ss {
                if c, ok := parseCard(s); ok {
                    cs = append(cs, c)
                } else {
                    return nil, false
                }
            }
            return cs, true
        }
        board, ok1 := toCards(r.Board[:5])
        sb, ok2 := toCards(r.SBHole)
        bb, ok3 := toCards(r.BBHole)
        if !ok1 || !ok2 || !ok3 { return nil }
        h := &engine.Hand{Board: board, SB: &engine.Player{Seat: engine.SB, Hole: sb}, BB: &engine.Player{Seat: engine.BB, Hole: bb}}
        seat := string(h.Showdown())
        if seat == string(engine.SB) || seat == string(engine.BB) {
            return &seat
        }
        return nil
    }
    for idx := range out {
        isLast := idx == len(out)-1
        boundary := isLast || out[idx+1].HandID != out[idx].HandID
        if !boundary { continue }
        // Prefer showdown if available
        if ws := computeShowdown(out[idx]); ws != nil {
            out[idx].WinnerSeat = ws
            continue
        }
        // Fold fallback: last action was a fold -> winner is other label mapped by hand suffix
        r := out[idx]
        if strings.EqualFold(r.Action, "fold") && r.ActorLabel != "" {
            aIsSB := strings.HasSuffix(strings.ToUpper(r.HandID), "A")
            var seat string
            if r.ActorLabel == "A" { // A folded -> B wins
                if aIsSB { seat = "BB" } else { seat = "SB" }
            } else { // B folded -> A wins
                if aIsSB { seat = "SB" } else { seat = "BB" }
            }
            out[idx].WinnerSeat = &seat
        }
    }
    writeJSON(w, map[string]any{"rows": out})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// optional helper if you ever need a context with timeout inside handlers
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
