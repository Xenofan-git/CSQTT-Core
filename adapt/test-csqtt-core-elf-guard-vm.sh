#!/usr/bin/env bash
set -euo pipefail
: "${CSQTT_LAB_SSH_KEY:?}" "${CSQTT_LAB_PORT:?}"
[[ "$CSQTT_LAB_PORT" == 22239 ]] || exit 2
scp -i "$CSQTT_LAB_SSH_KEY" -P "$CSQTT_LAB_PORT" -o StrictHostKeyChecking=accept-new server-assets/deploy-csqtt-core-250.sh adapt/csqtt-core-elf-preflight-guest.sh csqttlab@127.0.0.1:/tmp/
ssh -i "$CSQTT_LAB_SSH_KEY" -p "$CSQTT_LAB_PORT" -o BatchMode=yes csqttlab@127.0.0.1 'cp /tmp/deploy-csqtt-core-250.sh /tmp/csqtt-core-deploy-lab.sh; sudo rm -f /tmp/csqtt-test-x86 /tmp/csqtt-test-arm64 /tmp/csqtt-elf-x86.log /tmp/csqtt-elf-arm64.log; sudo bash /tmp/csqtt-core-elf-preflight-guest.sh'
