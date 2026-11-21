#!/usr/bin/env python3
"""Manual opt-in actual TPROXY route activation/cleanup on private QEMU ARM64 guest.

IMPORTANT: writes iptables and ip -4 policy routing rules solely INSIDE the
pre-existing disposable guest, never a host. Guest QEMU must have restrict=on,
SSH mapped to host loopback only. Tested rules apply to CSQTT's private TUN,
not a global default route. This does not exercise real VK TURN or E2E packets.

The external durable guard owns sysctl snapshots; this inner fixture starts
a real patched Core8 with a fake loopback-only SOCKS5 UDP ASSOCIATE endpoint,
uses Core8's private HTTPS API with ephemeral local credentials, verifies
actual TPROXY mangle/policy rules and child and then deactivates/tears down.
No hard-kill of real processes; normal SIGTERM only.
"""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import socketserver
import ssl
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid

GUARD = Path("/usr/local/libexec/core8-private-durable-sysctl-guard.py")
ORIGINAL = Path("/home/csqttlab/core8-arm64")
ELF = Path("/home/csqttlab/core8-rustpatch-experimental-aarch64")
STATE = Path("/var/lib/csqtt-core-private/sysctl-guard")
OUT = Path("/var/lib/csqtt-core-private/tproxy-acceptance")
SOCKS_PORT = 46781
UDP_PORT = 46881
WEB_PORT = 46882
EXPECTED_ORIGINAL = "a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706"
BASE_URL = f"https://127.0.0.1:{WEB_PORT}"


def check(cond: bool, message: str):
    if not cond:
        raise RuntimeError(message)


