#!/usr/bin/env bash
set -Eeuxo pipefail

# Disposable, rootless-compatible harness: rewrite installer-owned absolute
# paths into a temp root and shadow system commands with deterministic stubs.
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/package" "$ROOT/bin"
cp server-assets/install-csqtt-lalune.sh "$ROOT/package/install.sh"
cp server-assets/uninstall-csqtt-lalune.sh "$ROOT/package/uninstall.sh"
printf 'test-binary\n' > "$ROOT/package/csqtt-lalune-linux-arm64"
chmod +x "$ROOT/package/install.sh" "$ROOT/package/uninstall.sh"

# Relocate only the installer's owned paths. Never execute these scripts on host paths.
for file in "$ROOT/package/install.sh" "$ROOT/package/uninstall.sh"; do
  sed -i \
    -e "s|/usr/local/bin/csqtt-lalune|$ROOT/host/usr/local/bin/csqtt-lalune|g" \
    -e "s|/usr/local/lib/csqtt-lalune|$ROOT/host/usr/local/lib/csqtt-lalune|g" \
    -e "s|/etc/csqtt-lalune|$ROOT/host/etc/csqtt-lalune|g" \
    -e "s|/var/lib/csqtt-lalune|$ROOT/host/var/lib/csqtt-lalune|g" \
    -e "s|/var/log/csqtt-lalune|$ROOT/host/var/log/csqtt-lalune|g" \
    -e "s|/etc/systemd/system/csqtt-lalune.service|$ROOT/host/etc/systemd/system/csqtt-lalune.service|g" \
    "$file"
done

# The disposable harness runs as the hosted runner user; bypass only the root guard
# in relocated test copies. Production installer/uninstaller remain root-only.
sed -i '/run as root/d' "$ROOT/package/install.sh" "$ROOT/package/uninstall.sh"

cat > "$ROOT/bin/uname" <<'SH'
#!/bin/sh
case "$1" in -s) echo Linux;; -m) echo aarch64;; *) exec /usr/bin/uname "$@";; esac
SH
cat > "$ROOT/bin/systemctl" <<'SH'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$TEST_SYSTEMCTL_LOG"
case "$1" in
  is-active) [[ -f "$TEST_ACTIVE" ]];;
  start)
    if [[ "${SYSTEMCTL_START_FAIL:-0}" == 1 ]]; then exit 1; fi
    if [[ "${SYSTEMCTL_START_INACTIVE:-0}" == 1 ]]; then exit 0; fi
    touch "$TEST_ACTIVE"
    ;;
  stop|disable) rm -f "$TEST_ACTIVE";;
  disable) rm -f "$TEST_ACTIVE";;
  *) exit 0;;
esac
SH
cat > "$ROOT/bin/ss" <<'SH'
#!/usr/bin/env bash
case "$*" in
  *"-lun"*"47000"*) [[ "${SS_MODE:-clear}" == occupied-udp ]] && echo 'UNCONN 0 0 0.0.0.0:47000 0.0.0.0:*';;
  *"-ltn"*"47002"*) [[ "${SS_MODE:-clear}" == occupied-tcp ]] && echo 'LISTEN 0 128 0.0.0.0:47002 0.0.0.0:*';;
esac
exit 0
SH
cat > "$ROOT/bin/ip" <<'SH'
#!/usr/bin/env bash
set -eu
case "$*" in
  "link show dev csqtt-lalune0") [[ "${IP_TUN_PRESENT:-0}" == 1 ]] && exit 0 || exit 1;;
  "-4 rule show") printf '%s\n' "${IP_RULES:-}";;
  "-4 route show table 47001"|"-4 route show table 47066") [[ "${IP_ROUTE_PRESENT:-0}" == 1 ]] && echo 'default dev fake0';;
  *) exit 0;;
esac
SH
cat > "$ROOT/bin/iptables-save" <<'SH'
#!/bin/sh
printf '%s\n' "${IPTABLES_RULES:-}"
SH
chmod +x "$ROOT/bin/"*
export PATH="$ROOT/bin:$PATH"
export TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" TEST_ACTIVE="$ROOT/active"

# Simulate standard host parent directories that exist on real Linux systems.
mkdir -p "$ROOT/host/usr/local/bin" "$ROOT/host/usr/local/lib" "$ROOT/host/etc/systemd/system" "$ROOT/host/var/lib" "$ROOT/host/var/log"

