#!/usr/bin/env python3
"""Run experimental Core8 server startup/restart ONLY in an isolated CI netns.

Refuses host network namespace, non-AArch64 systems, any default route or
unexpected interfaces. No installed services, global routes, external
credentials, host systemd or real NanoPi hosts are used.
"""
from __future__ import annotations
import argparse
import json
import hashlib
import os
from pathlib import Path
import secrets
import signal
import socket
import ssl
import subprocess
import sys
import time

SERVER_NAME = "csqtt-core-derived-server-250-arm64"
PROTOCOL = "CSQTT-WIRE-3"
EXPECTED_SOURCE = "4211ac5e3d1356699c84057f608035ae669e7cc7"
TEST_UDP_PORT = 47000
TEST_WEB_PORT = 47002

def run(*args:str, timeout:int=5)->str:
    return subprocess.check_output(args,text=True,stderr=subprocess.STDOUT,timeout=timeout)

def fail(message:str)->None:
    raise RuntimeError("isolated-arm64-Core8: "+message)

def preflight(bundle:Path,work:Path)->Path:
    if (os.geteuid()!=0 or os.uname().machine!="aarch64" or
        os.environ.get("GITHUB_ACTIONS")!="true" or
        os.environ.get("RUNNER_ENVIRONMENT")!="github-hosted" or
        os.environ.get("CSQTT_ISOLATED_NETNS")!="yes"):
        fail("not an authorized root-capable disposable GitHub AArch64 netns experiment")
    host_ns=os.environ.get("CSQTT_HOST_NETNS_ID","")
    current_ns=os.readlink("/proc/self/ns/net")
    if not host_ns.startswith("net:[") or host_ns==current_ns:
        fail("network namespace is not separated from original CI runner")
    if run("ip","-4","route","show","default").strip():
        fail("has a default route; DO NOT start Core8")
    devices=run("ip","-o","link","show").splitlines()
    if len(devices)!=1 or " lo:" not in devices[0]:
        fail("unexpected device in private namespace")
    if not work.is_dir() or work.is_symlink():
        fail("test scratch path not an existing regular directory")
    if not str(work.resolve()).startswith(str(Path(os.environ["RUNNER_TEMP"]).resolve())+"/"):
        fail("test scratch must be under disposable runner temporary directory")
    manifest=json.loads((bundle/"manifest.json").read_text(encoding="utf8"))
    if (manifest.get("status")!="experimental-not-approved-for-production" or
        manifest.get("fullCombinedRuntimeAcceptance") is not False or
        manifest.get("globalDefaultRoute") is not False or
        manifest.get("wireProtocol")!=PROTOCOL or
        manifest.get("selective253Core8Commit")!=EXPECTED_SOURCE):
        fail("unverified experimental bundle metadata")
    p=bundle/"bin"/SERVER_NAME
    if not p.is_file() or p.is_symlink() or not os.access(p,os.X_OK):
        fail("missing regular ARM64 server ELF")
    data=p.read_bytes()
    if hashlib.sha256(data).hexdigest()!=manifest["files"][SERVER_NAME]["sha256"]:
        fail("server digest mismatch")
    if run(str(p),"--protocol-revision").strip()!=PROTOCOL:
        fail("mismatched Core8 wire protocol")
    return p

def bound(proto:str,port:int)->bool:
    flag="-lun" if proto=="udp" else "-ltn"
    output=run("ss","-H",flag)
    return any(":"+str(port) in line for line in output.splitlines())

def sysctl_baseline()->dict[str,str]:
    names=("net.ipv4.ip_forward","net.ipv6.conf.all.forwarding")
    values={}
    for n in names:
        try:
            values[n]=run("sysctl","-n",n).strip()
        except (OSError,subprocess.CalledProcessError):
            pass
    return values

def check_no_default_route()->None:
    if run("ip","-4","route","show","default").strip():
        fail("default route appeared in isolated network namespace")
    if run("ip","-6","route","show","default").strip():
        fail("IPv6 default route appeared in isolated network namespace")

