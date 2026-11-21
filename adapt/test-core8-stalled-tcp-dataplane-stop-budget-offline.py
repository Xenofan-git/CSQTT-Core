#!/usr/bin/env python3
"""Offline-only strict source-anchor tests for opt-in bounded dataplane stop.

No network/kernel access, no Core8 process, no production files changed.
"""
import importlib.util
from pathlib import Path

HERE=Path(__file__).resolve().parent
SCRIPT=HERE/"patch-core8-stalled-tcp-dataplane-stop-budget.py"
spec=importlib.util.spec_from_file_location("budget_patch",SCRIPT)
mod=importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

def rejects(source: str) -> bool:
    try:mod.patch(source)
    except ValueError:return True
    return False

valid="header\n"+mod.CONTEXT+"    pub fn shutdown(mut self) -> Result<()> {\n"+mod.ORIGINAL+"    }\n}\nfooter\n"
patched=mod.patch(valid)
assert patched.count("Duration::from_secs(6)") == 1
assert patched.count(mod.REPLACEMENT) == 1
assert mod.ORIGINAL not in patched
assert patched.split(mod.CONTEXT)[0] == valid.split(mod.CONTEXT)[0]
assert patched.endswith("footer\n")
print("OFFLINE_ONE_ANCHOR_REPLACEMENT_PASS")
assert rejects(patched)
print("OFFLINE_IDEMPOTENCE_REJECT_PASS")
assert rejects(valid.replace(mod.CONTEXT,"",1))
print("OFFLINE_MISSING_CONTEXT_REJECT_PASS")
assert rejects(valid.replace(mod.ORIGINAL,"missing shutdown block",1))
print("OFFLINE_MISSING_ANCHOR_REJECT_PASS")
assert rejects(valid+mod.ORIGINAL)
print("OFFLINE_DUPLICATE_ANCHOR_REJECT_PASS")
assert rejects(valid+mod.CONTEXT)
print("OFFLINE_DUPLICATE_RUNTIME_CONTEXT_REJECT_PASS")
print("CORE8_STALLED_TCP_SHUTDOWN_BUDGET_PATCH_OFFLINE_6_OF_6_PASS")