# Case 1: occupied TCP web port is refused before any filesystem changes.
if SS_MODE=occupied-tcp "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted occupied TCP port" >&2; exit 1
fi
grep -q 'TCP panel port 47002 is occupied' "$ROOT/out"
[[ ! -e "$ROOT/host/etc/csqtt-lalune" && ! -e "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]

# Case 1b: an existing TUN interface is never adopted or reconfigured.
if IP_TUN_PRESENT=1 "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted existing LaLune TUN" >&2; exit 1
fi
grep -q 'csqtt-lalune0 already exists' "$ROOT/out"
[[ ! -e "$ROOT/host/etc/csqtt-lalune" ]]

# Case 1c: non-empty dedicated route table is never flushed or adopted.
if IP_ROUTE_PRESENT=1 "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted non-empty LaLune route table" >&2; exit 1
fi
grep -q 'route table 47001/47066 is non-empty' "$ROOT/out"
[[ ! -e "$ROOT/host/etc/csqtt-lalune" ]]

# Case 1d: existing namespaced firewall markers are not adopted or deleted.
if IPTABLES_RULES='-A OUTPUT -m comment --comment CSQTT_LALUNE_OLD -j ACCEPT' "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted existing LaLune firewall markers" >&2; exit 1
fi
grep -q 'LaLune firewall markers already exist' "$ROOT/out"
[[ ! -e "$ROOT/host/etc/csqtt-lalune" ]]

# Case 1e: an unmarked config directory is treated as operator-owned, never adopted.
mkdir -p "$ROOT/host/etc/csqtt-lalune"
printf 'production-secret-placeholder\n' > "$ROOT/host/etc/csqtt-lalune/operator.conf"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer adopted an unmarked config directory" >&2; exit 1
fi
grep -Eq 'exists without (our |a regular )ownership marker' "$ROOT/out"
grep -Fxq 'production-secret-placeholder' "$ROOT/host/etc/csqtt-lalune/operator.conf"
[[ ! -e "$ROOT/host/usr/local/bin/csqtt-lalune" ]]
rm -rf "$ROOT/host/etc/csqtt-lalune"

# Case 1f: symlinked owned paths are never followed, even if an external target exists.
mkdir -p "$ROOT/external-config" "$ROOT/external-lib" "$ROOT/external-state" "$ROOT/external-log"
printf 'external-data-must-survive\n' > "$ROOT/external-config/operator.conf"
ln -s "$ROOT/external-config" "$ROOT/host/etc/csqtt-lalune"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer followed a symlinked config path" >&2; exit 1
fi
grep -q 'is a symlink' "$ROOT/out"
grep -Fxq 'external-data-must-survive' "$ROOT/external-config/operator.conf"
rm "$ROOT/host/etc/csqtt-lalune"
ln -s "$ROOT/external-lib" "$ROOT/host/usr/local/lib/csqtt-lalune"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer followed a symlinked library path" >&2; exit 1
fi
grep -q 'is a symlink' "$ROOT/out"
[[ -z "$(find "$ROOT/external-lib" -mindepth 1 -print -quit)" ]]
rm "$ROOT/host/usr/local/lib/csqtt-lalune"
ln -s "$ROOT/external-state" "$ROOT/host/var/lib/csqtt-lalune"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer followed a symlinked state path" >&2; exit 1
fi
grep -q 'is a symlink' "$ROOT/out"
[[ -z "$(find "$ROOT/external-state" -mindepth 1 -print -quit)" ]]
rm "$ROOT/host/var/lib/csqtt-lalune"
ln -s "$ROOT/external-log" "$ROOT/host/var/log/csqtt-lalune"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer followed a symlinked log path" >&2; exit 1
fi
grep -q 'is a symlink' "$ROOT/out"
[[ -z "$(find "$ROOT/external-log" -mindepth 1 -print -quit)" ]]
rm "$ROOT/host/var/log/csqtt-lalune"

# Case 1g: an ownership-marker symlink cannot authorize writes into an external file.
mkdir -p "$ROOT/host/etc/csqtt-lalune" "$ROOT/external-marker-target"
printf 'CSQTT-LALUNE-MANAGED-V1\n' > "$ROOT/external-marker-target/marker"
ln -s "$ROOT/external-marker-target/marker" "$ROOT/host/etc/csqtt-lalune/.managed-by-lalune"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer trusted a symlinked ownership marker" >&2; exit 1
fi
grep -q 'regular ownership marker' "$ROOT/out"
grep -Fxq 'CSQTT-LALUNE-MANAGED-V1' "$ROOT/external-marker-target/marker"
rm -rf "$ROOT/host/etc/csqtt-lalune"

# Case 2: pre-existing LaLune policy IDs are not adopted or modified.
if IP_RULES='47001: from all fwmark 0x6741/0x6741 lookup 47001' "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted pre-existing policy IDs" >&2; exit 1
fi
grep -q 'LaLune policy identifiers already exist' "$ROOT/out"
[[ ! -e "$ROOT/host/etc/csqtt-lalune" ]]

# Case 2b: deployment credentials are mandatory and failure must precede filesystem changes.
if SS_MODE=clear IP_RULES= IP_ROUTE_PRESENT=0 IP_TUN_PRESENT=0 IPTABLES_RULES= "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted a missing secrets file" >&2; exit 1
fi
grep -q 'regular --secrets-file is required' "$ROOT/out"
[[ ! -e "$ROOT/host/etc/csqtt-lalune" && ! -e "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]

# Prepare valid credentials before both negative systemd-start tests and the success case.
printf '%s\n' 'mainPass123' 'webUser123' 'webPass123' '01234567-89ab-cdef-0123-456789abcdef' > "$ROOT/package/server-secrets.txt"
chmod 0600 "$ROOT/package/server-secrets.txt"

# Case 2c: systemd start failure must never be reported as a successful install.
# The installer deliberately retains its namespaced files and unit for diagnosis.
if SYSTEMCTL_START_FAIL=1 SS_MODE=clear IP_RULES= IP_ROUTE_PRESENT=0 IP_TUN_PRESENT=0 IPTABLES_RULES= \
  "$ROOT/package/install.sh" --secrets-file "$ROOT/package/server-secrets.txt" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer reported success after systemd start failure" >&2; exit 1
fi
grep -q 'service failed to start; LaLune files were retained for diagnosis' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" ]]
[[ -f "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]
[[ -f "$ROOT/host/etc/csqtt-lalune/server.env" ]]
[[ "$(stat -c %a "$ROOT/host/etc/csqtt-lalune/server.env")" == 600 ]]
[[ ! -e "$ROOT/host/etc/systemd/system/csqtt-lalune.service.new" ]]
rm -rf "$ROOT/host/etc/csqtt-lalune" "$ROOT/host/usr/local/lib/csqtt-lalune" \
  "$ROOT/host/var/lib/csqtt-lalune" "$ROOT/host/var/log/csqtt-lalune" \
  "$ROOT/host/usr/local/bin/csqtt-lalune" "$ROOT/host/etc/systemd/system/csqtt-lalune.service"

# Case 2d: systemd may return success while the service is not active; this is also failure.
if SYSTEMCTL_START_INACTIVE=1 SS_MODE=clear IP_RULES= IP_ROUTE_PRESENT=0 IP_TUN_PRESENT=0 IPTABLES_RULES= \
  "$ROOT/package/install.sh" --secrets-file "$ROOT/package/server-secrets.txt" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted a service that remained inactive" >&2; exit 1
fi
grep -q 'service is not active' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" ]]
[[ -f "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]
[[ ! -e "$ROOT/host/etc/systemd/system/csqtt-lalune.service.new" ]]
rm -rf "$ROOT/host/etc/csqtt-lalune" "$ROOT/host/usr/local/lib/csqtt-lalune" \
  "$ROOT/host/var/lib/csqtt-lalune" "$ROOT/host/var/log/csqtt-lalune" \
  "$ROOT/host/usr/local/bin/csqtt-lalune" "$ROOT/host/etc/systemd/system/csqtt-lalune.service"


SS_MODE=clear IP_RULES= IP_ROUTE_PRESENT=0 IP_TUN_PRESENT=0 IPTABLES_RULES= "$ROOT/package/install.sh" --secrets-file "$ROOT/package/server-secrets.txt" >"$ROOT/out"
grep -q 'CSQTT_LALUNE_INSTALL_OK' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" ]]
grep -Fq 'Description=CSQTT LaLune isolated dataplane' "$ROOT/host/etc/systemd/system/csqtt-lalune.service"
grep -Fq "EnvironmentFile=$ROOT/host/etc/csqtt-lalune/server.env" "$ROOT/host/etc/systemd/system/csqtt-lalune.service"
grep -Fq 'CSQTT_MAIN_PASSWORD=mainPass123' "$ROOT/host/etc/csqtt-lalune/server.env"
grep -Fq 'CSQTT_WEB_USER=webUser123' "$ROOT/host/etc/csqtt-lalune/server.env"
grep -Fq 'CSQTT_WEB_PASS=webPass123' "$ROOT/host/etc/csqtt-lalune/server.env"
grep -Fq 'CSQTT_SERVICE_MANAGER=systemd' "$ROOT/host/etc/csqtt-lalune/server.env"
grep -Fq 'CSQTT_SYSTEMD_UNIT=csqtt-lalune.service' "$ROOT/host/etc/csqtt-lalune/server.env"
# Peer UDP/47000 is compiled into the patched server binary; this legacy standalone
# installer does not own or override the peer listener. Deploy uses deploy-csqtt-lalune.sh.
grep -Fq 'CSQTT_LALUNE_WEB_PORT=47002' "$ROOT/host/etc/csqtt-lalune/server.env"
grep -Fq 'CSQTT_DEVICE_ID=01234567-89ab-cdef-0123-456789abcdef' "$ROOT/host/etc/csqtt-lalune/server.env"
[[ "$(stat -c %a "$ROOT/host/etc/csqtt-lalune/server.env")" == 600 ]]
grep -Fq 'CSQTT-LALUNE-MANAGED-V1' "$ROOT/host/etc/csqtt-lalune/.managed-by-lalune"

# An active LaLune unit is not silently stopped for an upgrade/reinstall.
cp "$ROOT/host/usr/local/bin/csqtt-lalune" "$ROOT/binary-before-active-check"
touch "$TEST_ACTIVE"
if "$ROOT/package/install.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: installer accepted an active LaLune service" >&2; exit 1
fi
grep -q 'is already active; stop it deliberately' "$ROOT/out"
cmp "$ROOT/binary-before-active-check" "$ROOT/host/usr/local/bin/csqtt-lalune"
rm -f "$TEST_ACTIVE"

# Preserve operator config and service files when routing cleanup is incomplete.
mkdir -p "$ROOT/host/etc/csqtt-lalune"
printf 'keep-me\n' > "$ROOT/host/etc/csqtt-lalune/operator.conf"
if IP_RULES='47066: from all fwmark 0x6742 lookup 47066' "$ROOT/package/uninstall.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: uninstaller removed service with residual policy rule" >&2; exit 1
fi
grep -q 'LaLune policy rules remain after stop' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" ]]
[[ -f "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]
[[ -f "$ROOT/host/etc/csqtt-lalune/operator.conf" ]]

# Residual TUN, route-table entries, and firewall markers each block removal.
if IP_TUN_PRESENT=1 "$ROOT/package/uninstall.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: uninstaller removed files while LaLune TUN remained" >&2; exit 1
fi
grep -q 'csqtt-lalune0 remains after stop' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" && -f "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]

if IP_RULES= IP_ROUTE_PRESENT=1 "$ROOT/package/uninstall.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: uninstaller removed files while LaLune route table remained" >&2; exit 1
fi
grep -q 'route table entries remain after stop' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" && -f "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]

if IP_RULES= IP_ROUTE_PRESENT=0 IPTABLES_RULES='-A OUTPUT -m comment --comment CSQTT_LALUNE_OLD -j ACCEPT' "$ROOT/package/uninstall.sh" >"$ROOT/out" 2>&1; then
  echo "FAIL: uninstaller removed files while LaLune firewall markers remained" >&2; exit 1
fi
grep -q 'firewall rules remain after stop' "$ROOT/out"
[[ -x "$ROOT/host/usr/local/bin/csqtt-lalune" && -f "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]

# Clean uninstall removes only LaLune executable/unit/library; preserves config/state/log by default.
IP_RULES= IP_ROUTE_PRESENT=0 IP_TUN_PRESENT=0 IPTABLES_RULES= "$ROOT/package/uninstall.sh" >"$ROOT/out"
grep -q 'CSQTT_LALUNE_UNINSTALL_OK' "$ROOT/out"
[[ ! -e "$ROOT/host/usr/local/bin/csqtt-lalune" ]]
[[ ! -e "$ROOT/host/etc/systemd/system/csqtt-lalune.service" ]]
[[ -f "$ROOT/host/etc/csqtt-lalune/operator.conf" ]]
[[ -f "$ROOT/host/var/lib/csqtt-lalune/.managed-by-lalune" ]]
[[ -f "$ROOT/host/var/log/csqtt-lalune/.managed-by-lalune" ]]

# All service-control calls must be limited to the namespaced unit; never touch production CSQTT.
if grep -E '(^|[[:space:]])(csqtt|csqtt-47000)(\.service|[[:space:]]|$)' "$TEST_SYSTEMCTL_LOG"; then
  echo "FAIL: test harness observed a command targeting a production CSQTT unit" >&2; exit 1
fi
grep -Fq 'csqtt-lalune.service' "$TEST_SYSTEMCTL_LOG"

echo "CSQTT LaLune installer/uninstaller disposable tests: PASS"
