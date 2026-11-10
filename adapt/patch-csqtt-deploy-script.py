#!/usr/bin/env python3
"""Derive an isolated LaLune deploy script from pinned CSQTT v2.1.9 deploy.sh.

This deliberately retains the upstream install/health-check/uninstall flow.
Only runtime-owned names, ports, the patched TUN subnet, and unsafe port
conflict behavior are adapted. Run after fetching the pinned upstream script.
"""
from pathlib import Path
import hashlib
import re

EXPECTED_UPSTREAM_SHA256 = "6c94fb8dd033a4f1bab2961e7a1f6edfc1bdc532129d4e9cbf459684ba61ef5a"
source_path = Path("server-assets/deploy.sh")
output_path = Path("server-assets/deploy-csqtt-lalune.sh")
raw_source = source_path.read_bytes()
actual_sha256 = hashlib.sha256(raw_source).hexdigest()
if actual_sha256 != EXPECTED_UPSTREAM_SHA256:
    raise SystemExit(f"refusing to patch an unpinned CSQTT deploy.sh: sha256={actual_sha256}")
src = raw_source.decode("utf-8")

replacements = [
    ('readonly PEER_PORT="${CSQTT_PEER_PORT:-46000}"', 'readonly PEER_PORT="${CSQTT_PEER_PORT:-47000}"'),
    ('readonly WEB_PORT="${CSQTT_WEB_PORT:-46002}"', 'readonly WEB_PORT="${CSQTT_WEB_PORT:-47002}"'),
    ('readonly CSQTT_WIRE_PROTOCOL_REVISION="CSQTT-WIRE-3"', 'readonly CSQTT_WIRE_PROTOCOL_REVISION="CSQTT-WIRE-4"'),
    ('readonly LOG_FILE="/var/log/csqtt-install.log"', 'readonly LOG_FILE="/var/log/csqtt-lalune-install.log"'),
    ('readonly CSQTT_IFACE="csqtt1"', 'readonly CSQTT_IFACE="csqtt-lalune0"'),
    ('readonly CSQTT_CONFIG_DIR="/etc/csqtt"', 'readonly CSQTT_CONFIG_DIR="/etc/csqtt-lalune"'),
    ('readonly CSQTT_LE_CERTBOT="/opt/csqtt-certbot/bin/certbot"', 'readonly CSQTT_LE_CERTBOT="/opt/csqtt-lalune-certbot/bin/certbot"'),
    ('readonly CSQTT_LE_VENV="/opt/csqtt-certbot"', 'readonly CSQTT_LE_VENV="/opt/csqtt-lalune-certbot"'),
    ('readonly CSQTT_LE_RENEW_HELPER="/usr/local/lib/csqtt/letsencrypt-renew.sh"', 'readonly CSQTT_LE_RENEW_HELPER="/usr/local/lib/csqtt-lalune/letsencrypt-renew.sh"'),
    ('readonly CSQTT_LE_SERVICE="csqtt-letsencrypt.service"', 'readonly CSQTT_LE_SERVICE="csqtt-lalune-letsencrypt.service"'),
    ('readonly CSQTT_LE_TIMER="csqtt-letsencrypt.timer"', 'readonly CSQTT_LE_TIMER="csqtt-lalune-letsencrypt.timer"'),
    ('readonly CSQTT_SYSCTL_FILE="/etc/sysctl.d/99-csqtt.conf"', 'readonly CSQTT_SYSCTL_FILE="/etc/sysctl.d/99-csqtt-lalune.conf"'),
    ('readonly CSQTT_UDP_SYSCTL_FILE="/etc/sysctl.d/99-csqtt-udp-buffers.conf"', 'readonly CSQTT_UDP_SYSCTL_FILE="/etc/sysctl.d/99-csqtt-lalune-udp-buffers.conf"'),
    ('readonly IPT_COMMENT="CSQTT_MANAGED"', 'readonly IPT_COMMENT="CSQTT_LALUNE_MANAGED"'),
    ('readonly CSQTT_DOCKER_PREREQ_SERVICE="csqtt-docker-prereq.service"', 'readonly CSQTT_DOCKER_PREREQ_SERVICE="csqtt-lalune-docker-prereq.service"'),
    ('readonly CSQTT_DOCKER_PREREQ_HELPER="/usr/local/lib/csqtt/docker-prereq.sh"', 'readonly CSQTT_DOCKER_PREREQ_HELPER="/usr/local/lib/csqtt-lalune/docker-prereq.sh"'),
    ('readonly CSQTT_RUNTIME_DIR="/run/csqtt"', 'readonly CSQTT_RUNTIME_DIR="/run/csqtt-lalune"'),
    ('readonly UPLOAD_BINARY="/tmp/.csqtt-upload-server"', 'readonly UPLOAD_BINARY="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-server"'),
    ('readonly UPLOAD_ENV_FILE="/tmp/.csqtt-upload-web.env"', 'readonly UPLOAD_ENV_FILE="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-web.env"'),
    ('readonly UPLOAD_OVERRIDES_FILE="/tmp/.csqtt-upload-overrides.json"', 'readonly UPLOAD_OVERRIDES_FILE="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-overrides.json"'),
    ('readonly UPLOAD_OVERRIDES_FILE="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-overrides.json"', 'readonly UPLOAD_OVERRIDES_FILE="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-overrides.json"\nreadonly UPLOAD_SING_BOX="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-sing-box"'),
    ('readonly CSQTT_DOCKER_IMAGE="csqtt:2.1.9"', 'readonly CSQTT_DOCKER_IMAGE="csqtt-lalune:2.1.9"'),
    ('readonly CSQTT_DOCKER_CONTAINER="csqtt"', 'readonly CSQTT_DOCKER_CONTAINER="csqtt-lalune"'),
]
for old, new in replacements:
    count = src.count(old)
    if count != 1:
        raise SystemExit(f"expected exactly one upstream marker ({count}): {old}")
    src = src.replace(old, new, 1)

