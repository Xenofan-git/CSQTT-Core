#!/usr/bin/env python3
"""Static regression tests for the namespaced original CSQTT v2.1.9 deploy.sh."""
from pathlib import Path
import hashlib

EXPECTED_UPSTREAM_SHA256 = "6c94fb8dd033a4f1bab2961e7a1f6edfc1bdc532129d4e9cbf459684ba61ef5a"
upstream_path = Path("server-assets/deploy.sh")
upstream_bytes = upstream_path.read_bytes()
if hashlib.sha256(upstream_bytes).hexdigest() != EXPECTED_UPSTREAM_SHA256:
    raise SystemExit("upstream deploy.sh does not match the pinned CSQTT v2.1.9 source")
upstream = upstream_bytes.decode("utf-8")
script = Path("server-assets/deploy-csqtt-lalune.sh").read_text()

def need(fragment: str) -> None:
    if fragment not in script:
        raise SystemExit(f"missing required original/LaLune deploy invariant: {fragment}")

def forbid(fragment: str) -> None:
    if fragment in script:
        raise SystemExit(f"conflicting original CSQTT resource remains: {fragment}")

if 'readonly SCRIPT_VERSION="2.1.9"' not in upstream:
    raise SystemExit("source is not the pinned CSQTT v2.1.9 deploy.sh")
if 'readonly CSQTT_WIRE_PROTOCOL_REVISION="CSQTT-WIRE-3"' not in upstream:
    raise SystemExit("pinned upstream deploy.sh protocol baseline changed unexpectedly")
if 'readonly SCRIPT_VERSION="2.1.9"' not in script:
    raise SystemExit("derived deploy script lost the upstream version marker")
need('readonly CSQTT_WIRE_PROTOCOL_REVISION="CSQTT-WIRE-4"')
if 'readonly CSQTT_WIRE_PROTOCOL_REVISION="CSQTT-WIRE-3"' in script:
    raise SystemExit("derived deploy script still rejects the production WIRE-4 server")
need('uploaded_wire_protocol="$(timeout 5 "$UPLOAD_BINARY" --protocol-revision 2>/dev/null || true)"')
need('[ "$uploaded_wire_protocol" = "$CSQTT_WIRE_PROTOCOL_REVISION" ]')
need('readonly UPLOAD_SING_BOX="${CSQTT_DEPLOY_STAGE:-/tmp}/.csqtt-lalune-upload-sing-box"')
need('install_sing_box_binary()')
need('Pinned sing-box 1.14.4 asset is missing')
need('Environment=CSQTT_SING_BOX_BINARY=/usr/local/lib/csqtt-lalune/sing-box/1.14.4/sing-box')

# Keep the original deployment pipeline rather than replacing it with a second installer.
for function in [
    "prepare_uploaded_release()", "run_platform_preflight()", "setup_nat_and_firewall()",
    "setup_csqtt_environment()", "setup_csqtt_service()", "start_csqtt()",
    "verify_configured_network()", "verify_web_panel()", "do_uninstall()",
    "cleanup_deploy_uploads()", "CSQTT_LALUNE_DEPLOY_OK",
]:
    need(function)