def sha(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fp:
        for part in iter(lambda: fp.read(65536), b""):
            h.update(part)
    return h.hexdigest()


def run(*args: str, timeout=8, ok=True) -> subprocess.CompletedProcess:
    result = subprocess.run(args, text=True, capture_output=True, timeout=timeout)
    if ok and result.returncode:
        raise RuntimeError(f"command failed {args[0]} {list(args[1:])}: {result.stderr[:260]}")
    return result


def params() -> tuple[str, ...]:
    ns = {"__name__": "_imported"}
    exec(compile(GUARD.read_text(), str(GUARD), "exec"), ns)
    return tuple(ns["PARAMETERS"])


def snapshot() -> dict:
    return {k: (Path("/proc/sys") / k).read_text().strip() for k in params()}


def no_listener(port: int) -> bool:
    return f":{port} " not in run("ss", "-H", "-lntup").stdout


def read_tproxy_route_table() -> list[str]:
    out=run("ip","-4","route","show","table","30001",ok=False)
    if out.returncode:
        # iproute2 returns nonzero while the table has never been created.
        if "FIB table does not exist" in out.stderr:
            return []
        raise RuntimeError("unexpected iproute2 table lookup error: "+out.stderr[:240])
    return out.stdout.strip().splitlines()


def policy_snapshot() -> dict:
    return {
        "ip_rules": run("ip", "-4", "rule", "show").stdout.strip().splitlines(),
        "tproxy_routes": read_tproxy_route_table(),
        "mangle": run("iptables", "-t", "mangle", "-S", "PREROUTING").stdout.strip().splitlines(),
        "filter": run("iptables", "-t", "filter", "-S", "INPUT").stdout.strip().splitlines(),
    }


def owned_policy(snapshot: dict) -> bool:
    return (
        any("lookup 30001" in x and "fwmark" in x for x in snapshot["ip_rules"])
        or bool(snapshot["tproxy_routes"])
        or any("CSQTT_CORE_TPROXY" in x for x in snapshot["mangle"] + snapshot["filter"])
    )


def preflight(expected: str, inside: bool = False):
    check(bool(re.fullmatch(r"[0-9a-f]{64}", expected)), "invalid expected SHA256")
    check(os.geteuid() == 0, "guest root only")
    check(os.uname().machine == "aarch64" and socket.gethostname() == "csqtt-core8-isolated",
          "wrong guest/arch")
    check(not run("ip", "-4", "route", "show", "default").stdout.strip(),
          "private guest has an IPv4 default route")
    check(sha(ORIGINAL) == EXPECTED_ORIGINAL, "original ELF changed")
    check(ELF.is_file() and sha(ELF) == expected, "experimental ELF hash mismatch")
    check(GUARD.is_file(), "durable guard missing")
    check(Path("/dev/net/tun").exists(), "TUN device not present")
    for command in ("ip", "iptables"):
        check(bool(run("which", command).stdout.strip()), f"{command} unavailable")
    if inside:
        check((STATE / "pending-sysctls-v1.json").is_file(), "outer guard journal not active")
    else:
        check(not (STATE / "pending-sysctls-v1.json").exists(), "pending old recovery")
    check(not owned_policy(policy_snapshot()), "preexisting TPROXY-owned rules/table")
    check(all(no_listener(p) for p in (SOCKS_PORT, UDP_PORT, WEB_PORT)),
          "private guest test port occupied")


class SocksControl(socketserver.BaseRequestHandler):
    def recv_exact(self, size: int) -> bytes:
        data = b""
        while len(data) < size:
            block = self.request.recv(size - len(data))
            if not block:
                raise EOFError()
            data += block
        return data

    def handle(self):
        self.request.settimeout(9)
        try:
            version, count = self.recv_exact(2)
            methods = self.recv_exact(count)
            check(version == 5 and 0 in methods, "unexpected SOCKS methods")
            self.request.sendall(b"\x05\x00")
            version, cmd, rsv, atyp = self.recv_exact(4)
            check((version, rsv, atyp) == (5, 0, 1), "unexpected SOCKS address type")
            self.recv_exact(6)
            check(cmd == 3, "mock supports UDP ASSOCIATE only")
            relay = self.server.relay.getsockname()
            self.request.sendall(b"\x05\x00\x00\x01" + socket.inet_aton("127.0.0.1")
                                 + struct.pack("!H", relay[1]))
            # Hold control connection while the requested association is active.
            self.request.settimeout(1)
            while not self.server.done.is_set():
                try:
                    if not self.request.recv(1):
                        break
                except socket.timeout:
                    continue
        except (EOFError, OSError, RuntimeError):
            return


class SocksServer(socketserver.ThreadingTCPServer):
    daemon_threads = True
    allow_reuse_address = False

    def __init__(self):
        self.relay = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.relay.bind(("127.0.0.1", 0))
        self.done = threading.Event()
        super().__init__(("127.0.0.1", SOCKS_PORT), SocksControl)

    def close_all(self):
        self.done.set()
        self.shutdown()
        self.server_close()
        self.relay.close()


def request(path: str, key: str, data=None):
    headers = {"x-api-key": key}
    body = None
    if data is not None:
        body = json.dumps(data).encode()
        headers["content-type"] = "application/json"
    req = urllib.request.Request(BASE_URL + path, data=body, headers=headers,
                                 method="POST" if data is not None else "GET")
    # A self-signed certificate is generated only inside this restricted guest.
    context = ssl._create_unverified_context()
    try:
        with urllib.request.urlopen(req, context=context, timeout=14) as resp:
            raw = resp.read(16000).decode()
            return resp.status, json.loads(raw)
    except urllib.error.HTTPError as e:
        raw = e.read(3000).decode(errors="replace")
        raise RuntimeError(f"private API error HTTP{e.code} {path}: {raw[:450]}") from e


def post(path: str, key: str) -> tuple[int, dict]:
    return request(path, key, {})


def inner(expected: str) -> int:
    preflight(expected, inside=True)
    before = json.loads((OUT / "sysctl-baseline.json").read_text())
    check(snapshot() == before, "sysctl changed before test start")
    policy_before = json.loads((OUT / "policy-baseline.json").read_text())
    check(policy_snapshot() == policy_before, "guest policy changed before test start")

    socks = SocksServer()
    server_thread = threading.Thread(target=socks.serve_forever, daemon=True)
    server_thread.start()
    web_pass = uuid.uuid4().hex + uuid.uuid4().hex
    env = {**os.environ, "CSQTT_MAIN_PASSWORD":uuid.uuid4().hex + uuid.uuid4().hex,
           "CSQTT_WEB_PASS":web_pass}
    child = None
    active = False
    profile_id = None
    test_error = None
    try:
        with (OUT / "patched-core8.log").open("w") as log:
            child = subprocess.Popen(
                [str(ELF), "--listen", f"127.0.0.1:{UDP_PORT}", "--web-port",
                 str(WEB_PORT), "--config-dir", str(OUT / "config")],
                env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
            )
            ready = False
            for _ in range(32):
                if child.poll() is not None:
                    break
                if not no_listener(UDP_PORT) and not no_listener(WEB_PORT):
                    ready = True
                    break
                time.sleep(1)
            print("TPROXY_GUEST_REAL_CORE8_READY="+str(ready), flush=True)
            check(ready, f"patched Core8 failed to start, exit={child.poll()}")
            live = snapshot()
            print("TPROXY_GUEST_IP_FORWARD_DURING="+live["net/ipv4/ip_forward"], flush=True)
            check(live["net/ipv4/ip_forward"] == "1", "real Core8 kernel prep did not run")
            resp = request("/api/local-proxy", web_pass, {
                "name":"Isolated ARM64 TPROXY test", "port":SOCKS_PORT,
                "username":"", "password":"",
            })
            profile_id = resp[1].get("id")
            check(bool(profile_id), f"missing created profile id: {resp}")
            print("TPROXY_GUEST_SOCKS_PROFILE_CREATED=True", flush=True)
            activated = post(f"/api/local-proxy/activate/{profile_id}", web_pass)
            print("TPROXY_GUEST_ACTIVATE_RESPONSE="+json.dumps(activated[1]), flush=True)
            check(activated[1].get("activated") is True, "route activation response did not confirm")
            active = True  # Any later assertion must still deactivate the live policy.
            current = request("/api/local-proxy", web_pass)[1]
            check(current.get("route_active") is True, "route_active flag false")
            policies = policy_snapshot()
            active_rule = [x for x in policies["ip_rules"] if "lookup 30001" in x and "fwmark" in x]
            tproxy_rules = [x for x in policies["mangle"] if "CSQTT_CORE_TPROXY" in x]
            filter_rules = [x for x in policies["filter"] if "CSQTT_CORE_TPROXY" in x]
            routes = policies["tproxy_routes"]
            print("TPROXY_GUEST_POLICY_RULE_COUNT="+str(len(active_rule)), flush=True)
            print("TPROXY_GUEST_MANGLE_RULE_COUNT="+str(len(tproxy_rules)), flush=True)
            print("TPROXY_GUEST_FILTER_RULE_COUNT="+str(len(filter_rules)), flush=True)
            print("TPROXY_GUEST_ROUTE_TABLE="+repr(routes), flush=True)
            print("TPROXY_GUEST_DEFAULT_ROUTE_ABSENT="+str(not run("ip", "-4", "route", "show", "default").stdout.strip()), flush=True)
            check(len(active_rule)==1,"missing or duplicate TPROXY fwmark rule")
            check(len(tproxy_rules)==2 and any("-p udp" in x for x in tproxy_rules)
                  and any("-p tcp" in x for x in tproxy_rules), "wrong TPROXY mangle rules")
            check(len(filter_rules)==1, "missing/filter duplicate TPROXY INPUT rule")
            check(any("local" in x and "dev lo" in x for x in routes),
                  "local TPROXY route missing")
            check(snapshot() == live, "TPROXY improperly mutated global sysctl settings")
            print("TPROXY_GUEST_KERNEL_PARAMETERS_OWNER_PRESERVED=True",flush=True)
            active = True
            stopped = post("/api/local-proxy/deactivate", web_pass)
            print("TPROXY_GUEST_DEACTIVATE_RESPONSE="+json.dumps(stopped[1]), flush=True)
            check(stopped[1].get("deactivated") is True, "API route deactivation failed")
            for _ in range(15):
                if policy_snapshot() == policy_before:
                    break
                time.sleep(.2)
            after_policy = policy_snapshot()
            print("TPROXY_GUEST_POLICY_RESTORED="+str(after_policy==policy_before), flush=True)
            check(after_policy==policy_before, "TPROXY route/rules not cleaned after deactivation")
            check(not request("/api/local-proxy", web_pass)[1]["route_active"],
                  "route still active after deactivation")
            active = False
            print("TPROXY_GUEST_ACTIVATE_DEACTIVATE_PASS", flush=True)
    except Exception as e:
        test_error = e
        print("TPROXY_GUEST_TEST_ERROR="+str(e), flush=True)
    finally:
        if child is not None:
            if child.poll() is None and active and profile_id:
                try:
                    post("/api/local-proxy/deactivate", web_pass)
                except Exception as e:
                    print("TPROXY_GUEST_DEACTIVATE_CLEANUP_WARNING="+str(e), flush=True)
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=35)
                except subprocess.TimeoutExpired as e:
                    raise RuntimeError("Core8 did not exit after graceful TERM") from e
            print("TPROXY_GUEST_CORE8_EXIT="+str(child.returncode), flush=True)
        socks.close_all()
    after = policy_snapshot()
    print("TPROXY_GUEST_FINAL_POLICY_RESTORED="+str(after==policy_before), flush=True)
    print("TPROXY_GUEST_RUST_SYSCTL_RESTORED="+str(snapshot()==before), flush=True)
    if test_error:
        raise test_error
    check(after==policy_before,"TPROXY policy remnants after clean Core8 stop")
    check(snapshot()==before,"Rust failed to restore sysctl before external guard")
    check(all(no_listener(p) for p in (UDP_PORT, WEB_PORT, SOCKS_PORT)),
          "test sockets left listening")
    return 0