# Replace the standalone lowercase runtime name, never function identifiers
# such as csqtt_cleanup or uppercase CSQTT_* protocol/environment variables.
src = re.sub(r"(?<![A-Za-z0-9_-])csqtt(?![A-Za-z0-9_-])", "csqtt-lalune", src)

# Keep filenames that the pinned Rust server binary opens internally.
# The server's SQLite path is compiled as <config-dir>/csqtt.db; renaming that
# basename only in the shell installer would split state between two databases.
# The env file can retain its upstream basename because its parent directory
# is already namespaced (/etc/csqtt-lalune).
file_contracts = [
    ('readonly CSQTT_DATABASE_FILE="csqtt-lalune.db"', 'readonly CSQTT_DATABASE_FILE="csqtt.db"'),
    ('readonly CSQTT_DATABASE_WAL_FILE="csqtt-lalune.db-wal"', 'readonly CSQTT_DATABASE_WAL_FILE="csqtt.db-wal"'),
    ('readonly CSQTT_DATABASE_SHM_FILE="csqtt-lalune.db-shm"', 'readonly CSQTT_DATABASE_SHM_FILE="csqtt.db-shm"'),
    ('readonly CSQTT_ENV_FILE="${CSQTT_CONFIG_DIR}/csqtt-lalune.env"', 'readonly CSQTT_ENV_FILE="${CSQTT_CONFIG_DIR}/csqtt.env"'),
]
for old, new in file_contracts:
    if src.count(old) != 1:
        raise SystemExit(f"expected exactly one server-compatible filename marker: {old}")
    src = src.replace(old, new, 1)

# The upstream Docker-state archiver uses absolute paths inside the container.
# Namespace the directory, but keep the DB basename expected by the Rust binary.
for old, new in [
    ("/etc/csqtt-lalune/csqtt-lalune.db ", "/etc/csqtt-lalune/csqtt.db "),
    ("/etc/csqtt-lalune/csqtt-lalune.db-wal ", "/etc/csqtt-lalune/csqtt.db-wal "),
    ("/etc/csqtt-lalune/csqtt-lalune.db-shm ", "/etc/csqtt-lalune/csqtt.db-shm "),
]:
    if src.count(old) != 1:
        raise SystemExit(f"expected exactly one namespaced Docker DB archive path: {old}")
    src = src.replace(old, new, 1)

