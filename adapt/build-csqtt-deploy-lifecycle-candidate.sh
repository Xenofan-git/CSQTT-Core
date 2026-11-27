#!/bin/bash
# Build reviewable lab artifacts only; never install or start them.
set -euo pipefail
[ "$#" -eq 2 ] || { echo "usage: $0 PINNED_ASSETS_DIR NEW_OUTPUT_DIR" >&2; exit 2; }
source_assets="$(realpath "$1")"
output="$2"
[ ! -e "$output" ] || { echo "Output already exists; refusing overwrite" >&2; exit 2; }
repo="$(cd "$(dirname "$0")/.." && pwd)"
umask 077
mkdir -p "$output/server-assets"
output="$(realpath "$output")"
python3 - "$repo" "$source_assets" "$output" <<'PY'
import hashlib, pathlib, sys
repo, source, output = map(pathlib.Path, sys.argv[1:])
sys.path.insert(0, str(repo/"adapt"))
from csqtt_deploy_lifecycle_candidate import lifecycle_bytes, LIFECYCLE_SHA
assets = output/"server-assets"
baseline = (repo/"server-assets/deploy-csqtt-core-250.sh").read_bytes()
candidate = lifecycle_bytes(baseline)
server = (source/"csqtt-core8-linux-arm64").read_bytes()
assert len(server)==9589352 and hashlib.sha256(server).hexdigest()=="8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d"
(assets/"deploy-csqtt-core-250.sh").write_bytes(baseline)
(assets/"deploy-csqtt-core-lifecycle.sh").write_bytes(candidate)
(assets/"csqtt-core8-linux-arm64").write_bytes(server)
print("Installer:", LIFECYCLE_SHA)
PY
cd "$repo/adapt/deploy-manager-core"
GO111MODULE=off GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -buildvcs=false -o "$output/deploy-manager" .
chmod 755 "$output" "$output/server-assets" "$output/deploy-manager"
chmod 600 "$output/server-assets/"*
sha256sum "$output/deploy-manager"
echo "Only laboratory artifacts built. No remote operation or acceptance performed."