def kill_group(proc:subprocess.Popen)->None:
    if proc.poll() is None:
        try:
            os.killpg(proc.pid,signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            proc.wait(timeout=14)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(proc.pid,signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=10)
    else:
        proc.wait(timeout=3)

def trial(server:Path,config_dir:Path,logs:Path,epoch:int,secrets_env:dict[str,str])->None:
    with logs.open("w",encoding="utf8") as out:
        args=[
            str(server),"--listen","127.0.0.1:"+str(TEST_UDP_PORT),
            "--web-port",str(TEST_WEB_PORT),
            "--config-dir",str(config_dir),
            "--dns","77.88.8.8",
        ]
        env=os.environ.copy()
        env.update(secrets_env)
        env.pop("CSQTT_SERVICE_MANAGER",None)
        p=subprocess.Popen(args,stdout=out,stderr=subprocess.STDOUT,
                           env=env,start_new_session=True)
        try:
            deadline=time.monotonic()+50
            while time.monotonic()<deadline:
                if p.poll() is not None:
                    fail("Core8 exited before listener readiness; log suffix: "+logs.read_text(encoding="utf8")[-1600:])
                if bound("udp",TEST_UDP_PORT) and bound("tcp",TEST_WEB_PORT):
                    break
                time.sleep(.3)
            else:
                fail("Core8 did not bind UDP+HTTPS in 50s; log suffix: "+logs.read_text(encoding="utf8")[-1600:])
            check_no_default_route()
            print("CSQTT_CORE_UNIFIED_ARM64_NETNS_LISTENERS_PASS cycle="+str(epoch),flush=True)
            with socket.create_connection(("127.0.0.1",TEST_WEB_PORT),timeout=5) as s:
                tls=ssl.create_default_context()
                tls.check_hostname=False
                tls.verify_mode=ssl.CERT_NONE
                with tls.wrap_socket(s,server_hostname="localhost") as conn:
                    if not conn.version():
                        fail("no HTTPS TLS handshake")
                    print("CSQTT_CORE_UNIFIED_ARM64_NETNS_TLS_HANDSHAKE_PASS cycle="+str(epoch),flush=True)
        finally:
            kill_group(p)
    for _ in range(25):
        if not bound("udp",TEST_UDP_PORT) and not bound("tcp",TEST_WEB_PORT):
            break
        time.sleep(.15)
    if bound("udp",TEST_UDP_PORT) or bound("tcp",TEST_WEB_PORT):
        fail("Core8 listener persisted after exit")
    check_no_default_route()
    print("CSQTT_CORE_UNIFIED_ARM64_NETNS_CLEAN_STOP_PASS cycle="+str(epoch),flush=True)

def main()->None:
    parser=argparse.ArgumentParser()
    parser.add_argument("--bundle",required=True,type=Path)
    parser.add_argument("--work",required=True,type=Path)
    a=parser.parse_args()
    server=preflight(a.bundle,a.work)
    if not Path("/dev/net/tun").exists():
        fail("TUN device missing; cannot claim live Core8 startup test")
    # CI random secrets are not printed, stored, or transmitted.
    env={"CSQTT_MAIN_PASSWORD":secrets.token_hex(32),
         "CSQTT_WEB_PASS":secrets.token_hex(32)}
    config=a.work/"config"
    config.mkdir(mode=0o700,parents=True,exist_ok=False)
    before=sysctl_baseline()
    trial(server,config,a.work/"server-first.log",1,env)
    trial(server,config,a.work/"server-restart.log",2,env)
    after=sysctl_baseline()
    if after!=before:
        fail("network namespace sysctl baseline not restored: "+str({k:(before.get(k),after.get(k)) for k in before if after.get(k)!=before.get(k)}))
    print("CSQTT_CORE_UNIFIED_ARM64_NETNS_SYSCTL_CLEANUP_PASS",flush=True)
    print("CSQTT_CORE_UNIFIED_ARM64_REAL_SERVER_START_RESTART_PASS",flush=True)

if __name__=="__main__":
    main()
