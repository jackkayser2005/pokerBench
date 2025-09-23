# Seed packs

These JSON files back the staged duel workflow described in the PokerBench README. Each file exposes a
curated list of mirrored seeds so you can replay the smoke test, staged screening rounds, and finals
without regenerating data.

| Pack | Pairs | Purpose |
| ---- | ----- | ------- |
| `S0-smoke.json` | 50 | Quick sanity block; stop immediately afterwards. |
| `S1-200.json` – `S4-200.json` | 200 each | Stage A–D screening; append sequentially until the confidence interval is tight enough. |
| `F1-500.json` – `F4-500.json` | 500 each | Finals packs for top matchups. |

The seed values were generated with deterministic PRNG seeds so they stay stable across clones. If you
need to regenerate the packs, run the helper snippet below (Python 3.8+):

```bash
python seedpacks/generate.py
```

and commit the resulting JSON.
