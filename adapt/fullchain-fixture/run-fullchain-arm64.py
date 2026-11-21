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
    lookup=ns(client_ns,"ip","route","get","10.67.69.1","mark","0x4d53",check=False,timeout=6)
    print("FULLCHAIN_SO_MARK_ROUTE_LOOKUP",lookup.returncode,lookup.stdout.strip(),lookup.stderr.strip(),flush=True)
    assert lookup.returncode==0 and "dev csqtt0" in lookup.stdout, "marked SOCKS5 traffic must route into TUN"
    print("FULLCHAIN_MARKED_ROUTE_TARGETS_REAL_GO_TUN_PASS",flush=True)
    for label,argv in (
        ("RULES",["ip","-4","rule","show"]),
        ("ROUTE_TABLE202",["ip","-4","route","show","table","202"]),
        ("ROUTE_MAIN",["ip","-4","route","show","table","main"]),
        ("RP_FILTER",["sysctl","net.ipv4.conf.all.rp_filter","net.ipv4.conf.csqtt0.rp_filter"]),
        ("NFT_RULESET",["nft","list","ruleset"]),
        ("MARKED_ROUTE_SOURCE",["ip","route","get","10.67.69.1","from","10.67.69.2","mark","0x4d53"])
    ):
        result=ns(client_ns,*argv,check=False,timeout=6)
        print("FULLCHAIN_LINUX_ENV_"+label,result.returncode,result.stdout.strip()[-2200:],result.stderr.strip()[-250:],flush=True)

    # Independent native Linux kernel control *inside the exact same client
    # netns after the Go/Rust app has started*. It uses a separate temporary
    # TUN/table/mark, never redirects any CSQTT SOCKS traffic.
    kernel_control=r"""
import os,fcntl,struct,socket,select,subprocess,time
def ip(*args):
    p=subprocess.run(["ip",*args],capture_output=True,text=True,timeout=4)
    if p.returncode:raise RuntimeError("ip failed: "+p.stderr[-200:])
fd=None
try:
    ip("tuntap","add","dev","csqprobe0","mode","tun")
    ip("addr","add","10.89.90.2/32","dev","csqprobe0")
    ip("link","set","csqprobe0","up")
    ip("route","add","default","dev","csqprobe0","table","203")
    ip("rule","add","pref","22014","fwmark","0x4d54/0xffff","lookup","203")
    fd=os.open("/dev/net/tun",os.O_RDWR|os.O_NONBLOCK)
    fcntl.ioctl(fd,0x400454ca,struct.pack("16sH",b"csqprobe0",0x1001))
    sock=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET,socket.SO_MARK,0x4d54)
    sock.setblocking(False)
    print("KERNEL_SOCK_MARKED_CONNECT_ERRNO",sock.connect_ex(("10.89.90.1",47093)))
    end=time.monotonic()+2
    packets=[]
    while time.monotonic()<end:
        r,_,_=select.select([fd],[],[],0.25)
        if r:
            try:packet=os.read(fd,65535)
            except BlockingIOError:continue
            packets.append((len(packet),packet[0]>>4,
                            ".".join(str(c) for c in packet[16:20]) if len(packet)>=20 else "?"))
    print("ISOLATED_KERNEL_CONTROL_TUN_PACKETS",packets[:10])
    assert any(ver==4 and dst=="10.89.90.1" for _,ver,dst in packets), "kernel did not deliver marked IPv4 to independent probe TUN"
    print("ISOLATED_KERNEL_CONTROL_TCP_IPV4_SYN_AFTER_GO_PASS")
finally:
    if fd is not None:os.close(fd)
    for args in (("rule","del","pref","22014","fwmark","0x4d54/0xffff","lookup","203"),
                 ("route","flush","table","203"),
                 ("tuntap","del","dev","csqprobe0","mode","tun")):
        subprocess.run(["ip",*args],capture_output=True,timeout=4)
"""
    after_app_probe=ns(client_ns,"python3","-c",kernel_control,check=False,timeout=8)
    print("FULLCHAIN_INDEPENDENT_TUN_AFTER_GO_KERNEL_CONTROL",
          after_app_probe.returncode,after_app_probe.stdout[-1700:],after_app_probe.stderr[-450:],flush=True)
    assert after_app_probe.returncode==0, "disposable ARM64 kernel TUN failed inside Go runtime namespace"
    # Strict A/B: replace only mark selector, retain Go's exact csqtt0+table202
    # interface, existing Go SOCKS rule 22015 and original application runtime.
    crossmark=r"""
import os,socket,subprocess,fcntl,struct,select,time
def ip(*args,check=True):
    r=subprocess.run(["ip",*args],capture_output=True,text=True,timeout=4)
    if check and r.returncode:raise RuntimeError("ip error: "+r.stderr)
    return r
try:
    ip("rule","add","pref","22013","fwmark","0x4d54/0xffff","lookup","202")
    a=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
    a.setsockopt(socket.SOL_SOCKET,socket.SO_MARK,0x4d54)
    a.setblocking(False)
    print("CROSSMARK_ROUTE",ip("route","get","10.67.69.1","mark","0x4d54").stdout.strip())
    print("CROSSMARK_CONNECT_ERRNO",a.connect_ex(("10.67.69.1",47093)))
    b=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
    b.setsockopt(socket.SOL_SOCKET,socket.SO_BINDTODEVICE,b"csqtt0\x00")
    b.setblocking(False)
    print("BINDTODEVICE_CONNECT_ERRNO",b.connect_ex(("10.67.69.1",47093)))
    time.sleep(1.2)
    print("CSQTT0_AFTER_CROSSMARK_BIND_STATS",ip("-s","link","show","dev","csqtt0").stdout[-440:])
    print("CSQTT0_QDISC",subprocess.run(["tc","-s","qdisc","show","dev","csqtt0"],capture_output=True,text=True,timeout=4).stdout[-540:])
finally:
    ip("rule","del","pref","22013","fwmark","0x4d54/0xffff","lookup","202",check=False)
"""
    cross=ns(client_ns,"python3","-c",crossmark,check=False,timeout=7)
    print("FULLCHAIN_CSQTT0_CROSSMARK_AND_BINDTODEVICE_DIAGNOSTIC",
          cross.returncode,cross.stdout[-2100:],cross.stderr[-400:],flush=True)
    for label,cmdline in [
        ("TUN_FLAGS",["ip","-d","link","show","dev","csqtt0"]),
        ("TUN_ATTACHED",["ip","tuntap","show"]),
        ("IPTABLES",["iptables-save"]),
        ("TC_FILTER",["tc","-s","filter","show","dev","csqtt0","egress"]),
        ("CSQTT_ROUTE_CACHE",["ip","-4","route","get","10.67.69.1","mark","0x4d53","ipproto","tcp","dport","47093"])
    ]:
        res=ns(client_ns,*cmdline,check=False,timeout=5)
        print("FULLCHAIN_TUN_INTERFACE_"+label,res.returncode,res.stdout.strip()[-1400:],res.stderr.strip()[-220:],flush=True)

    # Actual Go process FD ownership and kernel TUN attachment state:
    # this only inspects /proc on a disposable runner, no intervention.
    try:
        tun_fds=[]
        for f in Path(f"/proc/{app.pid}/fd").iterdir():
            try:
                target=os.readlink(f)
                if "tun" in target or "tuntap" in target:
                    info=(Path(f"/proc/{app.pid}/fdinfo")/f.name).read_text()
                    tun_fds.append((f.name,target,info[:750]))
            except (OSError,PermissionError):
                continue
        print("FULLCHAIN_GO_PROCESS_TUN_FDINFO",tun_fds,flush=True)
    except Exception as exc:
        print("FULLCHAIN_GO_PROCESS_TUN_FDINFO_UNAVAILABLE",type(exc).__name__,flush=True)
    print("FULLCHAIN_GO_INTERFACE_QUEUE_INITIAL",
          ns(client_ns,"tc","-s","qdisc","show","dev","csqtt0",check=False).stdout[-700:],flush=True)

    client_tun_before=ns(client_ns,"ip","-s","link","show","dev","csqtt0",timeout=8).stdout
    server_tun_before=ns(server_ns,"ip","-s","link","show","dev","csqtt-core0",timeout=8).stdout
    print("FULLCHAIN_BEFORE_TUN_COUNTERS_CLIENT",client_tun_before.strip()[-600:],flush=True)
    print("FULLCHAIN_BEFORE_TUN_COUNTERS_SERVER",server_tun_before.strip()[-600:],flush=True)

    # ICMP over the marked route probes WIRE-3 independently of TCP.
    icmp=ns(client_ns,"ping","-n","-m","19795","-c","1","-W","2","10.67.69.1",
            check=False,timeout=5)
    print("FULLCHAIN_REAL_MARKED_ICMP_PROBE",icmp.returncode,icmp.stdout.strip()[-470:],icmp.stderr.strip()[-220:],flush=True)
    raw_probe = r"""
import socket
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET,socket.SO_MARK,0x4d53)
s.settimeout(4)
try:
    s.connect(("10.67.69.1",47093))
    s.sendall(b"GET /marker.txt HTTP/1.0\r\nHost: 10.67.69.1\r\nConnection: close\r\n\r\n")
    data=s.recv(300)
    print("REAL_WIRE3_MARKED_TCP_RESPONSE",data[:200].decode(errors="replace"),flush=True)
except Exception as error:
    print("REAL_WIRE3_MARKED_TCP_ERROR",str(error),flush=True)
finally: s.close()
"""
    raw_tcp=ns(client_ns,"python3","-c",raw_probe,check=False,timeout=7)
    print("FULLCHAIN_MARKED_RAW_TCP_CONTROL",raw_tcp.returncode,raw_tcp.stdout[-850:],raw_tcp.stderr[-280:],flush=True)
    print("FULLCHAIN_TUN_COUNTERS_AFTER_RAW_PROBE_CLIENT",
          ns(client_ns,"ip","-s","link","show","dev","csqtt0",check=False).stdout.strip()[-410:],flush=True)
    print("FULLCHAIN_TUN_COUNTERS_AFTER_RAW_PROBE_SERVER",
          ns(server_ns,"ip","-s","link","show","dev","csqtt-core0",check=False).stdout.strip()[-410:],flush=True)
    # One-variable A/B on disposable csqtt0: switch qdisc from fq_codel
    # to noqueue, leave WIRE-3/Go bridge/route/SO_MARK/client unchanged.
    print("FULLCHAIN_QUEUE_AB_PRE",
          ns(client_ns,"tc","-s","qdisc","show","dev","csqtt0",check=False).stdout[-750:],flush=True)
    zero_queue=ns(client_ns,"ip","link","set","dev","csqtt0","txqueuelen","0",check=False)
    print("FULLCHAIN_QUEUE_AB_TXQLEN_ZERO",zero_queue.returncode,zero_queue.stderr[-450:],flush=True)
    print("FULLCHAIN_QUEUE_AB_POST",
          ns(client_ns,"tc","-s","qdisc","show","dev","csqtt0",check=False).stdout[-750:],flush=True)
    qprobe=ns(client_ns,"python3","-c",raw_probe,check=False,timeout=7)
    print("FULLCHAIN_QUEUE_AB_MARKED_TCP_PROBE",qprobe.returncode,qprobe.stdout[-800:],qprobe.stderr[-280:],flush=True)
    print("FULLCHAIN_QUEUE_AB_TUN_COUNTERS",
          ns(client_ns,"ip","-s","link","show","dev","csqtt0",check=False).stdout.strip()[-490:],flush=True)
    print("FULLCHAIN_QUEUE_AB_GOBRIDGE_READS",json.dumps(curl_api("GET","/logs"),ensure_ascii=False)[-2200:],flush=True)


    # Negative control was unreachable directly. Positive request must
    # complete via sing-box SOCKS inbound -> CSQTT SOCKS outbound -> Go
    # marked TCP -> csqtt0 packet bridge -> Rust original client -> local
    # synthetic TURN -> Core8 WIRE-3 -> server csqtt-core0 HTTP.
    curl_cmd=["ip","netns","exec",client_ns,"curl","--noproxy","","-fsS","--max-time","23",
              "--socks5-hostname","127.0.0.1:19180","http://10.67.69.1:47093/marker.txt"]
    curl_child=subprocess.Popen(curl_cmd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        time.sleep(1.5)
        sock_diag=ns(client_ns,"ss","-tanop",check=False,timeout=5)
        tcp_lines=[x for x in sock_diag.stdout.splitlines()
                   if "10.67.69.1:47093" in x or "State" in x or "SYN-SENT" in x]
        print("FULLCHAIN_KERNEL_TCP_SOCKET_STATE_DURING_SOCKS",
              "\n".join(tcp_lines)[-3600:],sock_diag.stderr[-240:],flush=True)
        curl_stdout,curl_stderr=curl_child.communicate(timeout=26)
    except Exception:
        if curl_child.poll() is None:
            curl_child.terminate()
            try:curl_child.wait(timeout=2)
            except subprocess.TimeoutExpired:curl_child.kill();curl_child.wait()
        raise
    req=type("RequestResult",(),{"returncode":curl_child.returncode,"stdout":curl_stdout,"stderr":curl_stderr})()
    print("FULLCHAIN_CURL_EXIT",req.returncode,req.stderr[-1500:],flush=True)
    print("FULLCHAIN_AFTER_TUN_COUNTERS_CLIENT",
          ns(client_ns,"ip","-s","link","show","dev","csqtt0",check=False).stdout.strip()[-700:],flush=True)
    print("FULLCHAIN_AFTER_TUN_COUNTERS_SERVER",
          ns(server_ns,"ip","-s","link","show","dev","csqtt-core0",check=False).stdout.strip()[-700:],flush=True)
    current_go_logs=json.dumps(curl_api("GET","/logs"),ensure_ascii=False)
    print("FULLCHAIN_GO_SOCKS5_TRACE",current_go_logs[-1800:],flush=True)
    singbox_log=root/"singbox-state"/"sing-box.log"
    if singbox_log.exists():
        print("FULLCHAIN_SINGBOX_LOG",singbox_log.read_text(errors="replace")[-1400:],flush=True)
    # TUNSETIFF attach is read-only in the normal EBUSY case. Only perform
    # this final diagnostic AFTER the SOCKS request, not before. A successful
    # unexpected second attachment will be closed immediately.
    attachment_check=r"""
import os,fcntl,struct
path="/dev/net/tun"
fd=os.open(path,os.O_RDWR|os.O_NONBLOCK)
try:
    ifreq=struct.pack("16sH",b"csqtt0",0x1001)
    try:
        ret=fcntl.ioctl(fd,0x400454ca,ifreq)
        print("EXTRA_TUN_ATTACHMENT_WAS_ACCEPTED",ret[:16],flush=True)
    except OSError as exc:
        print("EXTRA_TUN_ATTACHMENT_REJECTED",exc.errno,exc.strerror,flush=True)
finally:
    os.close(fd)
"""
    attached=ns(client_ns,"python3","-c",attachment_check,check=False,timeout=5)
    print("FULLCHAIN_POST_REQUEST_TUN_ATTACHMENT_CHECK",attached.returncode,attached.stdout[-700:],attached.stderr[-350:],flush=True)
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
