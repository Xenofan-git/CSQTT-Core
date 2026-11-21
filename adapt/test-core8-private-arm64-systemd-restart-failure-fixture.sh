#!/usr/bin/env bash
# MANUAL ONLY: ARM64 QEMU private guest; fake sysctl tree. Never run on host.
# Tests natural nonzero child exit, NOT SIGKILL, VK TURN or real /proc/sys.
set -euo pipefail
umask 077
test "$(id -u)" = 0
test "$(hostname)" = csqtt-core8-isolated
test "$(uname -m)" = aarch64
test -z "$(ip -4 route show default)"
test "$(sha256sum /usr/local/libexec/core8-private-durable-sysctl-guard.py | awk '{print $1}')" = fe1af8822a752bd5e9edeaaac2efe2c7b94fdf544b55696253d94a28631ef449
SERVICE=csqtt-core8-private-restart-fixture-20261122.service
BASE=/tmp/csqtt-core8-restart-fixture-20261122
UNIT=/run/systemd/system/$SERVICE
test ! -e "$UNIT"
test ! -e "$BASE"
mkdir -m 0700 "$BASE"
# Remove only owned temporary fixture and test unit, regardless of test result.
cleanup() {
  systemctl stop "$SERVICE" >/dev/null 2>&1 || :
  systemctl reset-failed "$SERVICE" >/dev/null 2>&1 || :
  rm -f -- "$UNIT"
  systemctl daemon-reload >/dev/null 2>&1 || :
  if test -d "$BASE"; then rm -rf -- "$BASE"; fi
}
trap cleanup EXIT
export BASE
python3 -B - <<'PY'
import os,runpy,uuid,json
from pathlib import Path
base=Path(os.environ["BASE"])
guard=runpy.run_path('/usr/local/libexec/core8-private-durable-sysctl-guard.py')
root=base/'proc'/'sys'
initial={}
replacement={'1':'0','2':'1','0':'1','fq':'fq_codel','bbr':'cubic','15':'3','3':'1'}
for rel,expected in guard['PARAMETERS'].items():
    p=root/rel
    p.parent.mkdir(parents=True,exist_ok=True)
    prev=replacement[expected]
    p.write_text(prev+'\n')
    initial[rel]=prev
(base/'baseline.json').write_text(json.dumps(initial,sort_keys=True))
(base/'boot-id').write_text(str(uuid.uuid4()))
(base/'state').mkdir(mode=0o700)
(base/'child.py').write_text('''from pathlib import Path
import os,runpy
base=Path("/tmp/csqtt-core8-restart-fixture-20261122")
values=runpy.run_path("/usr/local/libexec/core8-private-durable-sysctl-guard.py")["PARAMETERS"]
with (base/"attempts").open("a") as f:
 f.write("ATTEMPT\\n")
for rel,value in values.items():
 (base/"proc"/"sys"/rel).write_text(value+"\\n")
print("FIXTURE_CHILD_EXIT_42",flush=True)
raise SystemExit(42)
''')
os.chmod(base/'child.py',0o700)
PY
cat > "$UNIT" <<EOF
[Unit]
Description=CSQTT Core8 private ARM64 restart failure fixture (no real kernel writes)
ConditionHost=csqtt-core8-isolated
ConditionPathExists=$BASE/child.py
After=local-fs.target
StartLimitIntervalSec=60
StartLimitBurst=3

[Service]
Type=simple
User=root
Group=root
ExecStart=/usr/bin/python3 -B /usr/local/libexec/core8-private-durable-sysctl-guard.py --state-dir $BASE/state --sysctl-root $BASE/proc/sys --boot-id-file $BASE/boot-id run -- /usr/bin/python3 -B $BASE/child.py
ExecStopPost=/usr/bin/python3 -B /usr/local/libexec/core8-private-durable-sysctl-guard.py --state-dir $BASE/state --sysctl-root $BASE/proc/sys --boot-id-file $BASE/boot-id recover
KillMode=control-group
Restart=on-failure
RestartSec=1
UMask=0077
NoNewPrivileges=true
EOF
chmod 0644 "$UNIT"
systemctl daemon-reload
# The previous fixture run may leave the test-only name in failed state.
# Clear ONLY that test service before re-checking the final assertion.
systemctl reset-failed "$SERVICE" >/dev/null 2>&1 || :
systemd-analyze verify "$SERVICE"
echo SYSTEMD_FIXTURE_UNIT_VERIFIED
SINCE="$(date -u '+%Y-%m-%d %H:%M:%S')"
systemctl start "$SERVICE"
echo SYSTEMD_FIXTURE_STARTED
for x in $(seq 1 25);do
  status="$(systemctl show "$SERVICE" -p Result --value)"
  case "$status" in start-limit-hit) break;; esac
  sleep 1
done
echo "FINAL_RESULT=$status"
echo "ATTEMPTS=$(test -f "$BASE/attempts" && wc -l < "$BASE/attempts" || echo 0)"
echo 'SERVICE_READBACK'
systemctl show "$SERVICE" -p ActiveState -p SubState -p Result -p NRestarts -p ExecMainStatus --no-pager
echo 'JOURNAL_MARKERS'
# Save once: grep -q on a live pipe would SIGPIPE journalctl under pipefail
# (exit 141), even when all measured recovery conditions are satisfied.
journalctl -u "$SERVICE" --since "$SINCE" -n 90 --no-pager -o cat > "$BASE/service-journal.txt"
grep -E 'CORE8_|FIXTURE_CHILD|restart counter|Start request|Failed|failed' "$BASE/service-journal.txt" |tail -n 55 ||:
echo 'BASELINE_VERIFY'
python3 -B - <<'PY'
from pathlib import Path
import json,runpy
base=Path('/tmp/csqtt-core8-restart-fixture-20261122')
initial=json.loads((base/'baseline.json').read_text())
values=runpy.run_path('/usr/local/libexec/core8-private-durable-sysctl-guard.py')['PARAMETERS']
after={k:(base/'proc'/'sys'/k).read_text().strip() for k in values}
attempts=(base/'attempts').read_text().splitlines()
print("ATTEMPT_COUNT="+str(len(attempts)))
print("RESTORED_ALL="+str(after==initial))
print("PENDING_JOURNAL_EXISTS="+str((base/'state'/'pending-sysctls-v1.json').exists()))
assert 2 <= len(attempts) <= 3, "expected 2-3 bounded attempts"
assert after==initial, "fake sysctl files not restored"
assert not (base/'state'/'pending-sysctls-v1.json').exists(), "recovery journal remains"
PY
# Some systemd versions keep Result=exit-code after hitting start-limit;
# the journal and bounded attempt count are the authoritative evidence.
case "$status" in start-limit-hit|exit-code) : ;; *) exit 1 ;; esac
grep -Fq 'Start request repeated too quickly.' "$BASE/service-journal.txt"
test "$(grep -c 'CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED' "$BASE/service-journal.txt")" -eq 3
test "$(grep -c 'FIXTURE_CHILD_EXIT_42' "$BASE/service-journal.txt")" -eq 3
test "$(wc -l < "$BASE/attempts")" -eq 3
echo SYSTEMD_FAILURE_RESTART_LIMIT_AND_RESTORE_PASS
