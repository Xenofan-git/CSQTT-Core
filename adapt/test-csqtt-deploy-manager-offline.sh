#!/usr/bin/env bash
# No remote connections, install/uninstall, real secrets or deployable artifacts.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT
export GO111MODULE=off GOTOOLCHAIN=local GOPROXY=off
if command -v sftp >/dev/null && test -x /usr/lib/openssh/sftp-server; then
    export CSQTT_LOCAL_SFTP_EXPECTED=1
else
    export CSQTT_LOCAL_SFTP_EXPECTED=0
fi
unset CSQTT_DEPLOY_FIXTURE_ASSETS CSQTT_DEPLOY_VM_INVENTORY_FILE CSQTT_DEPLOY_PROCESS_FIXTURE CSQTT_DEPLOY_CANONICAL_FIXTURE
# Catch any unintended SSH invocation outside a test's own synthetic executable.
cat > "$scratch/ssh" <<'EOF'
#!/bin/sh
echo unexpected-ssh-invocation >&2
exit 97
EOF
chmod 700 "$scratch/ssh"
export PATH="$scratch:$PATH"
cd "$root/adapt/deploy-manager-core"
go test -race -count=1 -timeout=90s -json . | tee "$scratch/results.jsonl"
python3 - "$scratch/results.jsonl" <<'PY'
import json
import os
import sys
from pathlib import Path
events = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
expected_skips = {
    "TestDeployManagerVerifiesPinnedScriptAndArm64Server",
    "TestInventoryOnlyNeverStartsDeploy",
    "TestRecordedARM64GuestInventory",
    "Test253PinnedAssetSnapshot",
}
if os.environ["CSQTT_LOCAL_SFTP_EXPECTED"] == "0":
    expected_skips.add("Test253LocalSFTPStaging")
top = {e["Test"]: e["Action"] for e in events
       if e.get("Test") and "/" not in e["Test"]
       and e["Action"] in ("pass", "fail", "skip")}
assert expected_skips == {n for n, result in top.items() if result == "skip"}, top
required = {"Test253LifecycleOrderAndResult", "Test253LifecycleCancellation",
            "TestDecodeDeployRequestStrict", "TestCanonicalInventoryRecord",
            "TestInventoryOnlyRejectsUninstallBeforeAssets",
            "Test253PreservedUpdateConfig", "Test253UpdateEnvironmentSubset",
            "Test253AssetBytesIntegrity", "TestInventoryValidationFailsClosed",
            "Test253StagingCleanupFailure"}
if os.environ["CSQTT_LOCAL_SFTP_EXPECTED"] == "1":
    required.add("Test253LocalSFTPStaging")
assert all(top.get(name) == "pass" for name in required), top
assert not any(result == "fail" for result in top.values()), top
assert not any("unexpected-ssh-invocation" in e.get("Output", "") for e in events)
assert any(e["Action"] == "pass" and "Test" not in e for e in events)
print(f"OFFLINE_MANAGER_PASS: {sum(v == 'pass' for v in top.values())} groups passed, "
      f"{len(expected_skips)} fixture-dependent groups intentionally skipped")
PY
go vet .
# Compile only; do not run the manager or upload binaries.
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$scratch/manager-arm64" .
file "$scratch/manager-arm64" | grep -F 'ARM aarch64'
echo 'OFFLINE_MANAGER_ARM64_BUILD_PASS'
