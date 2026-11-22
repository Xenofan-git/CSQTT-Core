#!/usr/bin/env python3
"""Execute only extracted installer guard functions on fake listeners, no host mutation."""
import importlib.util
import os
from pathlib import Path
import subprocess

HERE=Path(__file__).resolve().parent
DEPLOY=HERE.parent/"server-assets"/"deploy-csqtt-core-250.sh"
PATCH=HERE/"patch-csqttr-253-selective-deploy-ports.py"
spec=importlib.util.spec_from_file_location("selective_port_patch",PATCH)
p=importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)

script=DEPLOY.read_text(encoding='utf8')
assert 'readonly CSQTT_IFACE="csqtt-core0"' in script
assert script.index("            assert_deployment_ports_not_foreign") < script.index("            detect_os")
assert script.index("    assert_web_port_is_available") > script.index("    assert_peer_port_is_available")
assert script.index("            csqtt_process_is_owned \"$pid\" ||",script.index("force_release_peer_port() {")) < script.index("            found=1",script.index("force_release_peer_port() {"))
try:
    p.patch(script)
except ValueError:
    pass
else:
    raise AssertionError("reapply must reject already patched installer")
print("DEPLOY_EARLY_PREFLIGHT_AND_REAPPLY_GUARD_PASS")

def function(name):
    start=script.index(name+"() {")
    end=script.index("\n}\n",start)+2
    return script[start:end]

names=("peer_port_listener_pids","web_port_listeners","web_port_listener_pids",
       "assert_deployment_ports_not_foreign","assert_peer_port_is_available",
       "assert_web_port_is_available","force_release_peer_port")
parts="\n\n".join(function(n) for n in names)
# Simulated PID and socket ownership only. No installer entry point sourced.
fixture=r'''
set -euo pipefail
PEER_PORT=47000
WEB_PORT=47002
EXIT_PREFLIGHT_FAILED=20
EXIT_CUTOVER_FAILED=30
LOG_FILE=/dev/null
die(){ echo "REFUSED:$1" >&2; return 41; }
peer_port_listeners(){ printf '%s' "$UDP_FAKE"; }
ss(){
  if [ "$*" = "-H -ltnp sport = :47002" ]; then
    printf '%s' "$TCP_FAKE"
  else
    echo "SS_UNEXPECTED:$*" >&2
    return 99
  fi
}
csqtt_process_is_owned(){
  case ",$OWNED_PIDS," in *,"$1",*) return 0;; esac
  return 1
}
log_warn(){ :; }
log_error(){ :; }
docker_container_for_pid(){ return 1; }
kill(){ echo 'UNSAFE_KILL_CALLED' >&2; return 99; }
docker(){ echo 'UNSAFE_DOCKER_CALLED' >&2; return 99; }
sleep(){ :; }
force_release_stub(){ UDP_FAKE=''; }
'''
# Use original release helper for the cleanup positive case only.
def harness(action, mocked_release=True):
    functions=parts
    if mocked_release:
        # Keep source guard function under test, swap only the release
        # implementation for an inert flag. A separate negative check runs
        # the genuine release function with all process effects stubbed.
        begin=functions.index("force_release_peer_port() {")
        functions=functions[:begin]+"force_release_peer_port(){ echo 'MOCK_RELEASE'; UDP_FAKE=''; }\n"
    return fixture+"\n"+functions+"\n"+action+"\n"

def run(action,udp="",tcp="",owned="",mocked=True):
    env={"PATH":os.environ["PATH"],"UDP_FAKE":udp,"TCP_FAKE":tcp,"OWNED_PIDS":owned}
    return subprocess.run(["bash","-c",harness(action,mocked)],
                          env=env,capture_output=True,text=True,timeout=7)

udp_owned='UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("csqtt",pid=9001,fd=3))'
udp_foreign='UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("other",pid=9002,fd=4))'
tcp_owned='LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("csqtt",pid=9001,fd=3))'
tcp_foreign='LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("other",pid=9002,fd=4))'
cases=[
 ("empty preflight","assert_deployment_ports_not_foreign","", "", "",True),
 ("owned UDP preflight","assert_deployment_ports_not_foreign",udp_owned,"","9001",True),
 ("owned TCP preflight","assert_deployment_ports_not_foreign","",tcp_owned,"9001",True),
 ("both owned preflight","assert_deployment_ports_not_foreign",udp_owned,tcp_owned,"9001",True),
 ("foreign UDP preflight","assert_deployment_ports_not_foreign",udp_foreign,"","9001",False),
 ("unknown UDP owner","assert_deployment_ports_not_foreign","UNCONN 0 0 0.0.0.0:47000 0.0.0.0:*","","",False),
 ("mixed UDP owners","assert_deployment_ports_not_foreign",udp_owned+"\n"+udp_foreign,"","9001",False),
 ("foreign TCP preflight","assert_deployment_ports_not_foreign","",tcp_foreign,"9001",False),
 ("unknown TCP owner","assert_deployment_ports_not_foreign","","LISTEN 0 128 0.0.0.0:47002 0.0.0.0:*","",False),
 ("mixed TCP owners","assert_deployment_ports_not_foreign","",tcp_owned+"\n"+tcp_foreign,"9001",False),
 ("owned UDP cleanup","assert_peer_port_is_available",udp_owned,"","9001",True),
 ("foreign UDP cleanup","assert_peer_port_is_available",udp_foreign,"","9001",False),
 ("unknown UDP cleanup","assert_peer_port_is_available","UNCONN 0 0 0.0.0.0:47000 0.0.0.0:*","","",False),
 ("empty TCP after cleanup","assert_web_port_is_available","","","",True),
 ("occupied TCP after cleanup","assert_web_port_is_available","",tcp_foreign,"",False),
 ("release function rechecks owner","force_release_peer_port",udp_foreign,"","9001",False),
]
for name,action,udp,tcp,owned,expected in cases:
    original_release=(name=="release function rechecks owner")
    result=run(action,udp,tcp,owned,mocked=not original_release)
    actual=result.returncode==0
    if actual!=expected:
        raise AssertionError(f"{name}: expected={expected} returned={result.returncode} stdout={result.stdout} stderr={result.stderr}")
    if "UNSAFE_KILL_CALLED" in result.stderr or "UNSAFE_DOCKER_CALLED" in result.stderr:
        raise AssertionError(f"{name}: process mutation attempted")
    print(("ALLOW" if actual else "REJECT")+": "+name)
print("CSQTTR_253_CORE_DEPLOY_PORT_OWNERSHIP_16_OF_16_PASS")