for old, new in [
    ("csqtt-cascade", "csqtt-lalune-cascade"),
    ("csqtt-docker-prereq", "csqtt-lalune-docker-prereq"),
    ("csqtt-letsencrypt", "csqtt-lalune-letsencrypt"),
    ("csqtt-certbot", "csqtt-lalune-certbot"),
    ("csqtt-docker-", "csqtt-lalune-docker-"),
    ("csqtt-ip-", "csqtt-lalune-ip-"),
    ("/usr/local/bin/hev-socks5-tunnel", "/usr/local/bin/lalune-hev-socks5-tunnel"),
    ("/usr/local/bin/hev-socks5-server", "/usr/local/bin/lalune-hev-socks5-server"),
    ("/tmp/csqtt-web-probe.", "${CSQTT_DEPLOY_STAGE:-/tmp}/csqtt-lalune-web-probe."),
    ("/tmp/csqtt-docker.", "/tmp/csqtt-lalune-docker."),
    ('probe_iface="cqtd$(( $$ % 100000 ))"', 'probe_iface="cqtdl$(( $$ % 100000 ))"'),
    ('probe_chain="CSQTT_D$(( $$ % 1000000 ))"', 'probe_chain="CSQTT_LALUNE_D$(( $$ % 1000000 ))"'),
    ("com.csqtt.managed=true", "com.csqtt-lalune.managed=true"),
    ("com.csqtt.component=server", "com.csqtt-lalune.component=server"),
    ("10.66.67", "10.67.68"),
]:
    src = src.replace(old, new)

# The original cleanup removed generic legacy proxy interfaces. They are not
# owned by this instance and must be left alone.
old = """cleanup_legacy_proxy_interfaces() {
    local iface
    for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep '^csqtp' || true); do
        timeout 2 ip link del "$iface" 2>/dev/null || true
    done
}"""
new = """cleanup_legacy_proxy_interfaces() {
    # The LaLune service owns no legacy proxy interfaces. Never remove
    # an interface that may belong to the original CSQTT runtime.
    return 0
}"""
if src.count(old) != 1:
    raise SystemExit("expected exactly one legacy proxy cleanup function")
src = src.replace(old, new, 1)

# The upstream cleanup owns several shared CSQTT markers. LaLune must only
# remove rules carrying its own IPT_COMMENT; the original service may still
# rely on CSQTT_MIRRORED/TPROXY/SOCKS/CASCADE rules.
old = 'for marker in "$IPT_COMMENT" CSQTT_MIRRORED CSQTT_TPROXY CSQTT_LOCAL_SOCKS CSQTT_LOCAL_SOCKS_MARK CSQTT_SOCKS CSQTT_CASCADE_NO_QUIC; do'
new = 'for marker in "$IPT_COMMENT" CSQTT_LALUNE_MIRRORED CSQTT_LALUNE_TPROXY CSQTT_LALUNE_LOCAL_SOCKS CSQTT_LALUNE_LOCAL_SOCKS_MARK CSQTT_LALUNE_SOCKS CSQTT_LALUNE_CASCADE_NO_QUIC; do'
if src.count(old) != 1:
    raise SystemExit("expected exactly one shared netfilter-marker cleanup loop")
src = src.replace(old, new, 1)

