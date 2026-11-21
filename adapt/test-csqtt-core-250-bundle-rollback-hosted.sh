#!/usr/bin/env bash
set -euo pipefail
# All actions below occur ONLY on disposable GitHub-hosted native ARM64.
test "${GITHUB_ACTIONS:-}" = true
test "${RUNNER_ENVIRONMENT:-}" = github-hosted
test "$(uname -m)" = aarch64
test "$(id -u)" = 0
test "$(ps -p 1 -o comm=)" = systemd
test "$(id -u runner)" -ge 1000

unit=csqtt-core-250-unified-rollback-ci.service
unit_path=/etc/systemd/system/$unit
test ! -e "$unit_path"
test ! -L "$unit_path"
host_route_before="$(ip -4 route show default)"
stage="$(mktemp -d /tmp/csqtt-250-unified-rollback.XXXXXXXX)"
test -n "$stage" && test -d "$stage"
cleanup() {
  systemctl disable --now "$unit" >/dev/null 2>&1 || :
  rm -f "$unit_path"
  systemctl daemon-reload >/dev/null 2>&1 || :
  rm -rf -- "$stage"
}
trap cleanup EXIT

mkdir -p "$stage/releases/old" "$stage/releases/new" "$stage/state" "$stage/config"
chmod 0755 "$stage"
chown runner:runner "$stage/state" "$stage/config"
cp -a "$GITHUB_WORKSPACE/candidate/." "$stage/releases/new/"
cp -a "$GITHUB_WORKSPACE/candidate/." "$stage/releases/old/"
old_elf="$GITHUB_WORKSPACE/old-core7/csqtt-core-linux-arm64"
(cd "$GITHUB_WORKSPACE/old-core7" && sha256sum -c SHA256SUMS)
echo "3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb  $old_elf" | sha256sum -c -
echo 'a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706  '"$stage"'/releases/new/bin/csqtt-core-derived-server-250-arm64' | sha256sum -c -
install -m 0755 "$old_elf" "$stage/releases/old/bin/csqtt-core-derived-server-250-arm64"

python3 - "$stage" <<'PY'
from pathlib import Path
import hashlib,json,sys
stage=Path(sys.argv[1])
old=stage/'releases/old'
new=stage/'releases/new'
m=json.loads((new/'manifest.json').read_text())
assert m['wireProtocol']=='CSQTT-WIRE-3'
assert m['status']=='experimental-not-approved-for-production'
assert len(m['files'])==4
expected={'original-client':'3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f44760fa',
'derived-server':'a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706'}
for dirname in ('old','new'):
 root=stage/'releases'/dirname
 for name,meta in m['files'].items():
  p=root/'bin'/name
  digest=hashlib.sha256(p.read_bytes()).hexdigest()
  if dirname=='old' and meta['role']=='derived-server':continue
  assert digest==meta['sha256'],f'{dirname}/{name} digest={digest} expected={meta["sha256"]}'
  if meta['role'] in expected:assert digest==expected[meta['role']],f'{dirname}/{name} role={meta["role"]} digest={digest} expected={expected[meta["role"]]}'
  p.chmod(0o755)
old_manifest=json.loads((old/'manifest.json').read_text())
old_manifest['derivedCoreCommit']='21c59ba2918fd04e4324a897d7afc2e53a2e0cae'
old_manifest['files']['csqtt-core-derived-server-250-arm64']['sha256']='3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb'
old_manifest['status']='experimental-baseline-for-rollback-only'
(old/'manifest.json').write_text(json.dumps(old_manifest,indent=2)+'\n')
print('FOUR_COMPONENT_BASELINE_AND_CANDIDATE_VERIFIED_DISTINCT_CORE_ELFS')
PY

