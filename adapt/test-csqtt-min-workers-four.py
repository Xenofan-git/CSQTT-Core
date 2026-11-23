#!/usr/bin/env python3
"""Offline unit test for the minimal CSQTT Go worker lower-bound patch."""
import importlib.util
from pathlib import Path
import sys
from tempfile import TemporaryDirectory

root = Path(__file__).resolve().parent
source = root/"patch-csqtt-min-workers-four.py"
spec = importlib.util.spec_from_file_location("worker_patch", source)
assert spec and spec.loader
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
sample = "const (\n\tMinWorkers            = 1\n\tMaxWorkers            = 127\n)\n"
modified = mod.patch_text(sample)
assert "MinWorkers            = 4" in modified
assert mod.patch_text(modified) == modified
assert "MaxWorkers            = 127" in modified
for bad in ("const (MinWorkers = 2)\n", "no limit here\n"):
    try:
        mod.patch_text(bad)
    except ValueError:
        pass
    else:
        raise AssertionError("fail-closed patch accepted unrecognized source")
print("CSQTT_MIN_WORKERS_FOUR_OFFLINE_TEST_PASS")
