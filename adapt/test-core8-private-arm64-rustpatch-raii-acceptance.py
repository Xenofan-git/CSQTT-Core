#!/usr/bin/env python3
"""Manual isolated AArch64 QEMU test of *patched Rust Core8* RAII cleanup.

This script MUST NOT run on real hosts. Only in the private lab guest
csqtt-core8-isolated, without a guest IPv4 default route. The guest QEMU
restrict=on network setting and SSH-local-only host forwarding are
independent preconditions to verify on the host.

The external durable guard owns a rollback journal for the guest and starts
this Python test process as a child. While Python remains running, it launches
the patched Core8 ELF, shuts it down normally, then BEFORE returning control
to the guard verifies that Rust code ALONE restored the 12 guest sysctls.
The guard runs its own later recovery irrespective of the test result.

Do not use live VK tokens or expose guest ports.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import uuid

EXPECTED_ORIGINAL_CORE8 = "a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706"
GUARD = Path("/usr/local/libexec/core8-private-durable-sysctl-guard.py")
TEST_BINARY = Path("/home/csqttlab/core8-rustpatch-experimental-aarch64")
CORE8_ORIGINAL = Path("/home/csqttlab/core8-arm64")
STATE = Path("/var/lib/csqtt-core-private/sysctl-guard")
OUT = Path("/var/lib/csqtt-core-private/rustpatch-raii-acceptance")
ROOT = Path("/proc/sys")
UDP_PORT = 46877
WEB_PORT = 46878


def fail(message: str) -> None:
    raise RuntimeError(message)


def read_sha(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1048576), b""):
            h.update(block)
    return h.hexdigest()


def preflight(expected: str, *, inside_guard: bool = False) -> None:
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        fail("expected SHA256 must be 64 lowercase hex digits")
    if os.geteuid() != 0:
        fail("guest root needed")
    if os.uname().machine != "aarch64" or socket.gethostname() != "csqtt-core8-isolated":
        fail("wrong arch or guest hostname")
    routes = subprocess.run(["ip", "-4", "route", "show", "default"],
                            capture_output=True, text=True, check=True, timeout=4)
    if routes.stdout.strip():
        fail("private guest unexpectedly has default IPv4 route")
    if read_sha(CORE8_ORIGINAL) != EXPECTED_ORIGINAL_CORE8:
        fail("original pinned Core8 unexpectedly changed")
    if not TEST_BINARY.is_file() or read_sha(TEST_BINARY) != expected:
        fail("experimental patched Core8 ELF SHA256 mismatch")
    if not GUARD.is_file():
        fail("private guest durable guard missing")
    if not Path("/dev/net/tun").exists():
        fail("guest TUN unavailable")
    pending = (STATE / "pending-sysctls-v1.json").exists()
    if inside_guard and not pending:
        fail("missing active outer guard journal")
    if not inside_guard and pending:
        fail("guest has unresolved previous sysctl journal")
    for proto, port in (("-lun", UDP_PORT), ("-ltn", WEB_PORT)):
        result = subprocess.run(["ss", "-H", proto], capture_output=True, text=True, check=True)
        if f":{port} " in result.stdout:
            fail(f"test port already occupied {port}")


def load_keys() -> tuple[str, ...]:
    namespace = {"__name__": "_imported"}
    exec(compile(GUARD.read_text(), str(GUARD), "exec"), namespace)
    return tuple(namespace["PARAMETERS"])


def sysctl_snapshot(keys: tuple[str, ...]) -> dict[str, str]:
    return {k: (ROOT / k).read_text().strip() for k in keys}


def check_port(command: str, token: str) -> bool:
    r = subprocess.run(["ss", "-H", command], capture_output=True, text=True,
                       timeout=4, check=True)
    return token in r.stdout


def inner(expected: str) -> int:
    preflight(expected, inside_guard=True)
    keys = load_keys()
    before = json.loads((OUT / "baseline.json").read_text())
    if sysctl_snapshot(keys) != before:
        fail("baseline changed before experimental binary started")
    os.environ["CSQTT_MAIN_PASSWORD"] = uuid.uuid4().hex + uuid.uuid4().hex
    os.environ["CSQTT_WEB_PASS"] = uuid.uuid4().hex + uuid.uuid4().hex
    config = OUT / "binary-config"
    with (OUT / "rustpatch-binary.log").open("w") as log:
        child = subprocess.Popen(
            [str(TEST_BINARY), "--listen", f"127.0.0.1:{UDP_PORT}",
             "--web-port", str(WEB_PORT), "--config-dir", str(config)],
            stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
        )
        try:
            bound = False
            for _ in range(35):
                if child.poll() is not None:
                    break
                if (check_port("-lun", f"127.0.0.1:{UDP_PORT}") and
                    check_port("-ltn", f":{WEB_PORT}")):
                    bound = True
                    break
                time.sleep(1)
            print("PATCHED_REAL_CORE8_UDP_WEB_BOUND=" + str(bound), flush=True)
            if not bound:
                fail(f"patched Core8 did not bind; process exit={child.poll()}")
            during = sysctl_snapshot(keys)
            changed = [k for k in keys if before[k] != during[k]]
            print("PATCHED_REAL_CORE8_SYSCTL_CHANGED_COUNT="+str(len(changed)), flush=True)
            print("PATCHED_REAL_CORE8_SYSCTL_CHANGED_KEYS="+",".join(changed), flush=True)
            print("PATCHED_REAL_CORE8_IP_FORWARD_DURING="+during["net/ipv4/ip_forward"], flush=True)
            if not changed or during["net/ipv4/ip_forward"] != "1":
                fail("patched Core8 did not write expected live guest sysctls")
        finally:
            if child.poll() is None:
                child.terminate()  # clean SIGTERM, never SIGKILL
                try:
                    child.wait(timeout=35)
                except subprocess.TimeoutExpired:
                    fail("clean TERM timeout; inspect guest and durable guard before any retry")
    print("PATCHED_CORE8_CLEAN_EXIT="+str(child.returncode), flush=True)
    after = sysctl_snapshot(keys)
    mismatch = [k for k in keys if after[k] != before[k]]
    print("RUST_RAII_BEFORE_GUARD_RESTORED_ALL="+str(not mismatch), flush=True)
    print("RUST_RAII_BEFORE_GUARD_MISMATCH_COUNT="+str(len(mismatch)), flush=True)
    if mismatch:
        print("RUST_RAII_MISMATCH_KEYS="+",".join(mismatch), flush=True)
        fail("Rust internal RAII cleanup did not restore baseline before external guard")
    if check_port("-lun", f":{UDP_PORT}") or check_port("-ltn", f":{WEB_PORT}"):
        fail("patched Core8 listener persisted after clean exit")
    return 0


def outer(expected: str) -> int:
    preflight(expected)
    OUT.mkdir(mode=0o700, parents=True, exist_ok=True)
    if OUT.is_symlink() or OUT.stat().st_mode & 0o077:
        fail("output dir not private")
    keys = load_keys()
    before = sysctl_snapshot(keys)
    (OUT / "baseline.json").write_text(json.dumps(before, sort_keys=True))
    guard_cmd = [
        sys.executable, "-B", str(GUARD),
        "--state-dir", str(STATE),
        "--sysctl-root", str(ROOT),
        "--boot-id-file", "/proc/sys/kernel/random/boot_id",
        "--private-guest", "run", "--",
        sys.executable, "-B", str(Path(__file__).resolve()), "inner", expected,
    ]
    print("PATCHED_ARM64_OUTER_GUARD_BEGIN", flush=True)
    result = subprocess.run(guard_cmd, capture_output=True, text=True, timeout=125)
    print("PATCHED_ARM64_GUARD_EXIT="+str(result.returncode), flush=True)
    print("PATCHED_ARM64_GUARD_STDOUT_BEGIN", flush=True)
    print(result.stdout, flush=True)
    print("PATCHED_ARM64_GUARD_STDOUT_END", flush=True)
    if result.returncode != 0:
        print("PATCHED_ARM64_GUARD_STDERR="+result.stderr[-2400:], flush=True)
        fail("patched ELF inner test/guard failed")
    after_guard = sysctl_snapshot(keys)
    print("PATCHED_ARM64_AFTER_GUARD_RESTORED_ALL="+str(after_guard == before), flush=True)
    if after_guard != before:
        fail("guard left guest sysctls changed")
    if (STATE / "pending-sysctls-v1.json").exists():
        fail("durable guard has pending snapshot")
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("outer", "inner"))
    parser.add_argument("expected_sha")
    args = parser.parse_args()
    try:
        sys.exit(outer(args.expected_sha) if args.mode == "outer" else inner(args.expected_sha))
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError) as e:
        print("RUSTPATCH_ARM64_ACCEPTANCE_FAILED:" + str(e), file=sys.stderr)
        sys.exit(75)
