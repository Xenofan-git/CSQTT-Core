#!/usr/bin/env bash
set -euo pipefail
python3 - <<'PY'
from pathlib import Path
for machine,name in [(62,'x86'),(183,'arm64')]:
    data=bytearray(64);data[:4]=b'\x7fELF';data[4]=2;data[5]=1;data[18:20]=machine.to_bytes(2,'little')
    Path('/tmp/csqtt-test-'+name).write_bytes(data)
PY
head -n -1 /tmp/csqtt-core-deploy-lab.sh > /tmp/csqtt-core-deploy-source.sh
for machine in x86 arm64; do
    cp "/tmp/csqtt-test-$machine" /tmp/.csqtt-core-upload-server
    printf 'CSQTT_WEB_USER=test\nCSQTT_WEB_PASS=test\n' > /tmp/.csqtt-core-upload-web.env
    printf '{"main_password":"test"}\n' > /tmp/.csqtt-core-upload-overrides.json
    set +e
    bash -c 'source /tmp/csqtt-core-deploy-source.sh; die() { echo "REJECT:$1"; exit "${2:-1}"; }; prepare_uploaded_release' > "/tmp/csqtt-elf-$machine.log" 2>&1
    rc=$?
    set -e
    if [[ "$machine" == x86 ]]; then
        [[ "$rc" == 20 ]] && grep -q 'ELF architecture mismatch' /tmp/csqtt-elf-x86.log
    else
        [[ "$rc" == 0 ]]
    fi
    echo "$machine ELF preflight rc=$rc PASS"
done
rm -f /tmp/.csqtt-core-upload-server /tmp/.csqtt-core-upload-web.env /tmp/.csqtt-core-upload-overrides.json
