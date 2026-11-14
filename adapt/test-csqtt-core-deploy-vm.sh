#!/usr/bin/env bash
# Only executes harmless status/refusal checks on a dedicated ARM64 VM.
set -euo pipefail
: "${CSQTT_LAB_SSH_KEY:?Set dedicated lab SSH key}"
: "${CSQTT_LAB_PORT:?Set dedicated lab forwarded SSH port}"
: "${CSQTT_LAB_HOST:=127.0.0.1}"
: "${CSQTT_LAB_USER:=csqttlab}"
[[ "$CSQTT_LAB_HOST" == 127.0.0.1 ]] || { echo 'Loopback-only lab SSH' >&2; exit 2; }
[[ "$CSQTT_LAB_PORT" == 22239 ]] || { echo 'Unexpected lab port' >&2; exit 2; }
ssh_opts=(-i "$CSQTT_LAB_SSH_KEY" -p "$CSQTT_LAB_PORT" -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10)
ssh "${ssh_opts[@]}" "$CSQTT_LAB_USER@$CSQTT_LAB_HOST" 'test "$(uname -m)" = aarch64 && test "$(hostname)" = csqtt-deploy-lab'
scp -i "$CSQTT_LAB_SSH_KEY" -P "$CSQTT_LAB_PORT" -o StrictHostKeyChecking=accept-new server-assets/deploy-csqtt-core-250.sh "$CSQTT_LAB_USER@$CSQTT_LAB_HOST:/tmp/csqtt-core-deploy-lab.sh"
ssh "${ssh_opts[@]}" "$CSQTT_LAB_USER@$CSQTT_LAB_HOST" 'set -eu; bash -n /tmp/csqtt-core-deploy-lab.sh; for action in install uninstall; do set +e; env -u CSQTT_CORE_ALLOW_MUTATION sudo bash /tmp/csqtt-core-deploy-lab.sh "$action" >/tmp/csqtt-lab-guard.log 2>&1; rc=$?; set -e; test "$rc" = 42 || { cat /tmp/csqtt-lab-guard.log; exit 1; }; done; sudo bash /tmp/csqtt-core-deploy-lab.sh status >/tmp/csqtt-lab-status.log; test ! -e /etc/csqtt-core; test ! -e /usr/local/bin/csqtt-core; echo "ARM64 VM guard/status PASS"'
