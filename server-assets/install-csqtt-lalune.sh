#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# Dedicated installer for the patched CSQTT v2.1.9 LaLune dataplane.
# Never invokes upstream deploy.sh. It owns only explicitly named LaLune paths.
readonly UNIT="csqtt-lalune.service"
readonly BIN="/usr/local/bin/csqtt-lalune"
readonly LIB="/usr/local/lib/csqtt-lalune"
readonly ETC="/etc/csqtt-lalune"
readonly STATE="/var/lib/csqtt-lalune"
readonly LOG="/var/log/csqtt-lalune"
readonly UNIT_FILE="/etc/systemd/system/csqtt-lalune.service"
readonly MARKER="CSQTT-LALUNE-MANAGED-V1"
WEB_PORT="${CSQTT_LALUNE_WEB_PORT:-47002}"
SECRETS_FILE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --secrets-file) [[ $# -ge 2 ]] || { echo "missing value for --secrets-file" >&2; exit 2; }; SECRETS_FILE="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

die(){ echo "csqtt-lalune install: $*" >&2; exit 1; }
[[ ${EUID} -eq 0 ]] || die "run as root"
[[ "$(uname -s)" == Linux ]] || die "Linux is required"
case "$(uname -m)" in
  aarch64|arm64) SERVER_ASSET="csqtt-lalune-linux-arm64" ;;
  x86_64|amd64) SERVER_ASSET="csqtt-lalune-linux-amd64" ;;
  *) die "unsupported server architecture: $(uname -m)" ;;
esac

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
SOURCE="${HERE}/${SERVER_ASSET}"
[[ -s "$SOURCE" ]] || die "missing isolated binary: $SOURCE"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"
command -v ss >/dev/null 2>&1 || die "ss is required for safe port preflight"
command -v ip >/dev/null 2>&1 || die "iproute2 is required for safe route/interface preflight"
command -v iptables-save >/dev/null 2>&1 || die "iptables is required for safe firewall preflight"
for port in "$WEB_PORT"; do
  [[ "$port" =~ ^[0-9]{1,5}$ ]] && (( port >= 1 && port <= 65535 )) || die "invalid web port: $port"
done
SSH_PORT="${CSQTT_SSH_PORT:-22}"
[[ "$SSH_PORT" =~ ^[0-9]{1,5}$ ]] && (( SSH_PORT >= 1 && SSH_PORT <= 65535 )) || die "invalid SSH port: $SSH_PORT"

# Reject symlinks before checking ownership markers or writing anything. A marker
# reachable through a symlink does not make the external target LaLune-owned.
for owned_path in "$UNIT_FILE" "$BIN" "$LIB" "$ETC" "$STATE" "$LOG"; do
  [[ ! -L "$owned_path" ]] || die "$owned_path is a symlink; refusing to follow or replace it"
done

# Never adopt or overwrite another service or an unowned path.
if [[ -e "$UNIT_FILE" ]] && ! grep -Fq 'Description=CSQTT LaLune isolated dataplane' "$UNIT_FILE"; then
  die "$UNIT_FILE already exists and is not our service"
fi
if [[ -e "$ETC" ]]; then
  [[ -f "$ETC/.managed-by-lalune" && ! -L "$ETC/.managed-by-lalune" ]] || die "$ETC exists without a regular ownership marker; refusing to adopt it"
  [[ "$(cat "$ETC/.managed-by-lalune")" == "$MARKER" ]] || die "ownership marker mismatch"
fi
if [[ -e "$BIN" && ! -e "$UNIT_FILE" ]]; then
  die "$BIN exists without our service unit; refusing to overwrite"
fi
if [[ -e "$LIB" && ! -f "$LIB/.managed-by-lalune" ]]; then
  die "$LIB exists without our ownership marker; refusing to overwrite"
fi
for owned_dir in "$STATE" "$LOG"; do
  if [[ -e "$owned_dir" ]]; then
    [[ -f "$owned_dir/.managed-by-lalune" && ! -L "$owned_dir/.managed-by-lalune" ]] || die "$owned_dir exists without a regular ownership marker; refusing to adopt it"
    [[ "$(cat "$owned_dir/.managed-by-lalune")" == "$MARKER" ]] || die "$owned_dir ownership marker mismatch"
  fi
done

# Never stop a running service automatically: a preflight failure must not
# interrupt an already working instance. Upgrades require an explicit stop by
# the operator after checking the maintenance window.
if [[ -e "$UNIT_FILE" ]] && systemctl is-active --quiet "$UNIT"; then
  die "$UNIT is already active; stop it deliberately before replacing the binary"
fi

# Do not kill listeners. Preflight the actual panel TCP listener before making filesystem changes.
for port in "$WEB_PORT"; do
  if ss -H -ltn "sport = :$port" | grep -q .; then
    die "TCP panel port $port is occupied; no listener will be killed"
  fi
