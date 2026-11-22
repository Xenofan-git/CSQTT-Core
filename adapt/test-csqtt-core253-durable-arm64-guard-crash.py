#!/usr/bin/env python3
"""Live Core8 + existing durable sysctl supervisor, disposable ARM64 NET+UTS netns.

FAIL CLOSED unless already inside disposable GitHub AArch64 root-only netns
with zero default routes and an isolated UTS hostname. Never run this on
NanoPi, Moscow/AMS VPS or any machine's original host network namespace.
Tests child SIGKILL, supervised same-boot recovery, and clean restart.
"""
from __future__ import annotations

import argparse
import importlib.util
import os
from pathlib import Path
import signal
import socket
import ssl
import subprocess
import sys
import time

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("csqtt_netns_guard_preflight", HERE / "test-csqtt-core-unified-arm64-netns-lifecycle.py")
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("verified private netns preflight helper is missing")
safety = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(safety)
GUARD = HERE / "core8-private-durable-sysctl-guard.py"
EXPECTED_HOSTNAME = "csqtt-core8-isolated"
UDP_PORT = 47000
HTTPS_PORT = 47002

def kernel_read(path: str) -> str:
    return Path(path).read_text(encoding="ascii").strip()

def verify_no_listener() -> None:
    for _ in range(40):
        if not safety.bound("udp", UDP_PORT) and not safety.bound("tcp", HTTPS_PORT):
            return
        time.sleep(.1)
    safety.fail("Core8 listeners survived process kill or stop")

def wait_for_guard_child(guard: subprocess.Popen, binary: Path, output: Path) -> int:
    children_file = Path(f"/proc/{guard.pid}/task/{guard.pid}/children")
    deadline = time.monotonic() + 55
    last = ""
    while time.monotonic() < deadline:
        if guard.poll() is not None:
            safety.fail("durable recovery supervisor exited too early: " + output.read_text(errors="replace")[-1800:])
        try:
            children = children_file.read_text().split()
        except (FileNotFoundError, ProcessLookupError):
            children = []
        for value in children:
            pid = int(value)
            try:
                exe = Path(f"/proc/{pid}/exe").resolve(strict=True)
            except (FileNotFoundError, ProcessLookupError):
                continue
            if exe != binary.resolve(strict=True):
                continue
            if safety.bound("udp",UDP_PORT) and safety.bound("tcp",HTTPS_PORT):
                return pid
            last = "child present but UDP+HTTPS listeners not ready"
        time.sleep(.2)
    safety.fail("guarded binary never became ready: " + last + "; log " + output.read_text(errors="replace")[-1800:])

def test_tls_ready() -> None:
    with socket.create_connection(("127.0.0.1", HTTPS_PORT), timeout=5) as sock:
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        with context.wrap_socket(sock, server_hostname="localhost") as stream:
            if stream.version() is None:
                safety.fail("no TLS handshake on guarded Core8")

def guard_arguments(binary: Path, work: Path, state: Path) -> list[str]:
    return [
        sys.executable, "-B", str(GUARD),
        "--state-dir",str(state),
        "--sysctl-root","/proc/sys",
        "--boot-id-file","/proc/sys/kernel/random/boot_id",
        "--private-guest","run","--",
        str(binary),"--listen",f"127.0.0.1:{UDP_PORT}",
        "--web-port",str(HTTPS_PORT),
        "--config-dir",str(work/"config"),
        "--dns","77.88.8.8",
    ]

