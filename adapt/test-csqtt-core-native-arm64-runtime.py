#!/usr/bin/env python3
"""Disposable native ARM64 Rust runtime smoke without real route/firewall writes.

Runs only on GitHub's ephemeral ARM64 runner as root, with a nonproduction
temporary config and fake ip/iptables/sysctl subprocesses. Never deploy on a VPS
or NanoPi. The fake iptables delays one harmless listing >1s to prove the outer
startup cleanup budget no longer prematurely aborts an otherwise valid scan.
"""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import signal
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True, type=Path)
args = parser.parse_args()
binary = args.binary.resolve(strict=True)
if not (
    os.environ.get("GITHUB_ACTIONS") == "true"
    and os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted"
    and os.environ.get("CSQTT_NATIVE_ARM64_SMOKE") == "disposable-runner-only"
    and platform.machine() in ("aarch64", "arm64")
    and os.geteuid() == 0
):
    raise SystemExit("REFUSED: must be root inside explicitly enabled GitHub-hosted ARM64 runner")

def reserved_port(family, socktype):
    with socket.socket(family, socktype) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]

before_routes = Path("/proc/net/route").read_text()
with tempfile.TemporaryDirectory(prefix="csqtt-native-arm64-smoke-") as temp:
    root = Path(temp)
    mockbin = root / "bin"
    mockbin.mkdir()
    state = root / "slow-once"
    calls = root / "commands.log"
    helper = """#!/bin/sh
set -eu
printf '%s %s\\n' "$(basename "$0")" "$*" >> "$CSQTT_MOCK_COMMANDS"
case "$(basename "$0")" in
  iptables)
    case " $* " in
      *" -D "*) exit 1 ;;
      *" -S INPUT "*)
        if [ ! -f "$CSQTT_MOCK_SLOW_ONCE" ]; then
          : > "$CSQTT_MOCK_SLOW_ONCE"
          sleep 1.7
        fi
        exit 0
        ;;
      *) exit 0 ;;
    esac
    ;;
  ip)
    case " $* " in
      *" rule del "*) exit 1 ;;
      *) exit 0 ;;
    esac
    ;;
  sysctl) exit 0 ;;
esac
exit 1
"""
    for name in ("ip", "iptables", "sysctl"):
        path = mockbin / name
        path.write_text(helper)
        path.chmod(0o755)
    conf = root / "config"
    logs = root / "server.log"
    udp_port = reserved_port(socket.AF_INET, socket.SOCK_DGRAM)
    web_port = reserved_port(socket.AF_INET, socket.SOCK_STREAM)
    assert udp_port != web_port
    env = os.environ.copy()
    env.update({
        "PATH": str(mockbin) + os.pathsep + os.environ["PATH"],
        "CSQTT_CORE_DIAG_POLICY_CLEANUP": "1",
        "CSQTT_MOCK_COMMANDS": str(calls),
        "CSQTT_MOCK_SLOW_ONCE": str(state),
    })
    command = [
        str(binary), "--listen", f"127.0.0.1:{udp_port}", "--web-port", str(web_port),
        "--config-dir", str(conf), "--web-secret-path", "native-arm64-lab",
        "--password", "native-smoke-main-not-used",
        "--web-user", "native-smoke-user", "--web-pass", "native-smoke-password",
        "--secure-cookie",
    ]
    proc = None
    try:
        with logs.open("wb") as output:
            started = time.monotonic()
            proc = subprocess.Popen(command, env=env, stdout=output, stderr=subprocess.STDOUT)
            opener = urllib.request.build_opener(
                urllib.request.ProxyHandler({}),
                urllib.request.HTTPSHandler(context=ssl._create_unverified_context()),
            )
            deadline = time.monotonic() + 30
            healthy = False
            while time.monotonic() < deadline:
                if proc.poll() is not None:
                    raise RuntimeError(f"native Rust binary exited early: {proc.returncode}")
                logtext = logs.read_text(errors="replace")
                if ("stage=verify_done" in logtext and
                        "startup policy cleanup timed out" not in logtext):
                    try:
                        with opener.open(
                            f"https://127.0.0.1:{web_port}/native-arm64-lab/",
                            timeout=2,
                        ) as response:
                            healthy = response.status == 200
                    except (urllib.error.URLError, TimeoutError):
                        pass
                    if healthy:
                        break
                time.sleep(0.25)
            if not healthy:
                raise RuntimeError("diagnostics or HTTPS readiness not reached within 30s")

        content = logs.read_text(errors="replace")
        match = re.search(r"stage=verify_done elapsed_ms=(\d+) result=ok", content)
        assert match, "cleanup stage did not finish successfully"
        elapsed_ms = int(match.group(1))
        assert 1500 <= elapsed_ms < 15000, f"unexpected cleanup diagnostic duration {elapsed_ms}ms"
        assert state.exists(), "slow iptables mock never executed"
        assert "startup policy cleanup timed out" not in content
        requests = calls.read_text().splitlines()
        assert any(line.startswith("iptables ") and " -S INPUT" in line for line in requests)
        assert any(line.startswith("ip ") for line in requests)
        assert before_routes == Path("/proc/net/route").read_text(), "default route changed"
        assert proc.poll() is None, "runtime exited unexpectedly"
        assert conf.exists(), "temporary config directory missing"
        print(f"PASS native ARM64 compiled ELF startup, mocked 1.7s iptables, "
              f"cleanup={elapsed_ms}ms, HTTPS=200, default route unchanged")
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=4)
        if before_routes != Path("/proc/net/route").read_text():
            raise RuntimeError("runner default routing differs after smoke")
print("PASS temporary config and mock network tools discarded")
