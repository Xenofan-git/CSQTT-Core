#!/bin/bash
set -euo pipefail
# Explicitly limited to disposable hosted ARM64 GitHub runner.
test "${GITHUB_ACTIONS:-}" = true
test "${RUNNER_ENVIRONMENT:-}" = github-hosted
test "$(uname -m)" = aarch64
test "$(id -u)" = 0
test "$(ps -p 1 -o comm=)" = systemd
test ! -e /usr/local/bin/csqtt-core
test ! -e /etc/systemd/system/csqtt-core.service
test ! -e /etc/csqtt-core
test -c /dev/net/tun
src="${GITHUB_WORKSPACE:?}"
old="$src/old/csqtt-core-linux-arm64"
new="$src/new/csqtt-core-linux-arm64"
(cd "$src/old" && sha256sum -c SHA256SUMS)
(cd "$src/new" && sha256sum -c SHA256SUMS)
sha_old="$(sha256sum "$old" | cut -d' ' -f1)"
sha_new="$(sha256sum "$new" | cut -d' ' -f1)"
test "$sha_old" = 771453b7efc155ffc60cbecdf03581a957e6b292c7d5fb63435ec4a482a570ad
test "$sha_new" = 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb
test "$sha_old" != "$sha_new"
ip route show default > /tmp/ci-default-before
echo RELEASE_PAIR_VERIFIED
mkdir -p /etc/csqtt-core
openssl req -newkey rsa:2048 -x509 -nodes -days 2 -sha256 -subj '/CN=disposable-ci-runner' -keyout /etc/csqtt-core/web_key.pem -out /etc/csqtt-core/web_cert.pem >/dev/null 2>&1
chmod 600 /etc/csqtt-core/web_key.pem
env_flags=(CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only CSQTT_DEPLOY_MODE=systemd CSQTT_PEER_PORT=47000 CSQTT_WEB_PORT=47002 CSQTT_SSH_PORT=22 CSQTT_START_STABILITY_SECONDS=1)
upload() {
  install -m 755 "$1" /tmp/.csqtt-core-upload-server
  printf 'CSQTT_WEB_USER=ci-rollback\nCSQTT_WEB_PASS=ci-private\nCSQTT_MAIN_PASSWORD=ci-main-password\nCSQTT_WEB_SECRET_PATH=%s\n' "$2" > /tmp/.csqtt-core-upload-web.env
  printf '{"main_password":"ci-main-password"}\n' > /tmp/.csqtt-core-upload-overrides.json
  chmod 600 /tmp/.csqtt-core-upload-web.env /tmp/.csqtt-core-upload-overrides.json
}
upload "$old" old-ci-panel
env "${env_flags[@]}" CSQTT_CORE_VERIFIED_BINARY_SHA256="$sha_old" bash "$src/server-assets/deploy-csqtt-core-250.sh" install > /tmp/ci-base.log 2>&1 || { tail -70 /tmp/ci-base.log; exit 1; }
grep -Fq CSQTT_DEPLOY_OK /tmp/ci-base.log
test "$(sha256sum /usr/local/bin/csqtt-core | cut -d' ' -f1)" = "$sha_old"
systemctl is-active --quiet csqtt-core
systemctl is-enabled --quiet csqtt-core
curl -kfsS --noproxy '*' https://127.0.0.1:47002/old-ci-panel/ > /dev/null
sha256sum /etc/systemd/system/csqtt-core.service /etc/csqtt-core/csqtt-core.env > /tmp/ci-baseline-files
echo CI_SAVED_STATE > /etc/csqtt-core/ci-state-marker
echo ACTUAL_ARM64_SYSTEMD_BASELINE_PASS
mkdir -p /tmp/ci-systemctl-fault
cat > /tmp/ci-systemctl-fault/systemctl <<'SH'
#!/bin/sh
if [ "$1" = start ] && [ "${2:-}" = csqtt-core ]; then
  echo CI_INTENTIONAL_CANDIDATE_START_FAILURE >&2
  exit 77
fi
exec /usr/bin/systemctl "$@"
SH
chmod 755 /tmp/ci-systemctl-fault/systemctl
upload "$new" rejected-ci-candidate
set +e
env PATH="/tmp/ci-systemctl-fault:$PATH" "${env_flags[@]}" CSQTT_CORE_VERIFIED_BINARY_SHA256="$sha_new" bash "$src/server-assets/deploy-csqtt-core-250.sh" install > /tmp/ci-candidate.log 2>&1
candidate_rc=$?
set -e
test "$candidate_rc" -ne 0 || { echo CANDIDATE_FAULT_IGNORED; exit 1; }
grep -Fq CI_INTENTIONAL_CANDIDATE_START_FAILURE /tmp/ci-candidate.log || { tail -90 /tmp/ci-candidate.log; exit 1; }
grep -Fq 'Предыдущий systemd runtime восстановлен' /tmp/ci-candidate.log || { tail -95 /tmp/ci-candidate.log; exit 1; }
test "$(sha256sum /usr/local/bin/csqtt-core | cut -d' ' -f1)" = "$sha_old"
sha256sum -c /tmp/ci-baseline-files
test "$(cat /etc/csqtt-core/ci-state-marker)" = CI_SAVED_STATE
systemctl is-active --quiet csqtt-core
systemctl is-enabled --quiet csqtt-core
curl -kfsS --noproxy '*' https://127.0.0.1:47002/old-ci-panel/ > /dev/null
test "$(curl -ks --noproxy '*' -o /dev/null -w '%{http_code}' https://127.0.0.1:47002/rejected-ci-candidate/)" != 200
ip route show default > /tmp/ci-default-after
cmp /tmp/ci-default-before /tmp/ci-default-after
echo ACTUAL_ARM64_CANDIDATE_FAILURE_RESTORED_BINARY_UNIT_CONFIG_STATE_AND_HTTPS_PASS
