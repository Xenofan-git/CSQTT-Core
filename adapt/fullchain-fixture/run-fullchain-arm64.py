#!/usr/bin/env python3
"""Run Go API -> Rust WIRE-3 -> local TUN/SOCKS -> sing-box as one isolated test.

Requires disposable GitHub-hosted ARM64 runner, real TUN, and separately staged
test-only original-source client with synthetic TURN credentials. Never run on
production NanoPi, Keenetic, or a VPS.
"""
from __future__ import annotations

import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

if __name__ != "__main__":
    raise SystemExit(2)
assert os.environ.get("GITHUB_ACTIONS") == "true"
assert os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted"
assert subprocess.check_output(["uname","-m"],text=True).strip()=="aarch64"
assert os.geteuid()==0
assert Path("/dev/net/tun").exists()
assert not Path("/etc/csqtt-core").exists(), "do not run on a production host"
root=Path(tempfile.mkdtemp(prefix="csqtt-core-250-fullchain-"))
suffix=secrets.token_hex(3)
client_ns="csqtcli"+suffix
server_ns="csqtsrv"+suffix
v1="c"+suffix
v2="s"+suffix
processes=[]
before_default=subprocess.check_output(["ip","-4","route","show","default"],text=True)
workspace=Path(os.environ["GITHUB_WORKSPACE"])
client=Path(os.environ["CSQTT_E2E_RUST_CLIENT"])
server=Path(os.environ["CSQTT_E2E_CORE8"])
go=Path(os.environ["CSQTT_E2E_GO"])
singbox=Path(os.environ["CSQTT_E2E_SINGBOX"])
turn_test_bin=Path(os.environ["CSQTT_E2E_TURN_TEST"])
auth_user="test-panel"
auth_pass="synthetic-panel-only"
tunnel_secret="e2e-local-password-20260830"
core_log=root/"core8.log"
go_log=root/"go-supervisor.log"
turn_log=root/"turn-fixture.log"
http_log=root/"remote-http.log"
marker="CSQTT_CORE_WIRE3_REAL_DATAPLANE_"+suffix
(root/"marker.txt").write_text(marker+"\n")
auth="Basic "+base64.b64encode(f"{auth_user}:{auth_pass}".encode()).decode()

def cmd(args, *, check=True, timeout=15, **kw):
    r=subprocess.run([str(x) for x in args],capture_output=True,text=True,timeout=timeout,**kw)
    if check and r.returncode:
        raise RuntimeError(f"COMMAND FAILED {args}: {r.returncode} {r.stderr[-500:]}")
    return r

def ns(nsname, *args, check=True, timeout=15):
    return cmd(["ip","netns","exec",nsname,*args],check=check,timeout=timeout)

def spawn(nsname,args,out,env=None):
    f=out.open("wb")
    p=subprocess.Popen(["ip","netns","exec",nsname,*map(str,args)],stdout=f,stderr=subprocess.STDOUT,env=env,start_new_session=True)
    f.close()
    processes.append(p)
    return p

def wait_until(fn,deadline=20,interval=.18):
    start=time.monotonic()
    last=None
    while time.monotonic()-start<deadline:
        try:
            result=fn()
            if result:return result
        except (OSError,RuntimeError,urllib.error.URLError) as e:last=e
        time.sleep(interval)
    raise AssertionError(f"condition timed out ({deadline}s): {last}")

def curl_api(method,path,payload=None,timeout=3):
    url="http://127.0.0.1:1062"+path
    req=urllib.request.Request(url,method=method)
    req.add_header("Authorization",auth)
    if payload is not None:
        req.data=json.dumps(payload).encode()
        req.add_header("Content-Type","application/json")
        req.add_header("Origin","http://127.0.0.1:1062")
    elif method!="GET":
        req.data=b""
        req.add_header("Origin","http://127.0.0.1:1062")
    # We are in a separate host netns, so call curl via ip netns exec.
    args=["curl","--noproxy","*","-sSf","--connect-timeout","1","--max-time",str(timeout),"-u",f"{auth_user}:{auth_pass}",
          "-H","Origin: http://127.0.0.1:1062"]
    if method!="GET":args+=["-X",method]
    if payload is not None:
        args+=["-H","Content-Type: application/json","--data-binary",json.dumps(payload)]
    args += [url]
    r=ns(client_ns,*args,check=False,timeout=timeout+3)
    if r.returncode:raise RuntimeError(f"API {method} {path} curl exit {r.returncode}: {r.stderr[-240:]}")
    return json.loads(r.stdout)