# Port guards run before service cutover and allow only the already-owned
# LaLune process during a redeploy. Unrelated or unidentifiable listeners fail closed.
port_guards = r'''
web_port_listeners() {
    ss -H -ltnp "sport = :${WEB_PORT}" 2>/dev/null || true
}

web_port_listener_pids() {
    web_port_listeners | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -un || true
}

assert_deployment_ports_not_foreign() {
    local listeners pids pid
    listeners="$(peer_port_listeners)"
    if [ -n "$listeners" ]; then
        pids="$(peer_port_listener_pids)"
        [ -n "$pids" ] || die "UDP/${PEER_PORT} occupied but its owner cannot be identified; refusing to change runtime"
        while IFS= read -r pid; do
            [ -n "$pid" ] || continue
            csqtt_process_owns_peer_port "$pid" || die "UDP/${PEER_PORT} is occupied by unrelated PID ${pid}; refusing to stop it"
        done <<< "$pids"
    fi
    listeners="$(web_port_listeners)"
    if [ -n "$listeners" ]; then
        pids="$(web_port_listener_pids)"
        [ -n "$pids" ] || die "TCP/${WEB_PORT} occupied but its owner cannot be identified; refusing to change runtime"
        while IFS= read -r pid; do
            [ -n "$pid" ] || continue
            csqtt_process_is_owned "$pid" || die "TCP/${WEB_PORT} is occupied by unrelated PID ${pid}; refusing to stop it"
        done <<< "$pids"
    fi
}

assert_web_port_is_available() {
    local listeners
    listeners="$(web_port_listeners)"
    [ -z "$listeners" ] && return 0
    log_error "TCP/${WEB_PORT} remains occupied after stopping LaLune; no listener will be terminated"
    printf '%s\n' "$listeners" | tee -a "$LOG_FILE" >&2
    die "TCP/${WEB_PORT} remains occupied; refusing to start LaLune"
}

'''
# The upstream installer used to kill any process occupying UDP/PEER_PORT.
# Retain recovery for our own process only; fail closed for unrelated listeners.
old = """assert_peer_port_is_available() {
    local listeners
    listeners="$(peer_port_listeners)"
    [ -z "$listeners" ] && return 0
    log_warn "UDP/${PEER_PORT} занят; выполняется принудительное освобождение"
    force_release_peer_port || die "Не удалось принудительно освободить UDP/${PEER_PORT}"
    [ -z "$(peer_port_listeners)" ] || die "UDP/${PEER_PORT} остался занят после принудительной очистки"
}"""
new = """assert_peer_port_is_available() {
    local listeners pid
    listeners="$(peer_port_listeners)"
    [ -z "$listeners" ] && return 0
    while IFS= read -r pid; do
        [ -n "$pid" ] || continue
        if ! csqtt_process_owns_peer_port "$pid"; then
            log_error "UDP/${PEER_PORT} занят посторонним процессом PID ${pid}; он не будет остановлен"
            printf '%s\n' "$listeners" | tee -a "$LOG_FILE" >&2
            die "Порт UDP/${PEER_PORT} занят посторонним процессом; конфликтующий процесс не изменён"
        fi
    done < <(peer_port_listener_pids)
    log_warn "UDP/${PEER_PORT} занят принадлежащим LaLune процессом; выполняется освобождение"
    force_release_peer_port || die "Не удалось освободить UDP/${PEER_PORT} LaLune"
    [ -z "$(peer_port_listeners)" ] || die "UDP/${PEER_PORT} остался занят после очистки LaLune"
}"""
if src.count(old) != 1:
    raise SystemExit("expected exactly one peer-port conflict handler")
src = src.replace(old, new, 1)

marker = "assert_peer_port_is_available() {"
if src.count(marker) != 1:
    raise SystemExit("expected one peer-port guard insertion point")
src = src.replace(marker, port_guards + marker, 1)

old = '''            require_runtime_tools
            if [ "$DEPLOY_MODE" = "docker" ]; then'''
new = '''            require_runtime_tools
            assert_deployment_ports_not_foreign
            if [ "$DEPLOY_MODE" = "docker" ]; then'''
if src.count(old) != 1:
    raise SystemExit("expected one pre-sysctl runtime-tools sequence")
src = src.replace(old, new, 1)

old = '''            run_timed "проверка upload" prepare_uploaded_release
            if [ "$DEPLOY_MODE" = "docker" ]; then'''
new = '''            run_timed "проверка upload" prepare_uploaded_release
            assert_deployment_ports_not_foreign
            if [ "$DEPLOY_MODE" = "docker" ]; then'''
if src.count(old) != 1:
    raise SystemExit("expected one pre-cutover upload sequence")
src = src.replace(old, new, 1)

old = '''    assert_peer_port_is_available

    cleanup_legacy_proxy_interfaces'''
new = '''    assert_peer_port_is_available
    assert_web_port_is_available

    cleanup_legacy_proxy_interfaces'''
if src.count(old) != 1:
    raise SystemExit("expected one cleanup port-release sequence")
src = src.replace(old, new, 1)

# A previous helper may have been installed by another local project. It is
# not safe to infer ownership from a shared /usr/local/bin basename. Keep it
# untouched during both redeploy and uninstall; the in-process SOCKS5 server
# is owned by the csqtt-lalune binary and needs no external HEV executable.
for old in [
    '    rm -f /usr/local/bin/lalune-hev-socks5-tunnel\n',
    '    rm -f /usr/local/bin/csqtt-lalune-cascade /usr/local/bin/lalune-hev-socks5-server\n',
    '    rm -f /usr/local/bin/lalune-hev-socks5-tunnel /usr/local/bin/lalune-hev-socks5-server\n',
]:
    src = src.replace(old, '    # Preserve legacy SOCKS5 helpers unless their ownership is independently verified.\n')

