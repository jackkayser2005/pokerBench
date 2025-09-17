# PokerBench

Deterministic heads‑up No‑Limit Texas Hold’em benchmarking for LLM poker agents.

Run seed‑mirrored duels, compute Elo & Glicko‑2, log every action, evaluate EV with a Monte‑Carlo judge, and visualize results in a lightweight web UI.

---

## Tech Stack

* **Language:** Go (1.21+)
* **Database:** PostgreSQL (≥ 15)
* **UI:** HTML + CSS + vanilla JS (no framework)
* **Containers:** Docker & Docker Compose
* **LLM Providers:** Any OpenAI‑compatible endpoint (OpenAI, Azure, OpenRouter, Together, Groq, etc.)
* **Ratings:** Elo (custom) and Glicko‑2 (τ = 0.5) Glicko is still a work in progress at this moment. 
* **EV Judge:** Monte‑Carlo evaluator

---

## Features

* **Mirrored pairs**: A/SB vs B/BB, then swap seats with the **exact same deck** for fair comparisons.
* **Ratings**: Elo (per hand *or* per mirrored pair) and Glicko‑2 with sensible defaults.
* **Full action logs**: legal actions, `to_call`, `min_to`/`max_to`, chosen amounts.
* **Monte‑Carlo judge**: estimates EV‑correctness → displayed as **Acc** in the UI.
* **Web UI**: Leaderboard, pairwise matrix, Elo timeline, replay/history.
* **Pluggable LLMs**: configurable entirely via environment variables.

---

## Table of Contents

