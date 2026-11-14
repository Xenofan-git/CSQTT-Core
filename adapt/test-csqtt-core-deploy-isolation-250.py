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
print('CSQTT 2.5 Core installer isolation/static syntax PASS')
