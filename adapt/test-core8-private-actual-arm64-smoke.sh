#!/usr/bin/env bash
# MANUAL OPT-IN TEST ONLY, NOT CI / NOT PRODUCTION / NOT HOST VPS.
# Only inside disposable guest csqtt-core8-isolated with isolated QEMU
# networking. Changes guest sysctls temporarily; wrapper restores them.
# Do not expose UDP to public networks or use live VK credentials.
set -euo pipefail
umask 077
test "$(id -u)" = 0
test "$(hostname)" = csqtt-core8-isolated
test "$(uname -m)" = aarch64
test -z "$(ip -4 route show default)"
test "$(sha256sum /home/csqttlab/core8-arm64 | awk '{print $1}')" = a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706
test "$(/home/csqttlab/core8-arm64 --protocol-revision)" = CSQTT-WIRE-3
test -z "$(ss -H -lun '( sport = :46871 )')"
test -z "$(ss -H -ltn '( sport = :46872 )')"
OUT=/var/lib/csqtt-core-private/acceptance
STATE=/var/lib/csqtt-core-private/sysctl-guard
install -d -m 0700 "$OUT" "$STATE"
python3 -B - <<'PY' > "$OUT/before.json"
import json,runpy
a=runpy.run_path('/usr/local/libexec/core8-private-durable-sysctl-guard.py')
print(json.dumps({k:(__import__('pathlib').Path('/proc/sys')/k).read_text().strip() for k in a['PARAMETERS']},sort_keys=True))
PY
export CSQTT_MAIN_PASSWORD="$(openssl rand -hex 24)"
export CSQTT_WEB_PASS="$(openssl rand -hex 24)"
python3 -B /usr/local/libexec/core8-private-durable-sysctl-guard.py \
 --state-dir "$STATE" --sysctl-root /proc/sys \
 --boot-id-file /proc/sys/kernel/random/boot_id --private-guest run \
 -- /home/csqttlab/core8-arm64 --listen 127.0.0.1:46871 --web-port 46872 \
 --config-dir "$OUT/core8-config" > "$OUT/guard.log" 2>&1 &
GUARD_PID=$!
echo "TEST_GUARD_STARTED"
UDP=0
WEB=0
for i in $(seq 1 40);do
 if ss -H -lun |grep -Fq '127.0.0.1:46871';then UDP=1;fi
 if ss -H -ltn |grep -Fq ':46872';then WEB=1;fi
 if [ "$UDP" = 1 ] && [ "$WEB" = 1 ];then break;fi
 if ! kill -0 "$GUARD_PID" 2>/dev/null;then break;fi
 sleep 2
done
echo "TEST_UDP_LOOPBACK_BIND=$UDP"
echo "TEST_WEB_PORT_BIND=$WEB"
python3 -B - <<'PY' > "$OUT/during.json"
import json,runpy
a=runpy.run_path('/usr/local/libexec/core8-private-durable-sysctl-guard.py')
print(json.dumps({k:(__import__('pathlib').Path('/proc/sys')/k).read_text().strip() for k in a['PARAMETERS']},sort_keys=True))
PY
GUARD_RC=0
kill -TERM "$GUARD_PID" 2>/dev/null || true
wait "$GUARD_PID" || GUARD_RC=$?
echo "TEST_GUARD_EXIT=$GUARD_RC"
python3 -B /usr/local/libexec/core8-private-durable-sysctl-guard.py \
 --state-dir "$STATE" --sysctl-root /proc/sys \
 --boot-id-file /proc/sys/kernel/random/boot_id --private-guest recover \
 >> "$OUT/guard.log" 2>&1
python3 -B - <<'PY' > "$OUT/after.json"
import json,runpy
a=runpy.run_path('/usr/local/libexec/core8-private-durable-sysctl-guard.py')
print(json.dumps({k:(__import__('pathlib').Path('/proc/sys')/k).read_text().strip() for k in a['PARAMETERS']},sort_keys=True))
PY
python3 -B - <<'PY'
import json
from pathlib import Path
p=Path('/var/lib/csqtt-core-private/acceptance')
b=json.loads((p/'before.json').read_text())
d=json.loads((p/'during.json').read_text())
a=json.loads((p/'after.json').read_text())
changed={k:(b[k],d[k]) for k in b if b[k]!=d[k]}
print('TEST_CHANGED_COUNT='+str(len(changed)))
print('TEST_CHANGED_NAMES='+','.join(sorted(changed)))
print('TEST_RESTORED_ALL='+str(a==b))
if a!=b: print('TEST_NONRESTORED_NAMES='+','.join(k for k in b if a[k]!=b[k]))
assert a==b,'sysctl baseline was not restored'
PY
test -z "$(ss -H -lun '( sport = :46871 )')"
test -z "$(ip -4 route show default)"
echo "TEST_SYSTEMD_PRIVATE_SMOKE_COMPLETE"