def outer(expected: str) -> int:
    preflight(expected)
    check(not OUT.exists(), "previous TPROXY acceptance dir exists; inspect first")
    OUT.mkdir(mode=0o700, parents=True)
    before=snapshot()
    (OUT/"sysctl-baseline.json").write_text(json.dumps(before,sort_keys=True))
    policy_before=policy_snapshot()
    (OUT/"policy-baseline.json").write_text(json.dumps(policy_before,sort_keys=True))
    guarded=[sys.executable,"-B",str(GUARD),
             "--state-dir",str(STATE),"--sysctl-root","/proc/sys",
             "--boot-id-file","/proc/sys/kernel/random/boot_id","--private-guest",
             "run","--",sys.executable,"-B",str(Path(__file__).resolve()),"inner",expected]
    print("TPROXY_GUEST_OUTER_START",flush=True)
    proc=subprocess.run(guarded,capture_output=True,text=True,timeout=160)
    print("TPROXY_GUEST_OUTER_RETURN_CODE="+str(proc.returncode),flush=True)
    print(proc.stdout,flush=True)
    if proc.stderr:
        print("TPROXY_GUEST_OUTER_STDERR="+proc.stderr[-2700:],flush=True)
    print("TPROXY_GUEST_GUARD_CLEANUP="+str(not (STATE/"pending-sysctls-v1.json").exists()),flush=True)
    after_policy=policy_snapshot()
    print("TPROXY_GUEST_POST_GUARD_POLICY_RESTORED="+str(after_policy==policy_before),flush=True)
    print("TPROXY_GUEST_POST_GUARD_SYSCTL_RESTORED="+str(snapshot()==before),flush=True)
    check(proc.returncode==0,"guest inner TPROXY acceptance or guard failed")
    check(after_policy==policy_before,"TPROXY rules remain after guard")
    check(snapshot()==before,"global parameters remain changed after guard")
    check(not (STATE/"pending-sysctls-v1.json").exists(),"durable pending journal remains")
    return 0


if __name__ == "__main__":
    parser=argparse.ArgumentParser()
    parser.add_argument("mode",choices=("outer","inner"))
    parser.add_argument("expected_sha")
    opts=parser.parse_args()
    try:
        sys.exit(outer(opts.expected_sha) if opts.mode=="outer" else inner(opts.expected_sha))
    except (OSError,RuntimeError,ValueError,KeyError,subprocess.SubprocessError) as exc:
        print("TPROXY_PRIVATE_ARM64_ACCEPTANCE_ERROR="+str(exc),file=sys.stderr)
        sys.exit(75)