# Install a pinned, namespaced sing-box runtime alongside the original CSQTT service.
# Never replace a system-wide sing-box binary or adopt an unmarked directory.
sing_box_installer = r'''install_sing_box_binary() {
    local root="/usr/local/lib/csqtt-lalune/sing-box"
    local version_dir="${root}/1.14.4"
    local binary="${version_dir}/sing-box"
    local root_marker="${root}/.managed-by-lalune"
    local version_marker="${version_dir}/.managed-by-lalune"
    local data_dir="/etc/csqtt-lalune/sing-box"
    local data_marker="${data_dir}/.managed-by-lalune"
    local marker_value="CSQTT-LALUNE-SING-BOX-MANAGED-V1"
    local data_marker_value="CSQTT-LALUNE-SING-BOX-DATA-V1"
    local version_output

    [ -s "$UPLOAD_SING_BOX" ] || die "Pinned sing-box 1.14.4 asset is missing" "$EXIT_PREFLIGHT_FAILED"
    for owned_path in "/usr/local/lib/csqtt-lalune" "$root" "$version_dir" "$binary" "$data_dir"; do
        [ ! -L "$owned_path" ] || die "$owned_path is a symlink; refusing to replace or follow it"
    done
    if [ -e "$root" ]; then
        [ -f "$root_marker" ] && [ "$(cat "$root_marker")" = "$marker_value" ] || die "$root exists without our ownership marker; refusing to adopt it"
    else
        install -d -m 0755 "$root"
        printf '%s\n' "$marker_value" > "$root_marker"
        chmod 0644 "$root_marker"
    fi
    if [ -e "$version_dir" ]; then
        [ -f "$version_marker" ] && [ "$(cat "$version_marker")" = "$marker_value" ] || die "$version_dir exists without our ownership marker; refusing to adopt it"
    else
        install -d -m 0755 "$version_dir"
        printf '%s\n' "$marker_value" > "$version_marker"
        chmod 0644 "$version_marker"
    fi
    if [ -e "$data_dir" ]; then
        [ -f "$data_marker" ] && [ "$(cat "$data_marker")" = "$data_marker_value" ] || die "$data_dir exists without our ownership marker; refusing to adopt it"
    else
        install -d -m 0700 "$data_dir"
        printf '%s\n' "$data_marker_value" > "$data_marker"
        chmod 0600 "$data_marker"
    fi
    [ ! -L "${binary}.new" ] || die "temporary sing-box binary path is a symlink"
    rm -f -- "${binary}.new"
    install -m 0755 "$UPLOAD_SING_BOX" "${binary}.new"
    version_output="$("${binary}.new" version 2>&1)" || die "bundled sing-box binary cannot execute: $version_output"
    printf '%s\n' "$version_output" | grep -Fq "sing-box version 1.14.4" || die "bundled sing-box version mismatch: $version_output"
    mv -f -- "${binary}.new" "$binary"
    log_info "Pinned sing-box 1.14.4 installed in the LaLune-owned directory"
}

'''
marker_install = "setup_csqtt_binary() {"
if src.count(marker_install) != 1:
    raise SystemExit("expected exactly one CSQTT binary installation function")
src = src.replace(marker_install, sing_box_installer + marker_install, 1)
old_binary = '''    install -m 0755 "$UPLOAD_BINARY" /usr/local/bin/csqtt || \\
        die "Не удалось установить новый бинарник csqtt"
    echo "✓ csqtt установлен"'''
new_binary = old_binary + '''
    install_sing_box_binary'''
if src.count(old_binary) != 1:
    raise SystemExit("expected exactly one CSQTT binary install sequence")
src = src.replace(old_binary, new_binary, 1)
old_upload = '''    [ -f "$UPLOAD_OVERRIDES_FILE" ] && [ -s "$UPLOAD_OVERRIDES_FILE" ] || \\
        die "Одноразовая deploy-конфигурация не загружена" "$EXIT_PREFLIGHT_FAILED"'''
