#!/usr/bin/env python3
"""Independent real Core8 SIGKILL/restart test in a route-less disposable ARM64 netns.

This is NOT a watchdog/systemd test: it verifies kernel listener release
and whether a new binary incarnation can recover after abrupt process death.
No public routes, VPN links, third-party services or host sysctls.
"""
from __future__ import annotations
import argparse
import importlib.util
import os
from pathlib import Path
import secrets
import signal
import subprocess
import time

helper=Path(__file__).with_name("test-csqtt-core-unified-arm64-netns-lifecycle.py")
spec=importlib.util.spec_from_file_location("csqtt_netns_preflight",helper)
if spec is None or spec.loader is None:
    raise RuntimeError("cannot load pinned private netns preflight")
safety=importlib.util.module_from_spec(spec)
spec.loader.exec_module(safety)

def start_server(executable:Path,config:Path,env:dict,log:Path) -> tuple[subprocess.Popen,object]:
    fd=log.open("w",encoding="utf8")
    args=[str(executable),"--listen","127.0.0.1:47000","--web-port","47002",
          "--config-dir",str(config),"--dns","77.88.8.8"]
    child=subprocess.Popen(args,stdout=fd,stderr=subprocess.STDOUT,env=env,
                           start_new_session=True)
    return child,fd

def wait_ready(p:subprocess.Popen, log:Path) -> None:
    deadline=time.monotonic()+50
    while time.monotonic()<deadline:
        if p.poll() is not None:
            safety.fail("Core8 exited unexpectedly: "+log.read_text(errors="replace")[-900:])
        if safety.bound("udp",47000) and safety.bound("tcp",47002):
            safety.check_no_default_route()
            return
        time.sleep(.3)
    safety.fail("Core8 failed to bind after restart: "+log.read_text(errors="replace")[-900:])

def stop(proc:subprocess.Popen,mode:str) -> None:
    if proc.poll() is None:
        os.killpg(proc.pid, signal.SIGKILL if mode=="kill" else signal.SIGTERM)
    try:
        proc.wait(timeout=15)
    except subprocess.TimeoutExpired:
        try: os.killpg(proc.pid,signal.SIGKILL)
        except ProcessLookupError: pass
        proc.wait(timeout=8)

def verify_no_listeners() -> None:
    for _ in range(40):
        if not safety.bound("udp",47000) and not safety.bound("tcp",47002):
            return
        time.sleep(.1)
    safety.fail("Core8 old process listener survived SIGKILL; cannot restart")

def main() -> None:
    a=argparse.ArgumentParser()
    a.add_argument("--bundle",type=Path,required=True)
    a.add_argument("--work",type=Path,required=True)
    opts=a.parse_args()
    binary=safety.preflight(opts.bundle,opts.work)
    if not Path("/dev/net/tun").exists():
        safety.fail("no private TUN device; cannot attempt native runtime recovery")
    before=safety.sysctl_baseline()
    path=opts.work/"config"
    path.mkdir(mode=0o700)
    credentials={"CSQTT_MAIN_PASSWORD":secrets.token_hex(32),
                 "CSQTT_WEB_PASS":secrets.token_hex(32)}
    env=os.environ.copy()
    env.pop("CSQTT_SERVICE_MANAGER",None)
    env.update(credentials)

    first_log=opts.work/"unexpected-process-death.log"
    first,fd1=start_server(binary,path,env,first_log)
    try:
        wait_ready(first,first_log)
        print("CSQTT_CORE253_NATIVE_ARM64_PRE_SIGKILL_UDP_HTTPS_READY",flush=True)
        stop(first,"kill")
        fd1.close()
        verify_no_listeners()
        safety.check_no_default_route()
        print("CSQTT_CORE253_NATIVE_ARM64_SIGKILL_LISTENERS_RELEASED_PASS",flush=True)
        after_kill=safety.sysctl_baseline()
        print("CSQTT_CORE253_NATIVE_ARM64_SIGKILL_SYSCTL_CHANGED_COUNT",
              sum(before.get(k)!=v for k,v in after_kill.items()),flush=True)

        second_log=opts.work/"restart-after-kill.log"
        second,fd2=start_server(binary,path,env,second_log)
        try:
            wait_ready(second,second_log)
            print("CSQTT_CORE253_NATIVE_ARM64_RESTART_AFTER_SIGKILL_READY_PASS",flush=True)
        finally:
            stop(second,"term")
            fd2.close()
        verify_no_listeners()
        safety.check_no_default_route()
        after=safety.sysctl_baseline()
        diff={k:(before.get(k),after.get(k)) for k in before if after.get(k)!=before.get(k)}
        if diff:
            safety.fail("sysctl baseline not restored after restart: "+str(diff))
        print("CSQTT_CORE253_NATIVE_ARM64_POST_KILL_RESTART_SYSCTL_PASS",flush=True)
    finally:
        stop(first,"kill")
        fd1.close()
    print("CSQTT_CORE253_NATIVE_ARM64_REAL_SIGKILL_RESTART_IN_NETNS_PASS",flush=True)

if __name__=="__main__":
    main()
