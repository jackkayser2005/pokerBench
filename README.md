# PokerBench

[![Go Version](https://img.shields.io/badge/Go-1.21%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15%2B-336791?logo=postgresql&logoColor=white)](https://www.postgresql.org/)
[![Docker Compose](https://img.shields.io/badge/Docker-Ready-2496ED?logo=docker&logoColor=white)](docker-compose.yml)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](https://www.gnu.org/licenses/agpl-3.0.txt)

Deterministic heads-up No-Limit Texas Hold'em benchmarking for LLM poker agents.
Run mirrored-seed duels, compute Elo & Glicko-2, log every action, audit EV with a Monte-Carlo judge, and explore everything in a lightweight browser UI.

---

## Table of Contents

- [Highlights](#highlights)
- [Architecture at a Glance](#architecture-at-a-glance)
- [Quick Start](#quick-start)
  - [Option A — Docker Compose](#option-a--docker-compose)
  - [Option B — Local Go](#option-b--local-go)
- [Running Modes](#running-modes)
  - [Server Mode (Web UI + API + DB)](#server-mode-web-ui--api--db)
  - [Duel Mode](#duel-mode)
  - [Duel Matrix Mode](#duel-matrix-mode)
- [Configuration](#configuration)
  - [Required Secrets & Environment](#required-secrets--environment)
  - [Behavioral Knobs](#behavioral-knobs)
- [Database & Persistence](#database--persistence)
- [Web UI](#web-ui)
- [APIs](#apis)
- [Data & Ratings](#data--ratings)
- [Development Notes](#development-notes)
- [Troubleshooting](#troubleshooting)
- [Security](#security)
- [Acknowledgements](#acknowledgements)
- [License (AGPL-3.0-or-later)](#license-agpl-30-or-later)

---

## Highlights

- **Mirrored pairs:** Each duel plays both seat assignments (SB/BB) with the exact same shuffled deck for fair comparisons.
- **Ratings built in:** Live Elo (per hand or per mirrored pair) and a Glicko-2 implementation (τ = 0.5) with sensible defaults.
- **Detailed telemetry:** Structured action logs capture legal actions, `to_call`, `min_to`/`max_to`, chosen amounts, stack states, and boards.
- **Monte-Carlo EV judge:** Post-match rollouts estimate accuracy (displayed as **Acc** in the UI) to catch prompt regressions.
- **Minimalistic UI:** Leaderboard, pairwise matrix, Elo timeline, replay/history, and drill-downs – no heavy JS framework required.
- **Unified API:** Routes every match through OpenRouter so you can mix vendors without juggling provider-specific quirks.
- **Container-friendly:** Docker Compose stack ships Postgres, migrator, duel runner, and the web server.

## Architecture at a Glance

```text
.
├─ server/
│  ├─ main.go               # entrypoint, env handling, duel loop
│  ├─ router.go             # HTTP routes (web + JSON APIs)
│  ├─ agent/                # agent observation + contract structs
│  ├─ engine/               # card utilities, heads-up logic
│  ├─ judge/                # Monte-Carlo EV evaluator
│  ├─ llm/                  # structured prompts + chat helpers
│  ├─ store/                # PostgreSQL store + schema.sql
│  └─ web/                  # static leaderboard / history UI
├─ scripts/                 # helper PowerShell scripts for Windows
├─ docker-compose.yml       # db + server + duel runner stack
├─ Dockerfile               # multi-stage builder for the Go binary
└─ compose.env.example      # sample environment configuration
```

---

## Quick Start

### Option A — Docker Compose

```bash
# 1) copy the sample envs (edit them with your models/key)
cp compose.env.example compose.env
mkdir -p secrets
printf 'or-key-...' > secrets/openrouter_api_key.txt

# 2) launch the stack
docker compose up --build
```

`docker compose` binds `./secrets` into every container. Supplying
`OPENROUTER_API_KEY` or `secrets/openrouter_api_key.txt` is enough for both the
server and the duel runner.

The server becomes available at [http://localhost:8080/web/leaderboard.html](http://localhost:8080/web/leaderboard.html).
`docker compose` also spins up:

- `app` – web UI + API + duel scheduler (server mode)
- `duel` – continuous mirrored matches against configured models
- `migrate` – one-shot database migrations
- `db` – PostgreSQL 16 with persistent volume `pgdata`

### Option B — Local Go

Requirements: Go 1.21+, PostgreSQL 15+, and an OpenRouter API key.

```bash
# 1) install dependencies
go mod download

# 2) set environment (either export or place in a local .env)
export OPENROUTER_API_KEY=or-key-...
export DATABASE_URL="postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable"
export OPENROUTER_MODEL_A="meta-llama/llama-3.1-70b-instruct"
export OPENROUTER_MODEL_B="mistralai/mistral-nemo"

# 3) build & run the server
cd server
go build -o ../ai-thunderdome
cd ..
PORT=8080 AUTO_MIGRATE=1 ./ai-thunderdome
```

Open [http://localhost:8080/web/leaderboard.html](http://localhost:8080/web/leaderboard.html) once the binary is running.

### Seed orchestration

The duel loop decides which mirrored deck to deal before every pair, letting you script deterministic blocks when benchmarking agents.

1. **Pin the pseudo-random stream.** Export a numeric `DECK_SEED` (alongside `DUEL_SEEDS`) so successive runs replay the same mirrored sequence using the `seedStream` generator before each pair.
2. **Load curated packs.** Set `SEEDPACK_PATH=/path/to/pack.json` (or `SEEDPACK` for the same path) to feed a JSON file such as `{"name":"stage-1","version":"1.0","seeds":[...numbers...]}`. The loader validates the file, computes its hash, and the duel loop iterates through each listed seed in order.
3. **Chain stages.** Split a large evaluation into multiple pack files (`stage-1.json`, `stage-2.json`, …) or regenerate the pack between runs. Each block is consumed sequentially, so swapping in the next pack continues from the following seed block without rerunning prior pairs.

---

## Running Modes

### Server Mode (Web UI + API + DB)

Serve the leaderboard and JSON APIs while orchestrating duels (if enabled):

```bash
PORT=8080 \
AUTO_MIGRATE=1 \
DATABASE_URL="postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable" \
./ai-thunderdome
# → http://localhost:8080/web/leaderboard.html
```

### Duel Mode

Run a single mirrored duel and print the full log. Ratings persist if a database is configured.

```bash
OPENROUTER_MODEL_A="meta-llama/llama-3.1-70b-instruct" \
OPENROUTER_MODEL_B="mistralai/mistral-nemo" \
SB=50 BB=100 START_STACK=10000 DUEL_SEEDS=5 \
./ai-thunderdome --duel
```

### Duel Matrix Mode

Evaluate multiple model matchups across a matrix of seeds:

```bash
OPENROUTER_MODELS='meta-llama/llama-3.1-70b-instruct,mistralai/mistral-nemo' \
DUEL_SEEDS=5 \
./ai-thunderdome --duel-matrix
```

Set `OPENROUTER_MODELS` when you want the duel matrix to cycle through a list of vendors/models via OpenRouter.

Personal OpenRouter keys expect the caller to include the site metadata headers. Export `OPENROUTER_SITE_URL` (and optionally `OPENROUTER_TITLE`) so the client can attach them automatically:

```bash
export OPENROUTER_SITE_URL="https://your-app.example.com"
export OPENROUTER_TITLE="PokerBench (local dev)"
```

The site URL defaults to `http://localhost`, keeping local development simple while still allowing production deployments to report their own domain.

Windows-friendly PowerShell helpers live in `scripts/run-openrouter-pairwise.ps1` and `scripts/run-openrouter-matrix.ps1`.

---

## Configuration

### Required Secrets & Environment

| Variable | Purpose | Default |
| --- | --- | --- |
| `OPENROUTER_API_KEY` / `OPENROUTER_API_KEY_FILE` | OpenRouter auth token (env var or file in `./secrets`). | _(required)_ |
| `OPENROUTER_SITE_URL` | Referer value registered with OpenRouter (falls back to `http://localhost`). | `http://localhost` |
| `OPENROUTER_TITLE` | Optional app name advertised to OpenRouter. | `PokerBench` |
| `OPENROUTER_MODEL` | Shared OpenRouter identifier when both seats use the same model. | _(required)_ |
| `OPENROUTER_MODEL_A` / `OPENROUTER_MODEL_B` | Seat-specific model overrides. | `OPENROUTER_MODEL` fallback |
| `OPENROUTER_MODEL_SB` / `OPENROUTER_MODEL_BB` | SB/BB naming for the same overrides. | `OPENROUTER_MODEL` fallback |
| `OPENROUTER_REASONING_EFFORT` | Provider-specific reasoning hint (e.g., `medium`, `high`). | unset |
| `OPENROUTER_MAX_OUTPUT_TOKENS` | Hard cap on model responses. | provider default |
| `OPENROUTER_TEMPERATURE` / `OPENROUTER_TOP_P` / `OPENROUTER_TOP_K` | Sampling controls forwarded to OpenRouter. | provider default |
| `DATABASE_URL` | PostgreSQL DSN (`postgres://user:pass@host:port/db?sslmode=`). | `postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable` |
| `PORT` | HTTP port for the server mode. | `8080` |
| `LLM_COMPANY` | Label used in the UI (e.g., `OpenRouter`, `Anthropic`). | derived |
| `AUTO_MIGRATE` | Run database migrations on startup (recommended in dev). | `0` |
### Behavioral Knobs

Fine-tune duel behavior using environment variables:

| Variable | Description |
| --- | --- |
| `SB`, `BB`, `START_STACK` | Configure blind sizes and initial stack depth. |
| `DUEL_SEEDS` | Number of mirrored pairs per duel. (`DUEL_HANDS` can also be supplied; it is converted to seeds.) |
| `DECK_SEED`, `SEEDPACK`, `SEEDPACK_PATH` | Fix the mirrored deck stream (`DECK_SEED`) or load a JSON file of explicit seeds (`SEEDPACK_PATH` / `SEEDPACK`) for reproducible or staged runs. |
| `ELO_START`, `ELO_K`, `ELO_PER_HAND`, `ELO_WEIGHT_BY_POT` | Control Elo initialization and update cadence. |
| `RAISE_ZERO_CALL_PROB` | Probability of probing when `to_call == 0` to reduce auto-check loops. |
| `RAISE_FIRST_ZERO_CALL` | Force a raise on the first zero-to-call spot (0 disables). |
| `FORCE_NONCHECK` | Encourage the bot away from checks when legal alternatives exist. |
| `USE_TOOLS` | Toggle tool usage in multi-turn chat completions. |
| `MAX_SECONDS`, `STOP_FILE`, `STOP_IMMEDIATE` | Graceful shutdown controls for long-running benchmarks. |
| `NO_COLOR`, `USE_COLOR`, `DEBUG` | CLI output formatting and verbose state dumps. |

Set `DECK_SEED` when you need mirrored decks to replay exactly; point `SEEDPACK_PATH` (or `SEEDPACK`) at a JSON list of seeds to walk a curated set. The duel loop consumes each entry sequentially—replaying the same seed for both halves of a mirrored pair—so you can split long campaigns into predictable stages without repeating earlier cards.

---

## Database & Persistence

PostgreSQL schema lives in [`server/store/schema.sql`](server/store/schema.sql) and includes:

- **`bots` / `bot_ratings`:** Persistent Elo + Glicko-2 state so each bot resumes from prior strength estimates.
- **`matches`:** One row per duel run with stack, blind, seed, and rating configuration metadata.
- **`match_participants`:** Final bankroll snapshot, win counters, and derived analytics per bot.
- **`action_tallies` & `action_logs`:** Aggregated stats plus a full action stream (seat, action, amount, board, stacks) for replaying hands.
- **`rating_history`:** Timeline of Elo/Glicko trajectories across each mirrored pair.
- **Views (`v_match_action_mix`, `v_bot_summary`, `v_bot_career`):** Pre-joined material for the leaderboard and analytics dashboards.

Enable `AUTO_MIGRATE=1` to automatically run schema migrations on startup, or execute `./ai-thunderdome --migrate` when deploying manually.

---

## Web UI

Static assets under [`server/web`](server/web) are embedded into the binary via `go:embed`. The UI offers:

- **Leaderboard:** Aggregated Elo, win-rate confidence intervals, judge accuracy, and net chips.
- **Pairwise matrix:** Compare head-to-head performance across bots.
- **History timeline:** Scrollable list of recent matches with metadata snapshots.
- **Match replays:** Inspect action mixes, reasoning effort tags, and rating deltas per mirrored pair.

No external framework is required—plain HTML, CSS, and vanilla JavaScript keep the footprint light.

---

## APIs

High-level JSON endpoints exposed by the server include:

- `GET /api/leaderboard` — Rows containing bot/model metadata, Elo, career hands, win-rate %, net chips, and timestamps.
- `GET /api/judge-accuracy` — Monte-Carlo judge stats `{ bot_id, good, total, acc }` for the **Acc** column.
- `GET /api/matches` — Recent match history for the UI.
- `GET /api/last-match` — Full bundle for the most recent duel (participants, action mix, rating timeline).
- `GET /api/action-log?match_id=...` — Stream-friendly breakdown of actions per hand.

---

## Data & Ratings

The benchmarking pipeline emphasizes repeatability and transparent rating math.

### Mirrored Pair Normalization

- Every duel is scheduled as a mirrored seed pair so that each agent plays both **small blind** and **big blind** on the **identical shuffled deck**.
- Chip margins are normalized by the effective stack depth before being converted into a score. If `m` is the mean chip differential in big blinds, the logistic squashing
  \[
  S = 0.5 + 0.5 \tanh(m)
  \]
  maps unbounded chip margins into \([0, 1]\) while preserving directionality.

### Elo Update Path

- The expected score for agent A against agent B follows the classical logistic model
  \[
  E_A = \frac{1}{1 + 10^{(R_B - R_A)/400}}, \qquad E_B = 1 - E_A.
  \]
- After each hand (or mirrored pair, depending on configuration) the realized score \(S_A\) is combined with an optional **pot-weight** term \(w\) to yield the rating delta
  \[
  \Delta R_A = K \cdot w \cdot (S_A - E_A), \qquad \Delta R_B = -\Delta R_A,
  \]
  where \(K\) and \(w\) are governed by `ELO_K`, `ELO_PER_HAND`, and `ELO_WEIGHT_BY_POT`.
- Ratings are initialized via `ELO_START` and persist through `bot_ratings` so rematches pick up the correct prior.

### Glicko-2 Implementation

- PokerBench follows the original Glicko-2 formulation with system constant \(\tau = 0.5\).
- Ratings are expressed as \(\mu = (R - 1500)/173.7178\) with deviation \(\phi\) and volatility \(\sigma\). For each opponent \(j\), the scale term and expectation are
  \[
  g(\phi_j) = \frac{1}{\sqrt{1 + 3\phi_j^2/\pi^2}}, \qquad E(\mu, \mu_j) = \frac{1}{1 + e^{-g(\phi_j)(\mu - \mu_j)}}.
  \]
- The variance accumulator
  \[
  v^{-1} = \sum_j g(\phi_j)^2 E(\mu, \mu_j) (1 - E(\mu, \mu_j))
  \]
  and the score term \(\Delta = v \sum_j g(\phi_j) (S_j - E(\mu, \mu_j))\) feed the volatility update solved via the iterative `f(x)` root finding recommended by Glickman.
- Updated \(\phi'\) and \(\mu'\) are finally converted back to rating units using \(R' = 173.7178\, \mu' + 1500\).

### Monte-Carlo EV Judge

- After a duel completes, the Monte-Carlo judge replays each terminal hand with stochastic rollouts to approximate counterfactual values.
- The judge reports **good** versus **total** decisions, which aggregate into the **Acc** column of the leaderboard via
  \[
  \text{Acc} = \frac{\text{good}}{\text{total}}.
  \]
- Consistently monitoring **Acc** helps detect regression when adjusting prompts, reasoning depth, or heuristic knobs.

Use the leaderboard’s **Acc** column as a regression check whenever you tweak prompts or knob settings.

---

## Development Notes

- Build locally with `go build ./server` (binary defaults to `ai-thunderdome`).
- Run unit tests with `go test ./...`.
- When embedding new static assets run `go generate ./...` if you add `//go:generate` directives (none are required today).
- Keep secrets out of git; `.dockerignore` and `.gitignore` already exclude common sensitive files.

---

## Troubleshooting

- **Missing env vars:** Double-check `.env`, `compose.env`, or exported variables before launching.
- **Port 8080 busy:** Override with `PORT=...` or free the port.
- **Windows line endings:** Disable autocrlf for cleaner diffs:
  ```powershell
  git config core.autocrlf false
  git add --renormalize .
  ```
- **Secret file missing:** Ensure `./secrets/openrouter_api_key.txt` exists (one line with your key) or export the env var directly.

---

## Security

- Never commit real secrets—use environment variables or the git-ignored `./secrets` drop-in files.
- Keep a sanitized `compose.env.example` for collaborators.
- `.dockerignore` prevents leaking local secrets into build contexts.

---

## Acknowledgements

Huge thanks to Tyler for inspiring the project and to the open-source community for the libraries that power PokerBench. May it spark more game-specific evaluation suites.

---

## License (AGPL-3.0-or-later)

This project is licensed under the **GNU Affero General Public License v3.0 or later (AGPL-3.0-or-later)**.
It ensures that if you deploy modified versions over a network, you must also make the source available.

Add a `LICENSE` file with the following boilerplate:

<details>
<summary><code>LICENSE</code> (AGPL-3.0-or-later)</summary>

```text
Copyright (C) 2025  Jack Kayser

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
```

Full text: [https://www.gnu.org/licenses/agpl-3.0.txt](https://www.gnu.org/licenses/agpl-3.0.txt)

</details>

Add SPDX headers to source files if you like:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
```

---

Use the **Acc** metric and mirrored duels as regression tests whenever you adjust prompts, reasoning effort, or raise heuristics. Happy benchmarking!
