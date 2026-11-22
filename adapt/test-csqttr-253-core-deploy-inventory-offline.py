#!/usr/bin/env python3
"""Mocked Bash execution of extracted EXACT inventory functions, no host mutations."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
root=Path(__file__).resolve().parent.parent
s=(root/"server-assets/deploy-csqtt-core-250.sh").read_text()
spec=importlib.util.spec_from_file_location("inventory_patch",root/"adapt/patch-csqttr-253-selective-deploy-inventory.py")
p=importlib.util.module_from_spec(spec);spec.loader.exec_module(p)
try:p.patch(s)
except ValueError:pass
else:raise AssertionError("second patch accepted")

def fn(name):
    start=s.index(name+"() {");end=s.index("\n}\n",start)+2
    return s[start:end]
names=("peer_port_listeners","peer_port_listener_pids","web_port_listeners",
"web_port_listener_pids","inventory_emit","inventory_port","do_inventory")
funcs="\n\n".join(fn(n) for n in names)
fixture=r'''
set -euo pipefail
PEER_PORT=47000; WEB_PORT=47002; CSQTT_IFACE=csqtt-core0
CSQTT_DOCKER_CONTAINER=csqtt-core; CSQTT_CONFIG_DIR="$DB_DIR"
ss(){
 if [ "$SS_FAIL" = "both" ]; then return 79; fi
 if [ "$SS_FAIL" = "tcp" ] && [ "$*" = "-H -ltnp sport = :47002" ]; then return 79; fi
 case "$*" in
 "-H -lunp sport = :47000") printf '%s' "$UDP";;
 "-H -ltnp sport = :47002") printf '%s' "$TCP";;
 *) echo BAD_SS >&2;return 99;;
 esac
}
systemctl(){
 case "$*" in
 "show --value -p LoadState csqtt-core.service") printf '%s\n' "$UNIT_LOAD";;
 "show --value -p ActiveState csqtt-core.service") printf '%s\n' "$UNIT_ACTIVE";;
 *) echo BAD_SYSTEMCTL >&2;return 99;;
 esac
}
csqtt_systemd_unit_is_managed(){ [ "$UNIT_OWN" = 1 ]; }
csqtt_process_is_owned(){ case ",$OWNED," in *,"$1",*) return 0;; esac; return 1; }
ip(){ [ "$*" = "link show csqtt-core0" ] && [ "$TUN" = 1 ]; }
docker(){
 case "$*" in
 "inspect csqtt-core") [ "$DOCKER" = 1 ];;
 "inspect --format {{.State.Running}} csqtt-core") printf '%s\n' "$DOCKER_RUNNING";;
 *) echo BAD_DOCKER >&2;return 99;;
 esac
}
kill(){ echo UNSAFE_KILL >&2;return 101; }
rm(){ echo UNSAFE_DELETE >&2;return 101; }
'''
def run(extra=None,db=False):
    with tempfile.TemporaryDirectory(prefix="csqtt-inventory-mock-") as tmp:
        if db:(Path(tmp)/"csqtt.db").write_bytes(b"fake")
        env={"PATH":os.environ["PATH"],"DB_DIR":tmp,"UDP":"","TCP":"","UNIT_LOAD":"not-found",
        "UNIT_ACTIVE":"inactive","UNIT_OWN":"0","OWNED":"","TUN":"0","DOCKER":"0",
        "DOCKER_RUNNING":"false","SS_FAIL":"",**(extra or {})}
        r=subprocess.run(["bash","-c",fixture+"\n"+funcs+"\ndo_inventory"],env=env,capture_output=True,text=True,timeout=5)
        assert "UNSAFE_" not in r.stderr,r.stderr
        if extra and extra.get("SS_FAIL"):
            assert r.returncode==20,(r.returncode,r.stdout,r.stderr)
            assert "CSQTT_INV|end" not in r.stdout
            return r.stdout
        assert r.returncode==0,(r.returncode,r.stderr)
        assert r.stdout.splitlines()[-1]=="CSQTT_INV|end",r.stdout
        return r.stdout
udp1='UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("csqtt",pid=1001,fd=3))'
udp2='UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("other",pid=1002,fd=4))'
tcp1='LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("csqtt",pid=1001,fd=3))'
tcp2='LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("other",pid=1002,fd=4))'
cases=[
("absent",{},'runtime=systemd|state=absent|owner=none'),
("running managed",{"UNIT_LOAD":"loaded","UNIT_ACTIVE":"active","UNIT_OWN":"1"},'state=running|owner=core_candidate'),
("activating managed",{"UNIT_LOAD":"loaded","UNIT_ACTIVE":"activating","UNIT_OWN":"1"},'state=running|owner=core_candidate'),
("failed managed",{"UNIT_LOAD":"loaded","UNIT_ACTIVE":"failed","UNIT_OWN":"1"},'state=stopped|owner=core_candidate'),
("foreign service",{"UNIT_LOAD":"loaded","UNIT_ACTIVE":"active"},'state=running|owner=foreign'),
("unknown service",{"UNIT_LOAD":"strange"},'state=unknown|owner=unknown'),
("owned UDP",{"UDP":udp1,"OWNED":"1001"},'proto=udp|port=47000|state=busy|owner=core_candidate'),
("foreign UDP",{"UDP":udp2},'proto=udp|port=47000|state=busy|owner=foreign'),
("unknown UDP",{"UDP":"UNCONN 0 0 0.0.0.0:47000 0.0.0.0:*"},'proto=udp|port=47000|state=busy|owner=unknown'),
("mixed UDP",{"UDP":udp1+"\n"+udp2,"OWNED":"1001"},'proto=udp|port=47000|state=busy|owner=mixed'),
("owned TCP",{"TCP":tcp1,"OWNED":"1001"},'proto=tcp|port=47002|state=busy|owner=core_candidate'),
("foreign TCP",{"TCP":tcp2},'proto=tcp|port=47002|state=busy|owner=foreign'),
("unknown TCP",{"TCP":"LISTEN 0 128 0.0.0.0:47002 0.0.0.0:*"},'proto=tcp|port=47002|state=busy|owner=unknown'),
("mixed TCP",{"TCP":tcp1+"\n"+tcp2,"OWNED":"1001"},'proto=tcp|port=47002|state=busy|owner=mixed'),
("tun present",{"TUN":"1"},'name=csqtt-core0|state=present|owner=unverified'),
("docker running",{"DOCKER":"1","DOCKER_RUNNING":"true"},'runtime=docker|state=running|owner=unverified'),
("docker stopped",{"DOCKER":"1","DOCKER_RUNNING":"false"},'runtime=docker|state=stopped|owner=unverified'),
]
for name,settings,part in cases:
    assert part in run(settings),(name,part)
    print("PASS",name)
assert "database|state=present|name=csqtt.db" in run(db=True)
print("PASS database present")
assert 'CSQTT_INV|error|reason=requires_root' in s
assert s.index('if [ "'+ '$' + '{1:-}" = inventory ]; then') < s.index('    mkdir -p "$(dirname "$LOG_FILE")"',s.index("main() {",s.index("handle_unexpected_error()")))
print("PASS early read-only dispatch")
assert "CSQTT_INV|error|reason=udp_socket_query_failed" in run({"SS_FAIL":"both"})
print("PASS UDP socket query fails closed")
assert "CSQTT_INV|error|reason=tcp_socket_query_failed" in run({"SS_FAIL":"tcp"})
print("PASS TCP socket query fails closed")
print("CSQTTR_253_CORE_INVENTORY_21_OF_21_PASS")