ln -s "$stage/releases/old" "$stage/current"
cat > "$unit_path" <<EOF
[Unit]
Description=Ephemeral CSQTT Core 2.5 unified bundle rollback acceptance
[Service]
Type=simple
User=runner
Group=runner
Environment=HOME=$stage/state
Environment=XDG_CONFIG_HOME=$stage/config
Environment=TMPDIR=$stage/state
Environment=CSQTT_CORE_250_EXPERIMENT=isolated-only
Environment=CSQTT_250_ORIGINAL_CLIENT_BINARY=$stage/current/bin/csqtt-core-original-client-250-arm64
Environment=CSQTT_WEB_USER=ci
Environment=CSQTT_WEB_PASS=synthetic-web-ci
Environment=CSQTT_LALUNE_WEB_PORT=47092
ExecStartPre=/usr/bin/test ! -e $stage/intentional-fault
ExecStart=$stage/current/bin/csqtt-core-go-supervisor-arm64
Restart=no
NoNewPrivileges=yes
PrivateNetwork=yes
PrivateDevices=yes
ProtectSystem=strict
ReadWritePaths=$stage /tmp
CapabilityBoundingSet=
RestrictSUIDSGID=yes
[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "$unit_path"
systemctl daemon-reload
sha256sum "$unit_path" > "$stage/unit.sha256"
echo CI_PERSISTENT_MARKER > "$stage/state/owned-ci-data"
chown runner:runner "$stage/state/owned-ci-data"

native_http() {
  local pid
  pid="$(systemctl show -p MainPID --value "$unit")"
  test "$pid" -gt 0
  # Go is in its OWN systemd network namespace, not the host network.
  test -z "$(nsenter --target "$pid" --net ip -4 route show default)"
  local result
  result="$(nsenter --target "$pid" --net curl -fsS --connect-timeout 2 --max-time 4 -u ci:synthetic-web-ci http://127.0.0.1:1062/version)"
  python3 -c 'import json,sys; assert json.loads(sys.argv[1])["core"]=="csqtt-server/2.5.0-candidate"' "$result"
}
ready() {
  for _ in $(seq 1 55); do
    if systemctl is-active --quiet "$unit" && native_http >/dev/null 2>&1; then return 0; fi
    sleep .2
  done
  systemctl status "$unit" --no-pager | tail -35 || :
  journalctl -u "$unit" --no-pager -n 35 || :
  return 1
}

systemctl enable --now "$unit"
ready
systemctl is-enabled --quiet "$unit"
test "$(sha256sum "$stage/current/bin/csqtt-core-derived-server-250-arm64" | cut -d' ' -f1)" = 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb
echo REAL_FOUR_COMPONENT_BASELINE_SYSTEMD_AND_PRIVATE_GO_HTTP_PASS

systemctl stop "$unit"
ln -s "$stage/releases/new" "$stage/current.candidate"
mv -Tf "$stage/current.candidate" "$stage/current"
touch "$stage/intentional-fault"
set +e
systemctl start "$unit" >/dev/null 2>&1
activate_rc=$?
set -e
test "$activate_rc" -ne 0 || { echo REFUSED_TO_FAKE_ROLLBACK; exit 1; }
test "$(sha256sum "$stage/current/bin/csqtt-core-derived-server-250-arm64" | cut -d' ' -f1)" = a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706
echo EXPECTED_CANDIDATE_ACTIVATION_FAILURE

rm -f "$stage/intentional-fault"
ln -s "$stage/releases/old" "$stage/current.rollback"
mv -Tf "$stage/current.rollback" "$stage/current"
systemctl reset-failed "$unit"
systemctl start "$unit"
ready
systemctl is-active --quiet "$unit"
systemctl is-enabled --quiet "$unit"
sha256sum -c "$stage/unit.sha256"
test "$(cat "$stage/state/owned-ci-data")" = CI_PERSISTENT_MARKER
test "$(sha256sum "$stage/current/bin/csqtt-core-derived-server-250-arm64" | cut -d' ' -f1)" = 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb
test "$(ip -4 route show default)" = "$host_route_before"
echo REAL_NATIVE_ARM64_FOUR_COMPONENT_BUNDLE_ATOMIC_ROLLBACK_AND_GO_HTTP_PASS
