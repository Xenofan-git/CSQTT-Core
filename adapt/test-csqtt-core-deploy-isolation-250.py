#!/usr/bin/env python3
"""Static fail-closed isolation test; NEVER executes installer."""
from pathlib import Path
import re
import subprocess
s=Path('server-assets/deploy-csqtt-core-250.sh').read_text()
assert subprocess.run(['bash','-n','server-assets/deploy-csqtt-core-250.sh']).returncode==0
for required in ('SCRIPT_VERSION="2.5.0"','CSQTT_PEER_PORT:-47000','CSQTT_WEB_PORT:-47002',
 '/etc/csqtt-core','/usr/local/bin/csqtt-core','csqtt-core.service','csqtt-core0',
 'CSQTT_CORE_MANAGED','10.67.69.0/24','verify_web_panel()',
 'setup_csqtt_environment()', 'setup_csqtt_service()', 'CSQTT_DEPLOY_OK',
 'CSQTT_WEB_SECRET_PATH'):
 assert required in s,required
for forbidden in ('/etc/csqtt/','/usr/local/bin/csqtt ', 'csqtt1', 'CSQTT_MANAGED',
 '10.66.67.0/24','systemctl stop csqtt-cascade.service',
 'rm -f /usr/local/bin/hev-socks5-tunnel', "grep '^csqtp'", "|'csqtt-docker-'"):
 assert forbidden not in s,forbidden
assert 'cleanup_legacy_proxy_interfaces() {\n    : # Core owns no legacy csqtp* interfaces' in s
assert 'readonly CSQTT_DATABASE_FILE="csqtt.db"' in s
assert 'readonly CSQTT_ENV_FILE="${CSQTT_CONFIG_DIR}/csqtt-core.env"' in s
assert 'for marker in "$IPT_COMMENT"; do' in s
assert 'CSQTT_MIRRORED CSQTT_TPROXY' not in s
assert 'sysctl --system' not in s
assert 'mitigations=off' not in s
assert 'update-grub' not in s
assert 'echo 1 > /proc/sys/net/ipv4/ip_forward' not in s
assert 'CSQTT_SYSCTL_FILE" <<' not in s
assert s.index('run_timed "preflight" run_platform_preflight',s.index('main() {')) < s.index('run_timed "переключение runtime" csqtt_cleanup',s.index('main() {'))
assert 'ELF architecture mismatch' in s
assert 'elf_machine="$(od -An -tu2 -j18 -N2' in s
assert s.index('ELF architecture mismatch') < s.index('chmod 0755 "$UPLOAD_BINARY"')
assert s.count('CSQTT_DEPLOY_OK')>=2
# The old service boot state must survive automatic rollback. The old
# cutover explicitly disables the service, so merely restarting is not enough.
snapshot=s.split("snapshot_existing_systemd() {",1)[1].split("\nrestore_previous_systemd() {",1)[0]
restore=s.split("restore_previous_systemd() {",1)[1].split("\ncleanup_rollback_snapshot() {",1)[0]
assert 'systemctl is-enabled --quiet csqtt-core' in snapshot
# Incomplete/symlinked prior installation must not enter an unprotected cutover.
assert 'local old_binary=/usr/local/bin/csqtt-core' in snapshot
assert 'local old_unit=/etc/systemd/system/csqtt-core.service' in snapshot
assert 'Previous CSQTT-Core systemd runtime is incomplete or symlinked' in snapshot
assert snapshot.index('if [ ! -e "$old_binary" ]') < snapshot.index('ROLLBACK_DIR="$(mktemp')
assert snapshot.index('if [ ! -f "$old_binary" ]') < snapshot.index('ROLLBACK_DIR="$(mktemp')
assert 'ROLLBACK_WAS_ENABLED=1' in snapshot
assert 'if [ "$ROLLBACK_WAS_ENABLED" -eq 1 ]; then' in restore
assert 'systemctl enable csqtt-core' in restore
assert 'systemctl disable csqtt-core' in restore
assert restore.index('systemctl enable csqtt-core') < restore.index('systemctl restart csqtt-core')
# Candidate-owned optional files absent before upgrade must be removed on rollback.
for owned_file in ('csqtt-core.env','deploy-overrides.json','web_cert.pem','web_key.pem','letsencrypt-ip.env'):
 assert owned_file in restore, owned_file
assert 'rm -f -- "$CSQTT_CONFIG_DIR/$rollback_managed_file"' in restore
# Newly introduced helper scripts must not survive a failed cutover.
assert "for rollback_helper in network-up.sh tun-recover.sh docker-prereq.sh" in restore
assert 'rm -f -- "/usr/local/lib/csqtt-core/$rollback_helper"' in restore
assert '[ ! -L /usr/local/lib/csqtt-core ] || return 1' in restore
assert restore.index('rm -f -- "/usr/local/lib/csqtt-core/$rollback_helper"') < restore.index('cp -a "$ROLLBACK_DIR/helpers/."')
assert "[ ! -L \"$CSQTT_CONFIG_DIR\" ] || return 1" in restore
assert restore.index('if [ -d "$CSQTT_CONFIG_DIR" ]; then') < restore.index('if [ -d "$ROLLBACK_DIR/config" ]; then')
assert restore.index('rm -f -- "$CSQTT_CONFIG_DIR/$rollback_managed_file"') < restore.index('cp -a "$ROLLBACK_DIR/config/."')
# An unsuccessful rollback must remain armed and keep the private snapshot.
assert restore.index('ROLLBACK_ARMED=0') > restore.index('systemctl restart csqtt-core')
retention=s.split("cleanup_rollback_snapshot() {",1)[1].split("\nvalidate_port() {",1)[0]
assert 'if [ "$ROLLBACK_ARMED" -eq 1 ]; then' in retention
assert 'finish_deploy_rollback() {' in retention
assert 'recovery files preserved at $ROLLBACK_DIR' in retention
assert s.count('    finish_deploy_rollback || true')==2
print('CSQTT 2.5 Core installer isolation/static syntax PASS')
