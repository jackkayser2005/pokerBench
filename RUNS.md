AI Poker Lab â€” OpenAI Model Matrix Plan

Scope
- Objective: benchmark recent OpenAI models across mirrored HUNL duels.
- Output: Leaderboard with Elo, career hands, win%, net chips; perâ€‘model bot page.

Models (example seed list)
- gpt-4o-mini
- gpt-4o
- gpt-4.1-mini-2025-04-14 (snapshot)
- gpt-5-mini (if enabled on your org)

Reasoning Effort
- Values: "", low, medium, high
- Sent through env OPENAI_REASONING_EFFORT; autoâ€‘omitted for models that reject it.

Run Format
- Mirrored pairs: P (each pair = 2 hands with swapped seats).
- Suggested: quick passes with P = 1â€“3; deeper passes with P = 25â€“75.
- Perâ€‘hand Elo updates (ELO_PER_HAND=1) for faster movement initially; switch to pair updates later if desired.

Budget and Time (orderâ€‘ofâ€‘magnitude)
- Latency: ~2â€“6s per LLM call depending on load; ~6â€“10 actions/hand â‰ˆ ~12â€“60s/hand.
- 150 hands/model â‰ˆ 30â€“150 minutes per model (broad range; measure 1 run and extrapolate).
- Cost: depends on model prices and tokens; start with ~$50â€“$100 for a multiâ€‘model day run.

Execution
1) Ensure secrets/openai_api_key.txt has your key (one line).
2) Start Postgres and app images with Docker Compose (or run locally). The compose file will autoâ€‘migrate when AUTO_MIGRATE=1.
3) Baseline (selfâ€‘play sweep, quick):
   PowerShell: scripts/run-openai-matrix.ps1 -Pairs 5 -Models "gpt-4o-mini,gpt-4o,gpt-4.1-mini-2025-04-14,gpt-5-mini" -Reasoning "","low"
   This runs A vs A for each model/effort to sanityâ€‘check JSON compliance and action mix.
4) Headâ€‘toâ€‘head (pairwise matrix): use --duel-matrix with OPENAI_MODELS.
   - Quick 15â€‘pair sample (6 models, 1 mirrored pair each):
     PowerShell:
       $env:OPENAI_MODELS='gpt-5,gpt-5-mini,o3,o3-mini,gpt-4o,gpt-4.1-mini-2025-04-14'; $env:OPENAI_REASONING_EFFORT='low'; $env:OPENAI_MAX_OUTPUT_TOKENS='32'; $env:DUEL_SEEDS='1'; docker compose run --rm duel /app/ai-thunderdome --duel-matrix
   - Deeper pass on top performers: bump DUEL_SEEDS to 25â€“75 and reduce model list to your finalists.

Notes
- scripts/run-openai-matrix.ps1 loops model + reasoning effort for selfâ€‘play; it sets OPENAI_MODEL_A/B to the same model and applies DUEL_SEEDS.
- For true pairwise headâ€‘toâ€‘head, use --duel-matrix with OPENAI_MODELS as shown above.
- The server persists to Postgres; Leaderboard/Matrix/Elo pages read from v_bot_career, v_bot_summary, rating_history.

Cost control
- Prefer smaller DUEL_SEEDS for dry runs (1â€“3).
- Set OPENAI_REASONING_EFFORT=low for reasoning models; itâ€™s autoâ€‘stripped for models that donâ€™t support it.
- Keep OPENAI_MAX_OUTPUT_TOKENS small (e.g., 32) â€” outputs are JSON only; most cost is input tokens.
- Use MAX_SECONDS to cap a run wallâ€‘clock time.

OpenRouter (multiâ€‘vendor via one key)
- Set env:
  - OPENAI_API_BASE=https://openrouter.ai/api/v1
  - LLM_COMPANY=OpenRouter
  - OPENAI_API_KEY=sk-or-v1-...
- Use model names with vendor prefixes, for example:
  - anthropic/claude-3.5-sonnet, google/gemini-1.5-pro, meta-llama/llama-3.1-405b-instruct, mistralai/mistral-large, deepseek/deepseek-reasoner
- Then use the same matrix scripts (selfâ€‘play or pairwise). The DB will record company=OpenRouter and model=<vendor/model>.

Static site export (no live DB)
- After running duels locally, export JSON snapshots for the site:
  - PowerShell: scripts/export-static.ps1 -BaseUrl http://localhost:8080 -OutDir server/web/data
- Pages now autoâ€‘fallback to /web/data/*.json when /api/* is unavailable:
  - leaderboard.json, matrix.json, elo-history.json, matches.json
  - bot-<id>.json, bot-style-<id>.json, match-logs-<match_id>.json