# The LaLune runtime owns separate ports, paths, services, process and network names.
for value in [
    'CSQTT_PEER_PORT:-47000', 'CSQTT_WEB_PORT:-47002',
    'CSQTT_IFACE="csqtt-lalune0"', 'CSQTT_CONFIG_DIR="/etc/csqtt-lalune"',
    '/usr/local/bin/csqtt-lalune', '/usr/local/lib/csqtt-lalune',
    '/var/log/csqtt-lalune-install.log', '/opt/csqtt-lalune-certbot', 'csqtt-lalune.service',
    'CSQTT_LALUNE_MANAGED', '10.67.68.0/24',
    'Environment=CSQTT_SYSTEMD_UNIT=csqtt-lalune.service',
    'CSQTT_LALUNE_SKIP_LETSENCRYPT',
    'readonly CSQTT_DATABASE_FILE="csqtt.db"',
    'readonly CSQTT_DATABASE_WAL_FILE="csqtt.db-wal"',
    'readonly CSQTT_DATABASE_SHM_FILE="csqtt.db-shm"',
    'readonly CSQTT_ENV_FILE="${CSQTT_CONFIG_DIR}/csqtt.env"',
    '/etc/csqtt-lalune/csqtt.db',
    '/etc/csqtt-lalune/csqtt.db-wal',
    '/etc/csqtt-lalune/csqtt.db-shm',
    'assert_deployment_ports_not_foreign()', 'web_port_listeners()', 'assert_web_port_is_available()',
    'probe_iface="cqtdl', 'probe_chain="CSQTT_LALUNE_D',
    'for marker in "$IPT_COMMENT" CSQTT_LALUNE_MIRRORED CSQTT_LALUNE_TPROXY CSQTT_LALUNE_LOCAL_SOCKS CSQTT_LALUNE_LOCAL_SOCKS_MARK CSQTT_LALUNE_SOCKS CSQTT_LALUNE_CASCADE_NO_QUIC; do',
]:
    need(value)

# Refuse unrelated listeners and leave the original CSQTT legacy interfaces alone.
need('csqtt_process_owns_peer_port "$pid"')
need('CSQTT_LALUNE_UNINSTALL_OK')
need('TCP/${WEB_PORT} is occupied by unrelated PID')
need('Порт UDP/${PEER_PORT} занят посторонним процессом; конфликтующий процесс не изменён')
install_branch = script.split('install|--install|-i)', 1)[1]
if install_branch.index('assert_deployment_ports_not_foreign') > install_branch.index('run_timed "sysctl" setup_sysctl'):
    raise SystemExit("foreign-port preflight must run before sysctl mutation")
cleanup = script.split("cleanup_legacy_proxy_interfaces() {", 1)[1].split("\n}", 1)[0]
if "ip link del" in cleanup:
    raise SystemExit("LaLune cleanup still deletes generic legacy proxy interfaces")

for value in [
    'readonly CSQTT_DATABASE_FILE="csqtt-lalune.db"',
    'readonly CSQTT_DATABASE_WAL_FILE="csqtt-lalune.db-wal"',
    'readonly CSQTT_DATABASE_SHM_FILE="csqtt-lalune.db-shm"',
    'readonly CSQTT_ENV_FILE="${CSQTT_CONFIG_DIR}/csqtt-lalune.env"',
    'CSQTT_MIRRORED', 'CSQTT_TPROXY', 'CSQTT_LOCAL_SOCKS',
    'CSQTT_LOCAL_SOCKS_MARK', 'CSQTT_SOCKS', 'CSQTT_CASCADE_NO_QUIC',
    'CSQTT_PEER_PORT:-46000', 'CSQTT_WEB_PORT:-46002',
    'CSQTT_IFACE="csqtt1"', 'CSQTT_CONFIG_DIR="/etc/csqtt"',
    '/usr/local/bin/csqtt ', '/usr/local/lib/csqtt/',
    'csqtt.service', 'csqtt-letsencrypt.service', 'csqtt-cascade.service',
    '/opt/csqtt-certbot', "'|csqtt-docker-'*", 'csqtt-certbot', 'csqtt-docker-',
    'csqtt-ip-', 'csqtt-udp-buffers', 'csqtt-upload-server', 'csqtt-upload-web',
    'csqtt-upload-overrides', 'csqtt-web-probe', '/usr/local/bin/hev-socks5-tunnel',
    '/usr/local/bin/hev-socks5-server', 'CSQTT_MANAGED', 'CSQTT_DEPLOY_OK', '10.66.67', 'ip route flush cache',
    'ip route flush table main', 'ip route replace default',
]:
    forbid(value)

# Shared /usr/local/bin legacy helpers are not proven to be owned by this app.
for value in [
    'rm -f /usr/local/bin/lalune-hev-socks5-tunnel',
    'rm -f /usr/local/bin/lalune-hev-socks5-server',
]:
    forbid(value)
need('Preserve legacy SOCKS5 helpers unless their ownership is independently verified.')

print("CSQTT LaLune namespaced original deploy.sh static tests: PASS")
