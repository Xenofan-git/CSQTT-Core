#!/usr/bin/env python3
"""Execute isolated Deploy port-conflict guards with fake listeners and no system mutations."""
from pathlib import Path
import os
import subprocess

script = Path("server-assets/deploy-csqtt-lalune.sh").read_text()

def function(name):
    marker = name + "() {"
    start = script.index(marker)
    end = script.index("\n}\n", start) + 2
    return script[start:end]

guards = "\n\n".join(function(n) for n in (
    "web_port_listeners", "web_port_listener_pids", "assert_deployment_ports_not_foreign",
))
# No installer entry point is sourced or run: only these three functions.
harness = r"""
set -euo pipefail
PEER_PORT=47000
WEB_PORT=47002
die() { echo "GUARD_DENIED: $*" >&2; return 41; }
peer_port_listeners() { printf '%s' "${FAKE_UDP_LISTENER:-}"; }
peer_port_listener_pids() { printf '%s\\n' "${FAKE_UDP_PID:-}"; }
csqtt_process_owns_peer_port() { [ "$1" = "${OWNED_PID:-}" ]; }
csqtt_process_is_owned() { [ "$1" = "${OWNED_PID:-}" ]; }
ss() {
  if [ "$*" = "-H -ltnp sport = :47002" ]; then
    printf '%s' "${FAKE_TCP_LISTENER:-}"
  else
    echo "unexpected ss invocation: $*" >&2
    return 99
  fi
}
""" + guards + "\nassert_deployment_ports_not_foreign\n"

def run(**variables):
    env = {"PATH": os.environ["PATH"], **variables}
    return subprocess.run(["bash", "-c", harness], env=env, capture_output=True, text=True, timeout=5)

cases = [
    ("no listeners", {}, True),
    ("foreign UDP listener", {"FAKE_UDP_LISTENER": 'UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("other",pid=9001,fd=3))', "FAKE_UDP_PID": "9001"}, False),
    ("owned UDP listener", {"FAKE_UDP_LISTENER": 'UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("csqtt",pid=9001,fd=3))', "FAKE_UDP_PID": "9001", "OWNED_PID": "9001"}, True),
    ("unknown UDP owner", {"FAKE_UDP_LISTENER": "UNCONN 0 0 0.0.0.0:47000 0.0.0.0:*"}, False),
    ("foreign TCP listener", {"FAKE_TCP_LISTENER": 'LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("other",pid=9002,fd=3))'}, False),
    ("owned TCP listener", {"FAKE_TCP_LISTENER": 'LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("csqtt",pid=9002,fd=3))', "OWNED_PID": "9002"}, True),
    ("unknown TCP owner", {"FAKE_TCP_LISTENER": "LISTEN 0 128 0.0.0.0:47002 0.0.0.0:*"}, False),
]
for name, env, expected in cases:
    result = run(**env)
    if (result.returncode == 0) != expected:
        raise SystemExit(f"{name}: expected allow={expected}, got rc={result.returncode}; stderr={result.stderr}")
print("Isolated Deploy port guard scenarios: PASS (7 cases)")
