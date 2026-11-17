#!/usr/bin/env python3
"""Validate opt-in diagnostics on the actual pinned generated Rust source."""
from pathlib import Path
import argparse
import importlib.util
import tempfile

parser=argparse.ArgumentParser()
parser.add_argument('--source',required=True,type=Path)
args=parser.parse_args()
spec=importlib.util.spec_from_file_location(
    "csqtt_policy_diag",Path(__file__).with_name("instrument-csqtt-core-startup-cleanup.py"))
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

original=args.source.read_text(encoding="utf-8")
assert original.count(module.INSTRUMENTED)==1, "derived runtime is missing startup diagnostics"
assert module.ORIGINAL not in original, "old cleanup block remains"
block=original.split("    pub async fn cleanup_orphaned_policy() -> Result<()> {",1)[1].split("\n    }\n",1)[0]
assert 'CSQTT_CORE_DIAG_POLICY_CLEANUP' in block
stages=("lock_wait","lock_acquired","shared_start","shared_done",
        "legacy_start","legacy_done","verify_start","verify_done")
for stage in stages:
    assert f'stage={stage} ' in block,stage
assert block.index("ACTIVE_RUNTIME.store") < block.index("cleanup_shared_policy().await")
assert block.index("cleanup_shared_policy().await") < block.index("cleanup_legacy_proxy_policy().await")
assert block.index("cleanup_legacy_proxy_policy().await") < block.index("verify_proxy_policy_clean().await")
assert 'let verification = verify_proxy_policy_clean().await;' in block
assert block.rstrip().endswith("verification"),"must preserve original verification result"
assert 'Duration::from_secs(1)' not in block  # no policy timeout changes hidden here
with tempfile.TemporaryDirectory() as tmp:
    path=Path(tmp)/"proxy_route.rs"
    path.write_text(original)
    try:
        module.apply(path)
        raise AssertionError("repeat application must be rejected, not duplicate Rust code")
    except ValueError:
        assert path.read_text() == original
print("PASS pinned startup policy cleanup diagnostic stages, opt-in gating, preserved cleanup order, strict patcher")