* [Quick Start](#quick-start)

  * [Option A — Docker Compose](#option-a--docker-compose)
  * [Option B — Local Go](#option-b--local-go)
* [Running Modes](#running-modes)

  * [Server mode (Web UI + API + DB)](#server-mode-web-ui--api--db)
  * [Duel mode (CLI)](#duel-mode-cli)
  * [Duel matrix (CLI)](#duel-matrix-cli)
* [Configuration](#configuration)

  * [.env.example](#envexample)
  * [Behavioral Knobs](#behavioral-knobs)
* [Database](#database)
* [Web UI](#web-ui)
* [APIs](#apis)
* [Data & Ratings](#data--ratings)
* [Repository Layout](#repository-layout)
* [Troubleshooting](#troubleshooting)
* [Security](#security)
* [License (AGPL-3.0)](#license-agpl-30)

---

## Quick Start

### Option A — Docker Compose

```bash
# 1) copy examples
cp compose.env.example compose.env
cp .env.example .env

# 2) edit compose.env and .env with your keys/models
#    - OPENAI_API_KEY=...
#    - OPENAI_MODEL_A=...
#    - OPENAI_MODEL_B=...

# 3) up
docker compose up --build
```

Open: [http://localhost:8080/web/leaderboard.html](http://localhost:8080/web/leaderboard.html)

### Option B — Local Go

Prereqs: Go 1.21+, Postgres.

```bash
# 1) deps
go mod download

# 2) env
cp .env.example .env
# edit .env (OPENAI_API_KEY, DATABASE_URL, etc.)

# 3) build & run server
go build -o server ./server
PORT=8080 AUTO_MIGRATE=1 ./server
```

---

## Running Modes

### Server mode (Web UI + API + DB)

Serves static UI under `/web/*` and JSON under `/api/*`.
Requires `DATABASE_URL` and `OPENAI_API_KEY`.

```bash
PORT=8080 \
AUTO_MIGRATE=1 \
DATABASE_URL="postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable" \
./server
# → http://localhost:8080/web/leaderboard.html
```

### Duel mode (CLI)

Run a seed‑mirrored duel and print a full log. If DB is configured, ratings persist.

```bash
OPENAI_MODEL_A="gpt-4o-mini" \
OPENAI_MODEL_B="gpt-4.1-mini" \
SB=50 BB=100 START_STACK=10000 DUEL_SEEDS=5 \
./server --duel
```

### Duel matrix (CLI)

Round‑robin pairwise duels for a comma‑separated model list.

```bash
OPENAI_MODELS="gpt-4o-mini,gpt-4.1-mini,meta-llama/llama-3.1-8b" \
SB=50 BB=100 START_STACK=10000 DUEL_SEEDS=5 \
./server --duel-matrix
```

---

## Configuration

### `.env.example`

Copy to `.env` and fill in values as needed.

```dotenv
# Core
OPENAI_API_KEY=your_key_here
OPENAI_MODEL=gpt-4o-mini  # default if seat-specific not set

# Server
PORT=8080
DATABASE_URL=postgres://poker:poker@db:5432/thunderdome?sslmode=disable
AUTO_MIGRATE=1

# Duel defaults
SB=50
BB=100
START_STACK=10000
DUEL_SEEDS=5

# Ratings
ELO_START=1500
ELO_K=24
ELO_PER_HAND=0         # 1 = per hand; 0 = per mirrored pair
ELO_WEIGHT_BY_POT=1

# Behavior (min-raise/probe pressure)
RAISE_ZERO_CALL_PROB=0.10
RAISE_FIRST_ZERO_CALL=0
DEBUG=0
```

**Seat-specific models (optional):**

* `OPENAI_MODEL_A`, `OPENAI_MODEL_B` (preferred), or
* `OPENAI_MODEL_SB`, `OPENAI_MODEL_BB`

**Provider knobs (optional):**

* `OPENAI_API_BASE` / `OPENAI_BASE_URL`, `OPENAI_ORG`
* `OPENAI_TEMPERATURE`, `OPENAI_TOP_P`, `OPENAI_TOP_K`
* `OPENAI_MAX_OUTPUT_TOKENS`, `OPENAI_REASONING_EFFORT=low|medium|high`
* `LLM_COMPANY` label override (UI)

---

## Behavioral Knobs

These settings shape model tendencies when no bet is faced and help avoid min‑raise spam.

* `RAISE_ZERO_CALL_PROB` (0..1): chance to flip a **check** into a **min‑raise** when `to_call == 0`.

  * Recommended: `0.10` baseline (or `0.0` to disable for clean benchmarks).
* `RAISE_FIRST_ZERO_CALL` (0/1): if `1`, list **raise** before **check** when `to_call == 0`.

  * Recommended: `0`.
* `ENCOURAGE_PROBE_ZERO` (legacy): adds a hint that probes are good when `to_call == 0`.

  * Recommended: leave **unset**; the default prompt is already conservative.

---

## Database

* Schema: `server/store/schema.sql`
* Store: `server/store/store.go`
* Enable `AUTO_MIGRATE=1` in server mode to auto‑apply migrations.

**Compose snippet (already included):**

```yaml
services:
  db:
    image: postgres:15
    environment:
      POSTGRES_USER: poker
      POSTGRES_PASSWORD: poker
      POSTGRES_DB: thunderdome
    ports: ["5432:5432"]
    volumes: [dbdata:/var/lib/postgresql/data]

  bench:
    build: .
    env_file:
      - compose.env
    ports: ["8080:8080"]
    depends_on: [db]

volumes:
  dbdata:
```

---

## Web UI

Static files live in `server/web/`:

* `leaderboard.html` — Elo, hands, win%, **Acc**, net, updated timestamp
* `matrix.html` — pairwise results
* `elo.html` — rating timeline
* `replay.html` / `history.html` — match & hand logs
* `about.html`

APIs consumed by the UI (subject to change during active development):

* `GET /api/leaderboard`
* `GET /api/judge-accuracy`

---

## APIs

High‑level summary of what the server exposes (not a formal spec):

* `GET /api/leaderboard` — rows containing bot/model, Elo, career hands, win rate %, net chips, updated\_at.
* `GET /api/judge-accuracy` — judge stats `{ bot_id, good, total, acc }` for “Acc” display.

---

## Data & Ratings

* **Mirrored pairs** ensure each agent plays each seat on the **same board** for fairness.
* **Elo** can update per hand or per mirrored pair; optional pot‑weighted scoring.
* **Glicko‑2** uses τ = 0.5; chip margin normalized by effective stack with `S = 0.5 + 0.5 * tanh(m)`.
* **EV Judge** runs post‑match Monte‑Carlo evaluation; aggregated as **Acc** in the UI.

---

## Repository Layout

```text
.
├─ server/
│  ├─ main.go                 # modes, envs, duel loop
│  ├─ router.go               # HTTP routes (web + api)
│  ├─ agent/                  # observation/contracts
│  ├─ engine/                 # cards/hand engine
│  ├─ judge/                  # Monte-Carlo EV judge
│  ├─ llm/                    # structured outputs / chat API helpers
│  ├─ store/                  # DB store + schema.sql
│  ├─ web/                    # static UI
│  ├─ elo_v2.go               # Elo
│  └─ glicko2.go              # Glicko-2
├─ scripts/                   # helper scripts
├─ Dockerfile
├─ docker-compose.yml
├─ .dockerignore
├─ .gitignore
├─ .env.example
└─ RUNS.md
```

---

## Troubleshooting

* **Windows: “LF will be replaced by CRLF”** — harmless. Prefer LF?

  ```powershell
  git config core.autocrlf false
  git add --renormalize .
  ```
* **Missing env vars** — set them in `.env` or `compose.env`.
* **Port 8080 busy** — change `PORT` or free the port.
* **Quick Git push over HTTPS (Windows):**

  ```powershell
  git remote set-url origin https://github.com/<you>/pokerBench.git
  git push -u origin main
  ```

---

## Security

* Do **not** commit real secrets (`.env`, API key files).
* Keep a sanitized `.env.example` for collaborators.
* `.dockerignore` excludes secrets from Docker build context.

---

## License (AGPL-3.0)

This project is licensed under the **GNU Affero General Public License v3.0 or later (AGPL‑3.0‑or‑later)**.
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

### Notes

To reduce min‑raise spam while keeping realistic defense rates:

```dotenv
RAISE_ZERO_CALL_PROB=0.10
RAISE_FIRST_ZERO_CALL=0
```

Use the leaderboard’s **Acc** as a regression check when you tweak prompts/knobs.

# Thank you to my good friend Tyler for getting my addicted to gambling, leading to this brainchild of mine 
# Shoutout to all of the open source libraries that aided the development of pokerBench, I hope this inspires others to make more benchmarks pertaining to games. 
