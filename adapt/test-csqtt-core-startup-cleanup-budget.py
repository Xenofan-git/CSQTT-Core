#!/usr/bin/env python3
"""Strict regression of derived Rust startup cleanup budget and source isolation."""
from pathlib import Path
import argparse
import importlib.util
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--source", required=True, type=Path)
parser.add_argument("--proxy-source", required=True, type=Path)
args = parser.parse_args()
spec = importlib.util.spec_from_file_location(
    "csqtt_cleanup_budget", Path(__file__).with_name("extend-csqtt-core-startup-cleanup-budget.py"))
budget = importlib.util.module_from_spec(spec)
spec.loader.exec_module(budget)

main = args.source.read_text(encoding="utf-8")
proxy = args.proxy_source.read_text(encoding="utf-8")
assert budget.OLD not in main
assert main.count(budget.NEW) == 1, "derived main.rs must have exactly one updated cleanup timeout"
assert "Duration::from_secs(15)" in main
assert main.count("proxy_route::ProxyRoute::cleanup_orphaned_policy()") == 1
assert 'Err(_) => eprintln!("[PROXY] startup policy cleanup timed out")' in main
assert "const COMMAND_TIMEOUT: Duration = Duration::from_secs(2);" in proxy, "per-command timeout must be unchanged"
assert 'command.kill_on_drop(true);' in proxy, "outer cancellation must continue to stop child subprocesses"
assert 'eprintln!("[PROXY-CLEANUP] stage=shared_done' in proxy
assert "CSQTT_CORE_TPROXY" in proxy and "CSQTT_LOCAL_SOCKS" in proxy  # current ownership + legacy cleanup
with tempfile.TemporaryDirectory() as tmp:
    sample = Path(tmp)/"main.rs"
    sample.write_text(main, encoding="utf-8")
    try:
        budget.apply(sample)
    except ValueError:
        assert sample.read_text(encoding="utf-8") == main
    else:
        raise AssertionError("second patch application must fail without changing source")
    sample.write_text(budget.OLD, encoding="utf-8")
    budget.apply(sample)
    assert sample.read_text(encoding="utf-8") == budget.NEW
print("PASS strict startup cleanup 15s budget, unchanged ip/iptables 2s command budget, kill-on-drop, warnings, diagnostics")