done
if ip link show dev csqtt-lalune0 >/dev/null 2>&1; then
  die "csqtt-lalune0 already exists; refusing to adopt or reconfigure an existing interface"
fi
RULES="$(ip -4 rule show)"
if printf '%s\n' "$RULES" | grep -Eq '(^|[[:space:]])(47001|47066):|fwmark (0x6741|0x6742)(/|[[:space:]])|lookup (47001|47066)([[:space:]]|$)'; then
  die "LaLune policy identifiers already exist; refusing to delete or reuse existing rules"
fi
if [[ -n "$(ip -4 route show table 47001 2>/dev/null)" || -n "$(ip -4 route show table 47066 2>/dev/null)" ]]; then
  die "LaLune route table 47001/47066 is non-empty; refusing to flush or adopt it"
fi
if iptables-save 2>/dev/null | grep -Fq 'CSQTT_LALUNE_'; then
  die "LaLune firewall markers already exist; refusing to adopt or clean up existing rules"
fi

[[ -n "$SECRETS_FILE" && -f "$SECRETS_FILE" && ! -L "$SECRETS_FILE" ]] || die "a regular --secrets-file is required"
mapfile -t secret_values < "$SECRETS_FILE"
[[ "${#secret_values[@]}" -ge 4 ]] || die "secrets file must contain four values"
MAIN_PASSWORD="${secret_values[0]}"
WEB_USER="${secret_values[1]}"
WEB_PASSWORD="${secret_values[2]}"
DEVICE_ID="${secret_values[3]}"
for value in "$MAIN_PASSWORD" "$WEB_USER" "$WEB_PASSWORD"; do
  [[ -n "$value" && "$value" =~ ^[A-Za-z0-9]+$ ]] || die "credentials must be non-empty ASCII letters or digits"
done
[[ -n "$DEVICE_ID" && "$DEVICE_ID" =~ ^[A-Za-z0-9-]+$ ]] || die "device ID must be non-empty ASCII letters, digits, or hyphens"

install -d -m 0750 "$ETC" "$STATE" "$LOG" "$LIB"
printf '%s\n' "$MARKER" > "$ETC/.managed-by-lalune"
printf '%s\n' "$MARKER" > "$LIB/.managed-by-lalune"
printf '%s\n' "$MARKER" > "$STATE/.managed-by-lalune"
printf '%s\n' "$MARKER" > "$LOG/.managed-by-lalune"
cat > "$ETC/server.env.new" <<ENV
CSQTT_MAIN_PASSWORD=$MAIN_PASSWORD
CSQTT_WEB_USER=$WEB_USER
CSQTT_WEB_PASS=$WEB_PASSWORD
LALUNE_PANEL_USER=$WEB_USER
LALUNE_PANEL_PASSWORD=$WEB_PASSWORD
CSQTT_DEVICE_ID=$DEVICE_ID
CSQTT_SERVICE_MANAGER=systemd
CSQTT_SYSTEMD_UNIT=csqtt-lalune.service
CSQTT_LALUNE_WEB_PORT=$WEB_PORT
ENV
chmod 0600 "$ETC/server.env.new"
mv -f -- "$ETC/server.env.new" "$ETC/server.env"
install -m 0755 "$SOURCE" "${BIN}.new"
mv -f -- "${BIN}.new" "$BIN"

# Keep configuration and state in LaLune-only directories. No global sysctl,
# firewall chain, main route table, generic csqtt path, or upstream installer.
cat > "${UNIT_FILE}.new" <<'UNIT'
[Unit]
Description=CSQTT LaLune isolated dataplane
Documentation=https://github.com/Xenofan-git/CSQTT-sing-box-for-Debian-Ubuntu-ARM64
After=network-online.target
Wants=network-online.target
ConditionPathIsExecutable=/usr/local/bin/csqtt-lalune

[Service]
Type=simple
EnvironmentFile=/etc/csqtt-lalune/server.env
ExecStart=/usr/local/bin/csqtt-lalune
Restart=on-failure
RestartSec=3
UMask=0077
LimitNOFILE=65536
# CSQTT needs root for its dedicated TUN and namespaced policy rules.
User=root
Group=root
NoNewPrivileges=false
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=/etc/csqtt-lalune /var/lib/csqtt-lalune /var/log/csqtt-lalune /run
[Install]
WantedBy=multi-user.target
UNIT
chmod 0644 "${UNIT_FILE}.new"
mv -f -- "${UNIT_FILE}.new" "$UNIT_FILE"
systemctl daemon-reload
systemctl enable "$UNIT" >/dev/null
systemctl start "$UNIT" || {
  systemctl --no-pager --full status "$UNIT" >&2 || true
  die "service failed to start; LaLune files were retained for diagnosis"
}
systemctl is-active --quiet "$UNIT" || die "service is not active"
echo "CSQTT_LALUNE_INSTALL_OK"
