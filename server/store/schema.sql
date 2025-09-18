-- =========================
-- MODELS
-- =========================
CREATE TABLE IF NOT EXISTS bots (
  id                BIGSERIAL PRIMARY KEY,
  name              TEXT NOT NULL UNIQUE,   -- 'gpt-5-mini'
  company           TEXT NOT NULL,          -- 'OpenAI' (or your vendor tag)
  reasoning_effort  TEXT,                   -- NULL for non-thinking models
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Persisted ratings so each bot continues from its last known skill
CREATE TABLE IF NOT EXISTS bot_ratings (
  bot_id     BIGINT PRIMARY KEY REFERENCES bots(id) ON DELETE CASCADE,
  elo        REAL NOT NULL DEFAULT 1500,
  g_rating   REAL NOT NULL DEFAULT 1500,
  g_rd       REAL NOT NULL DEFAULT 350,
  g_sigma    REAL NOT NULL DEFAULT 0.06,
  matches    INT  NOT NULL DEFAULT 0,
  hands      INT  NOT NULL DEFAULT 0,
  judge_good INT  NOT NULL DEFAULT 0,
  judge_total INT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE bot_ratings
  ADD COLUMN IF NOT EXISTS matches INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS hands   INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS judge_good INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS judge_total INT NOT NULL DEFAULT 0;

-- =========================
-- MATCHES (one row per duel run)
-- =========================
CREATE TABLE IF NOT EXISTS matches (
  id               BIGSERIAL PRIMARY KEY,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at         TIMESTAMPTZ,
  sb               INT NOT NULL,
  bb               INT NOT NULL,
  start_stack      INT NOT NULL,
  duel_seeds       INT NOT NULL,            -- number of mirrored pairs
  deck_seed_base   BIGINT NOT NULL,
  elo_start        REAL NOT NULL,
  elo_k            REAL NOT NULL,
  elo_per_hand     BOOL NOT NULL,
  elo_weight_by_pot BOOL NOT NULL
);

-- =========================
-- PARTICIPANTS (final snapshot per bot in a match)
-- =========================
CREATE TABLE IF NOT EXISTS match_participants (
  match_id         BIGINT NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
  label            CHAR(1) NOT NULL CHECK (label IN ('A','B')),
  bot_id           BIGINT NOT NULL REFERENCES bots(id),
  name_snapshot    TEXT NOT NULL,
  company_snapshot TEXT NOT NULL,
  reasoning_effort_snapshot TEXT,
  start_bank       INT NOT NULL,
  end_bank         INT NOT NULL,
  wins             INT NOT NULL DEFAULT 0,   -- hand wins (your counter)
  -- analytics counters
  hands_dealt      INT NOT NULL DEFAULT 0,
  hands_sb         INT NOT NULL DEFAULT 0,
  hands_bb         INT NOT NULL DEFAULT 0,
  net_chips        INT NOT NULL DEFAULT 0,
  PRIMARY KEY (match_id, label)
);

-- Backfill for existing DBs (safe no-ops on fresh DBs)
ALTER TABLE match_participants
  ADD COLUMN IF NOT EXISTS hands_dealt INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS hands_sb    INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS hands_bb    INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS net_chips   INT NOT NULL DEFAULT 0;

-- =========================
-- RATING HISTORY (timeline across a match)
-- =========================
CREATE TABLE IF NOT EXISTS rating_history (
  id          BIGSERIAL PRIMARY KEY,  -- <— surrogate key
  match_id    BIGINT NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
  stage       TEXT NOT NULL CHECK (stage IN ('start','after_pair','end')),
  pair_index  INT,                             -- NULL for start/end
  elo_a       REAL NOT NULL,
  elo_b       REAL NOT NULL,
  g_a_rating  REAL, g_a_rd REAL, g_a_sigma REAL,
  g_b_rating  REAL, g_b_rd REAL, g_b_sigma REAL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Enforce uniqueness of (match_id, stage, COALESCE(pair_index,0))
CREATE UNIQUE INDEX IF NOT EXISTS ux_rating_history_match_stage_pair
  ON rating_history (match_id, stage, COALESCE(pair_index, 0));

-- =========================
-- ACTION TALLIES (per-bot in the match)
-- =========================
CREATE TABLE IF NOT EXISTS action_tallies (
  match_id BIGINT NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
  label    CHAR(1) NOT NULL CHECK (label IN ('A','B')),
  check_ct INT NOT NULL,
  call_ct  INT NOT NULL,
  raise_ct INT NOT NULL,
  fold_ct  INT NOT NULL,
  PRIMARY KEY (match_id, label)
);

-- =========================
-- VIEWS
-- =========================
CREATE OR REPLACE VIEW v_match_action_mix AS
SELECT
  m.id AS match_id, p.label,
  p.name_snapshot  AS model,
  p.company_snapshot AS company,
  p.reasoning_effort_snapshot AS reasoning_effort,
  t.check_ct, t.call_ct, t.raise_ct, t.fold_ct,
  (t.check_ct + t.call_ct + t.raise_ct + t.fold_ct) AS total_actions,
  CASE WHEN (t.check_ct + t.call_ct + t.raise_ct + t.fold_ct) = 0 THEN 0
       ELSE ROUND(100.0 * t.fold_ct  / NULLIF((t.check_ct + t.call_ct + t.raise_ct + t.fold_ct),0)) END AS fold_pct,
  CASE WHEN (t.check_ct + t.call_ct + t.raise_ct + t.fold_ct) = 0 THEN 0
       ELSE ROUND(100.0 * t.call_ct  / NULLIF((t.check_ct + t.call_ct + t.raise_ct + t.fold_ct),0)) END AS call_pct,
  CASE WHEN (t.check_ct + t.call_ct + t.raise_ct + t.fold_ct) = 0 THEN 0
       ELSE ROUND(100.0 * t.raise_ct / NULLIF((t.check_ct + t.call_ct + t.raise_ct + t.fold_ct),0)) END AS raise_pct,
  CASE WHEN (t.check_ct + t.call_ct + t.raise_ct + t.fold_ct) = 0 THEN 0
       ELSE ROUND(100.0 * t.check_ct / NULLIF((t.check_ct + t.call_ct + t.raise_ct + t.fold_ct),0)) END AS check_pct
FROM matches m
JOIN match_participants p ON p.match_id = m.id
JOIN action_tallies t     ON t.match_id = m.id AND t.label = p.label;

-- Recreate to allow column order/name changes safely during upgrades
DROP VIEW IF EXISTS v_bot_summary;
CREATE VIEW v_bot_summary AS
WITH per_match AS (
  SELECT
    p.bot_id,
    p.name_snapshot AS model,
    p.company_snapshot AS company,
    p.reasoning_effort_snapshot AS reasoning_effort,
    p.wins,
    p.hands_dealt,
    p.net_chips,
    (SELECT fold_ct::float / NULLIF((check_ct+call_ct+raise_ct+fold_ct),0)
       FROM action_tallies t
      WHERE t.match_id = p.match_id AND t.label = p.label) AS fold_rate
  FROM match_participants p
)
SELECT
  bot_id, model, company, reasoning_effort,
  COUNT(*)                                   AS matches,
  SUM(wins)                                  AS total_hand_wins,
  SUM(hands_dealt)                           AS total_hands,
  COALESCE(SUM(net_chips),0)                 AS total_net_chips,
  ROUND(100.0 * COALESCE(SUM(wins)::float / NULLIF(SUM(hands_dealt),0), 0)) AS win_rate_pct,
  ROUND(100*AVG(fold_rate))                  AS avg_fold_pct
FROM per_match
GROUP BY bot_id, model, company, reasoning_effort
ORDER BY matches DESC;

-- Recreate to allow column order/name changes safely during upgrades
DROP VIEW IF EXISTS v_bot_career;
CREATE VIEW v_bot_career AS
SELECT
  b.id, b.name, b.company, r.elo,
  r.g_rating, r.g_rd, r.g_sigma, r.matches, r.hands,
  r.judge_good, r.judge_total, r.updated_at
FROM bots b
LEFT JOIN bot_ratings r ON r.bot_id = b.id
ORDER BY b.name;

-- =========================
-- ACTION LOGS (per-action event stream for live viewing)
-- =========================
CREATE TABLE IF NOT EXISTS action_logs (
  id             BIGSERIAL PRIMARY KEY,
  match_id       BIGINT NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
  pair_index     INT NOT NULL,             -- 1..N per duel
  hand_id        TEXT NOT NULL,            -- e.g., "duel-3A"
  street         TEXT NOT NULL,            -- preflop|flop|turn|river
  actor_label    CHAR(1) NOT NULL,         -- 'A' or 'B'
  action         TEXT NOT NULL,            -- fold|check|call|raise
  amount         INT,                      -- NULL for check/fold; call amount; raise-to for raise
  -- snapshot fields to render state without re-simulating
  pot            INT NOT NULL,
  cur_bet        INT NOT NULL,
  to_call        INT NOT NULL,
  min_raise_to   INT NOT NULL,
  max_raise_to   INT NOT NULL,
  sb_stack       INT NOT NULL,
  bb_stack       INT NOT NULL,
  sb_committed   INT NOT NULL,
  bb_committed   INT NOT NULL,
  board          TEXT[] NOT NULL DEFAULT '{}',
  sb_hole        TEXT[] NOT NULL DEFAULT '{}',
  bb_hole        TEXT[] NOT NULL DEFAULT '{}',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_action_logs_match_pair_id
  ON action_logs (match_id, pair_index, id);

-- Backfill columns for older databases
ALTER TABLE action_logs
  ADD COLUMN IF NOT EXISTS sb_hole TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE action_logs
  ADD COLUMN IF NOT EXISTS bb_hole TEXT[] NOT NULL DEFAULT '{}';

-- =========================
-- SOLVER EVALUATION (per action, optional)
-- =========================
CREATE TABLE IF NOT EXISTS action_eval (
  id               BIGSERIAL PRIMARY KEY,
  action_log_id    BIGINT NOT NULL UNIQUE REFERENCES action_logs(id) ON DELETE CASCADE,
  solver           TEXT NOT NULL,            -- e.g., GTOWizard, Pio, SimplePostflop
  solver_version   TEXT,
  abstraction      TEXT,                     -- sizing abstraction or config id
  policy_json      JSONB,                    -- distribution over legal actions/sizes
  evs_json         JSONB,                    -- EVs per action/size bucket
  best_action      TEXT,                     -- fold|check|call|raise
  best_amount_to   INT,                      -- absolute raise-to if raise
  chosen_action    TEXT,                     -- from logs (redundant but handy)
  chosen_amount_to INT,
  ev_chosen        REAL,
  ev_best          REAL,
  ev_gap_bb        REAL,                     -- EV(best) - EV(chosen) in big blinds
  correctness_prob REAL,                     -- policy prob for chosen action
  is_top_action    BOOL,
  compute_ms       INT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_action_eval_action ON action_eval(action_log_id);

-- Aggregated judge accuracy per bot for MCJudge
DROP VIEW IF EXISTS v_judge_accuracy;
CREATE VIEW v_judge_accuracy AS
SELECT p.bot_id,
       SUM(CASE WHEN e.is_top_action THEN 1 ELSE 0 END)::int AS good,
       COUNT(*)::int AS total,
       CASE WHEN COUNT(*) > 0
            THEN (SUM(CASE WHEN e.is_top_action THEN 1 ELSE 0 END)::float / COUNT(*))
            ELSE 0.0
       END AS acc
  FROM action_eval e
  JOIN action_logs a ON a.id = e.action_log_id
  JOIN match_participants p ON p.match_id = a.match_id AND p.label = a.actor_label
 WHERE e.solver = 'MCJudge'
 GROUP BY p.bot_id;

-- Backfill persisted judge accuracy aggregates
UPDATE bot_ratings br
   SET judge_good = COALESCE(ja.good, br.judge_good),
       judge_total = COALESCE(ja.total, br.judge_total)
  FROM v_judge_accuracy ja
 WHERE ja.bot_id = br.bot_id;

