#!/usr/bin/env python3
"""Opt-in minimal patch of pinned Go controller's worker lower bound.

Touch only MinWorkers constant. The original CSQTT upstream code is fetched
during each release build, so this small patch is repeated after pinning.
No unrelated library source is rewritten or vendored.
"""
import re
import sys
from pathlib import Path

def patch_text(source: str) -> str:
    old = re.compile(r"(?m)^(\s*MinWorkers\s*=\s*)(\d+)(\s*)$")
    matches = list(old.finditer(source))
    if len(matches) != 1:
        raise ValueError(f"expected exactly one MinWorkers constant, got {len(matches)}")
    value = int(matches[0].group(2))
    if value not in (1, 4):
        raise ValueError(f"unknown upstream MinWorkers={value}; refusing patch")
    changed = old.sub(lambda m: m.group(1) + "4" + m.group(3), source, count=1)
    if changed.count("MinWorkers") != source.count("MinWorkers"):
        raise ValueError("unexpected mutation")
    return changed

def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch-csqtt-min-workers-four.py path/to/Libs/common.go")
    path = Path(sys.argv[1])
    raw = path.read_text(encoding="utf-8")
    updated = patch_text(raw)
    if raw != updated:
        path.write_text(updated, encoding="utf-8")
    print("CSQTT_MIN_WORKERS_FOUR_PATCH_OK", path)

if __name__ == "__main__":
    main()