new_upload = old_upload + '''
    [ -f "$UPLOAD_SING_BOX" ] && [ -s "$UPLOAD_SING_BOX" ] || \\
        die "Pinned sing-box 1.14.4 asset is not uploaded" "$EXIT_PREFLIGHT_FAILED"
    local sing_box_version
    sing_box_version="$(timeout 5 "$UPLOAD_SING_BOX" version 2>/dev/null || true)"
    printf '%s\n' "$sing_box_version" | grep -Fq "sing-box version 1.14.4" || \\
        die "Uploaded sing-box binary is not the pinned 1.14.4 release" "$EXIT_PREFLIGHT_FAILED"'''
if src.count(old_upload) != 1:
    raise SystemExit("expected exactly one uploaded release preflight sequence")
src = src.replace(old_upload, new_upload, 1)

# The patched server uses this variable for restart actions initiated by its panel.
old = "Environment=CSQTT_SERVICE_MANAGER=systemd"
new = old + "\nEnvironment=CSQTT_SYSTEMD_UNIT=csqtt-lalune.service\nEnvironment=CSQTT_SING_BOX_BINARY=/usr/local/lib/csqtt-lalune/sing-box/1.14.4/sing-box\nEnvironment=CSQTT_SING_BOX_DATA_DIR=/etc/csqtt-lalune/sing-box"
if src.count(old) != 1:
    raise SystemExit("expected exactly one systemd service-manager environment line")
src = src.replace(old, new, 1)

# Give the DeployManager an explicit namespaced success contract for both lifecycle operations.
old = 'echo "CSQTT_DEPLOY_OK"'
if src.count(old) != 2:
    raise SystemExit(f"expected exactly two upstream install success markers, found {src.count(old)}")
src = src.replace(old, 'echo "CSQTT_LALUNE_DEPLOY_OK"')

old = '    log_info "CSQTT удалён. SQLite state и конфигурация сохранены в ${CSQTT_CONFIG_DIR}"\n}'
new = '    log_info "CSQTT удалён. SQLite state и конфигурация сохранены в ${CSQTT_CONFIG_DIR}"\n    echo "CSQTT_LALUNE_UNINSTALL_OK"\n}'
if src.count(old) != 1:
    raise SystemExit("expected exactly one end of upstream uninstall function")
src = src.replace(old, new, 1)

# Optional CI switch: keep the upstream TLS setup flow in normal use, while
# avoiding an outbound Let's Encrypt request in a disposable test VM.
old = "setup_letsencrypt_ip_tls() {"
new = """setup_letsencrypt_ip_tls() {
    if [ "${CSQTT_LALUNE_SKIP_LETSENCRYPT:-0}" = "1" ]; then
        log_info "LaLune test setting: skipping optional Let's Encrypt issuance"
        return 0
    fi"""
if src.count(old) != 1:
    raise SystemExit("expected exactly one Let's Encrypt setup function")

src = src.replace(old, new, 1)

for forbidden in [
    "/etc/csqtt/", "/usr/local/bin/csqtt ", "/usr/local/lib/csqtt/",
    "csqtt.service", "csqtt-letsencrypt.service", "csqtt-cascade.service",
    "CSQTT_MANAGED", "csqtt1", "10.66.67", "grep '^csqtp'",
]:
    if forbidden in src:
        raise SystemExit(f"shared runtime identifier remains: {forbidden}")
for required in [
    'CSQTT_PEER_PORT:-47000', 'CSQTT_WEB_PORT:-47002',
    'readonly CSQTT_WIRE_PROTOCOL_REVISION="CSQTT-WIRE-4"',
    'csqtt-lalune.service', 'csqtt-lalune0', '10.67.68.0/24',
    'CSQTT_LALUNE_MANAGED', 'CSQTT_SYSTEMD_UNIT=csqtt-lalune.service',
    'CSQTT_DEPLOY_STAGE:-/tmp', 'CSQTT_LALUNE_SKIP_LETSENCRYPT',
    'посторонним процессом',
]:
    if required not in src:
        raise SystemExit(f"required LaLune adaptation missing: {required}")

output_path.write_text(src)
print(f"Created {output_path} from pinned CSQTT 2.1.9 deploy.sh")