try:
    assert all(p.is_file() for p in (client,server,go,singbox,turn_test_bin))
    print("ISOLATED_FULLCHAIN_4_BINARIES_PRESENT",flush=True)
    cmd(["ip","netns","add",client_ns])
    cmd(["ip","netns","add",server_ns])
    cmd(["ip","link","add",v1,"type","veth","peer","name",v2])
    cmd(["ip","link","set",v1,"netns",client_ns])
    cmd(["ip","link","set",v2,"netns",server_ns])
    ns(client_ns,"ip","link","set",v1,"name","eth0")
    ns(server_ns,"ip","link","set",v2,"name","eth0")
    for n in (client_ns,server_ns):
        ns(n,"ip","link","set","lo","up")
        ns(n,"ip","link","set","eth0","up")
    ns(client_ns,"ip","addr","add","192.168.4.26/24","dev","eth0")
    ns(server_ns,"ip","addr","add","192.168.4.1/24","dev","eth0")
    ns(client_ns,"ip","link","add","eth1","type","dummy")
    ns(client_ns,"ip","addr","add","192.168.5.26/24","dev","eth1")
    ns(client_ns,"ip","link","set","eth1","up")
    assert not ns(client_ns,"ip","-4","route","show","default").stdout.strip()
    assert not ns(server_ns,"ip","-4","route","show","default").stdout.strip()
    print("FULLCHAIN_ISOLATED_VETH_TWO_NETNS_NO_GLOBAL_DEFAULT_PASS",flush=True)

    serverargs=[server,"--listen","192.168.4.1:46920","--web-port","46921",
                "--config-dir",root/"server-config","--password",tunnel_secret,
                "--web-user","ci","--web-pass","synthetic-web-secret"]
    core=spawn(server_ns,serverargs,core_log)
    wait_until(lambda:core.poll() is None and "csqtt-core0" in ns(server_ns,"ip","-o","addr",check=False).stdout,30)
    print("FULLCHAIN_REAL_CORE8_WIRE3_SERVER_TUN_RUNNING",flush=True)
    ns(server_ns,"ip","-4","addr","show","csqtt-core0")

    port_file=root/"turn-port.txt"
    stop_file=root/"turn-stop"
    tenv=os.environ.copy()
    tenv.update(CSQTT_E2E_PEER="192.168.4.1:46920",
                CSQTT_E2E_RELAY_PORT_FILE=str(port_file),
                CSQTT_E2E_RELAY_STOP_FILE=str(stop_file))
    fixture=spawn(client_ns,[turn_test_bin,"csqtt_core_fullchain_synthetic_turn_fixture","--ignored","--nocapture","--test-threads=1"],turn_log,tenv)
    relay_port=wait_until(lambda:port_file.read_text().strip() if port_file.exists() else None,25)
    assert 1<=int(relay_port)<=65535
    print("FULLCHAIN_ORIGINAL_RUST_TURN_RELAY_FIXTURE_READY",flush=True)

    ns(server_ns,"ip","addr","show","csqtt-core0")
    http=spawn(server_ns,["python3","-m","http.server","47093","--bind","10.67.69.1","--directory",root],http_log)
    wait_until(lambda:http.poll() is None and ":47093" in ns(server_ns,"ss","-ltn",check=False).stdout,20)
    direct=ns(client_ns,"curl","--noproxy","*","-fsS","--max-time","2",
                "http://10.67.69.1:47093/marker.txt",check=False,timeout=4)
    assert direct.returncode!=0, "direct bypass reachable: test network isolation broken"
    print("FULLCHAIN_DIRECT_BYPASS_NEGATIVE_CONTROL_PASS",flush=True)

    home=root/"go-home";home.mkdir()
    config=root/"go-config";config.mkdir()
    env=os.environ.copy()
    env.update(HOME=str(home),XDG_CONFIG_HOME=str(config),TMPDIR=str(root),
               CSQTT_CORE_250_EXPERIMENT="isolated-only",
               CSQTT_TEST_TURN_RELAY_PORT=str(relay_port),
               CSQTT_250_ORIGINAL_CLIENT_BINARY=str(client),
               CSQTT_WEB_USER=auth_user,CSQTT_WEB_PASS=auth_pass,
               CSQTT_LALUNE_WEB_PORT="47092",
               CSQTT_SING_BOX_BINARY=str(singbox),
               CSQTT_SING_BOX_DATA_DIR=str(root/"singbox-state"))
    app=spawn(client_ns,[go],go_log,env)
    wait_until(lambda:curl_api("GET","/version").get("core")=="csqtt-server/2.5.0-candidate",30)
    print("FULLCHAIN_NATIVE_GO_SUPERVISOR_API_LIVE_PASS",flush=True)

    settings=curl_api("GET","/settings")
    settings.update({"obfs":"audio","workers":1,"vkAuthMode":"vkcalls",
                    "authMode":"manual","fingerprint":"chrome",
                    "turnTransport":"udp","captchaMode":"auto"})
    assert curl_api("PUT","/settings",settings).get("ok")
    assert curl_api("PUT","/tunnel/auth",{"password":tunnel_secret}).get("ok")
    profile=f"csqtt://connect?v=2&host=192.168.4.1&peer=46920&password={tunnel_secret}&hashes=112233445566"
    assert curl_api("POST","/configs",{"link":profile,"protocol":"CSQTT"}).get("ok")
    cfg=curl_api("GET","/configs")
    assert cfg and cfg[0]["peer"]=="192.168.4.1:46920"
    ident=int(cfg[0]["id"])
    print("FULLCHAIN_GO_API_CREDENTIALS_AND_TEST_PROFILE_READY",flush=True)

    config_obj={
        "listenAddress":"127.0.0.1:19180",
        "csqttAddress":"127.0.0.1:12500",
        "csqttEnabled":True,
        "rules":[
            {"ID":"system-direct","Outbound":"direct","Enabled":True,
             "Domains":["direct-only.test"]},
            {"ID":"system-csqtt","Outbound":"csqtt","Enabled":True,
             "IPCIDRs":["10.67.69.1/32"]}
        ]}
    save=curl_api("PUT","/singbox/config",config_obj)
    assert save.get("ok"),save
    run=curl_api("POST","/singbox/start")
    assert run.get("ok"),run
    wait_until(lambda:curl_api("GET","/singbox/status").get("running"),12)
    print("FULLCHAIN_GO_API_SINGBOX_C S Q T T_OUTBOUND_CONFIGURED".replace(" ",""),flush=True)

    connect=curl_api("POST","/vpn/connect",{"id":ident})
    assert connect.get("ok"),connect
    print("FULLCHAIN_GO_HTTP_CONNECT_TRIGGERED_REAL_RUST_WIRE3",flush=True)

    # A valid TUNCONF and active Rust worker are required before Go can
    # configure its policy-only TUN and start the stable CSQTT SOCKS listener.
    def socks_ready():
        log=json.dumps(curl_api("GET","/logs"),ensure_ascii=False)
        if "[SOCKS5] Downstream eth1 and loopback CSQTT SOCKS5 listeners started" in log:
            return True
        if "Подключение отменено:" in log or "Таймаут ожидания TUNCONF/traffic" in log:
            raise RuntimeError("Go TUN/SOCKS startup rejected: "+log[-1400:])
        if app.poll() is not None or core.poll() is not None or fixture.poll() is not None:
            raise RuntimeError("a critical real child process exited before SOCKS readiness")
        return False
    wait_until(socks_ready,115,interval=.3)
    print("FULLCHAIN_WIRE3_TUNCONF_ACTIVE_WORKER_GO_TUN_AND_SOCKS_PASS",flush=True)
    routes=ns(client_ns,"ip","route","show","table","202").stdout
    assert "csqtt0" in routes,routes
    rules=ns(client_ns,"ip","rule","show").stdout
    assert "22015" in rules and "22020" in rules
    print("FULLCHAIN_ISOLATED_GO_TUN_POLICY_202_AND_FWMARK_PASS",flush=True)

    # Negative control was unreachable directly. Positive request must
    # complete via sing-box SOCKS inbound -> CSQTT SOCKS outbound -> Go
    # marked TCP -> csqtt0 packet bridge -> Rust original client -> local
    # synthetic TURN -> Core8 WIRE-3 -> server csqtt-core0 HTTP.
    req=ns(client_ns,"curl","--noproxy","","-fsS","--max-time","23",
           "--socks5-hostname","127.0.0.1:19180","http://10.67.69.1:47093/marker.txt",
           check=False,timeout=26)
    print("FULLCHAIN_CURL_EXIT",req.returncode,req.stderr[-350:],flush=True)
    assert req.returncode==0 and req.stdout.strip()==marker,(
        "unified test did not carry traffic over real 2.5 WIRE-3 session; "+req.stderr[-500:])
    assert marker in http_log.read_text(errors="replace") or "GET /marker.txt" in http_log.read_text(errors="replace")
    print("FULLCHAIN_END_TO_END_GO_RUST250_CORE8_TUN_SOCKS_SINGBOX_TCP_PASS",flush=True)

    dis=curl_api("POST","/vpn/disconnect")
    assert dis.get("ok")
    stop=curl_api("POST","/singbox/stop")
    assert stop.get("ok")
    print("FULLCHAIN_STOP_AND_CLEANUP_PASS",flush=True)

finally:
    (root/"turn-stop").touch(exist_ok=True)
    for p in reversed(processes):
        if p.poll() is None:
            try:os.killpg(p.pid,signal.SIGTERM)
            except ProcessLookupError:pass
    for p in reversed(processes):
        try:p.wait(timeout=3)
        except subprocess.TimeoutExpired:
            try:os.killpg(p.pid,signal.SIGKILL)
            except ProcessLookupError:pass
            p.wait(timeout=3)
    for n in (client_ns,server_ns):
        cmd(["ip","netns","delete",n],check=False,timeout=10)
    assert subprocess.check_output(["ip","-4","route","show","default"],text=True)==before_default
    print("FULLCHAIN_HOST_DEFAULT_ROUTE_UNCHANGED_PASS",flush=True)
    for file in (core_log,turn_log,go_log,http_log):
        if file.exists():
            lines=file.read_text(errors="replace").splitlines()
            print("DIAGNOSTIC",file.name,"\n".join(lines[-34:])[:3600],flush=True)
    print("FULLCHAIN_DIAGNOSTICS_DIR",root,flush=True)
