#!/usr/bin/env python3
"""Regenerate the staged seed packs used by PokerBench."""

from __future__ import annotations

import json
import random
from pathlib import Path

PACKS = {
    "S0-smoke": (50, 2024091501),
    "S1-200": (200, 2024091502),
    "S2-200": (200, 2024091503),
    "S3-200": (200, 2024091504),
    "S4-200": (200, 2024091505),
    "F1-500": (500, 2024091506),
    "F2-500": (500, 2024091507),
    "F3-500": (500, 2024091508),
    "F4-500": (500, 2024091509),
}

VERSION = "2024-09-25"
OUTPUT_DIR = Path(__file__).resolve().parent


def make_seeds(count: int, seed: int) -> list[int]:
    rng = random.Random(seed)
    seen: set[int] = set()
    seeds: list[int] = []
    while len(seeds) < count:
        value = rng.randrange(1_000_000, 1_000_000_000)
        if value in seen:
            continue
        seen.add(value)
        seeds.append(value)
    return seeds


def dump_pack(name: str, seeds: list[int]) -> None:
    payload = {
        "name": name.replace("-", " ").upper(),
        "version": VERSION,
        "seeds": seeds,
    }
    path = OUTPUT_DIR / f"{name}.json"
    path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
    print(f"wrote {path.relative_to(OUTPUT_DIR)} ({len(seeds)} seeds)")


def main() -> None:
    for name, (count, seed) in PACKS.items():
        dump_pack(name, make_seeds(count, seed))


if __name__ == "__main__":
    main()