def supervise_one(binary: Path, work: Path, state: Path, credential_env: dict[str,str],
                  phase: str, *, soak_seconds: int = 0) -> None:
    logpath = work / f"guard-{phase}.log"
    parent = None
    with logpath.open("w", encoding="utf8") as stream:
        parent = subprocess.Popen(
            guard_arguments(binary,work,state),
            env=credential_env, stdout=stream, stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        try:
            child = wait_for_guard_child(parent,binary,logpath)
            safety.check_no_default_route()
            test_tls_ready()
            print("CSQTT_CORE253_GUARDED_UDP_HTTPS_TLS_READY_PHASE_"+phase.upper(),flush=True)
            if soak_seconds:
                # Check REAL wall-clock uptime of the SAME Core8 child, not
                # mocked polls or a restarted process. The 140-second bug
                # would cause the parent to terminate the child before this
                # loop completes; any listener outage fails the experiment.
                started=time.monotonic()
                deadline=started+soak_seconds
                checkpoints=0
                while time.monotonic()<deadline:
                    if parent.poll() is not None:
                        safety.fail("durable guard exited during live Core8 uptime soak")
                    if not Path("/proc").joinpath(str(child),"exe").exists():
                        safety.fail("Core8 PID vanished during live uptime soak")
                    if not (safety.bound("udp",UDP_PORT) and safety.bound("tcp",HTTPS_PORT)):
                        safety.fail("Core8 lost UDP or HTTPS listeners during uptime soak")
                    if not (state/"pending-sysctls-v1.json").is_file():
                        safety.fail("durable ownership journal vanished while Core8 runs")
                    safety.check_no_default_route()
                    test_tls_ready()
                    checkpoints+=1
                    print("CSQTT_CORE253_REAL_UPTIME_CHECKPOINT seconds="+
                          str(int(time.monotonic()-started))+
                          " pid="+str(child),flush=True)
                    remaining=deadline-time.monotonic()
                    if remaining>0:
                        time.sleep(min(10.0,remaining))
                elapsed=time.monotonic()-started
                if elapsed<soak_seconds:
                    safety.fail("insufficient real measured uptime")
                if parent.poll() is not None:
                    safety.fail("guard exited exactly at end of uptime soak")
                test_tls_ready()
                print("CSQTT_CORE253_REAL_CORE8_165S_CONTINUOUS_UPTIME_PASS elapsed="+
                      str(round(elapsed,3))+" checkpoints="+str(checkpoints),flush=True)
            if phase=="sigkill":
                # Guard process is NOT killed. Only the child Core8 group.
                os.killpg(child,signal.SIGKILL)
            else:
                os.killpg(child,signal.SIGTERM)
            status=parent.wait(timeout=30)
        finally:
            if parent.poll() is None:
                parent.terminate()
                try: parent.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    parent.kill()
                    parent.wait(timeout=5)
    result=logpath.read_text(encoding="utf8",errors="replace")
    if "CORE8_RECOVERY=" not in result or "CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED" not in result:
        safety.fail(f"guard did not durably restore after {phase}: "+result[-1800:])
    # SIGKILL must not look like a clean exit. The Rust server, however,
    # intentionally handles SIGTERM as an orderly shutdown and may return 0.
    # A successful graceful exit is the expected production behavior.
    if phase == "sigkill" and status == 0:
        safety.fail("SIGKILL was incorrectly reported as a clean zero exit")
    if phase == "term" and status != 0:
        safety.fail("gracefully stopped Core8 returned unexpected code " + str(status))
    if (state/"pending-sysctls-v1.json").exists():
        safety.fail("durable sysctl journal was not cleared after child death")
    verify_no_listener()
    safety.check_no_default_route()
    print("CSQTT_CORE253_GUARD_REAL_CHILD_"+phase.upper()+"_RESTORED_PASS",flush=True)

def main() -> None:
    p=argparse.ArgumentParser()
    p.add_argument("--bundle",required=True,type=Path)
    p.add_argument("--work",required=True,type=Path)
    p.add_argument("--soak-seconds",type=int,default=0)
    args=p.parse_args()
    if args.soak_seconds not in (0,165):
        safety.fail("only 0 or exactly 165 real seconds of guarded soak allowed")
    binary=safety.preflight(args.bundle,args.work)
    if socket.gethostname() != EXPECTED_HOSTNAME:
        safety.fail("not the named disposable ARM64 NET+UTS namespace")
    if os.readlink("/proc/self/ns/uts")==os.environ.get("CSQTT_HOST_UTSNS_ID",""):
        safety.fail("host UTS namespace not isolated")
    if not Path("/dev/net/tun").exists():
        safety.fail("missing TUN device in private runner")
    if not GUARD.is_file() or GUARD.is_symlink():
        safety.fail("durable guard source missing/nonregular")
    import secrets
    env=os.environ.copy()
    env.pop("CSQTT_SERVICE_MANAGER",None)
    env.update({
      "CSQTT_MAIN_PASSWORD":secrets.token_hex(32),
      "CSQTT_WEB_PASS":secrets.token_hex(32),
    })
    state=args.work/"durable-sysctl-state"
    state.mkdir(mode=0o700)
    (args.work/"config").mkdir(mode=0o700)
    before=safety.sysctl_baseline()
    verify_no_listener()
    supervise_one(binary,args.work,state,env,"sigkill",
                  soak_seconds=args.soak_seconds)
    after=safety.sysctl_baseline()
    if before != after:
        safety.fail("actual sysctl changed after guarded real SIGKILL: "+
                    str({k:(before.get(k),after.get(k)) for k in before if after.get(k)!=before.get(k)}))
    print("CSQTT_CORE253_GUARD_REAL_SIGKILL_IPV6_FORWARDING_RESTORED_PASS",flush=True)
    supervise_one(binary,args.work,state,env,"term")
    last=safety.sysctl_baseline()
    if before != last:
        safety.fail("sysctl changed after clean supervised Core8 restart: "+
                    str({k:(before.get(k),last.get(k)) for k in before if last.get(k)!=before.get(k)}))
    print("CSQTT_CORE253_GUARD_REAL_ARM64_SIGKILL_RECOVER_RESTART_ALL_PASS",flush=True)

if __name__ == "__main__":
    main()
