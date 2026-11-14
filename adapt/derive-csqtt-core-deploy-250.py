#!/usr/bin/env python3
"""Derive isolated Core installer from checksum-pinned original CSQTT 2.5.0.
No install execution. Preserve upstream flow; refuse unknown upstream changes.
"""
import hashlib
import re
import subprocess
from pathlib import Path
import argparse
p=argparse.ArgumentParser();p.add_argument('--upstream-root',required=True,type=Path);a=p.parse_args()
src=(a.upstream_root/'app/src/main/assets/deploy.sh').read_bytes()
assert hashlib.sha256(src).hexdigest()=='9a82d4ab5f12b0677b4e11be14ca892fb16c0a771eddb7ed5485d8957e9b9895'
s=src.decode()
# Whole-token replacements include quoted heredocs, Docker definitions and owner detection.
# The resulting script MUST pass static isolation audit before any deployment.
for old,new in [
 ('10.66.67.0/24','10.67.69.0/24'),
 ('/etc/csqtt','/etc/csqtt-core'),
 ('/usr/local/lib/csqtt','/usr/local/lib/csqtt-core'),
 ('/usr/local/bin/csqtt','/usr/local/bin/csqtt-core'),
 ('/opt/csqtt-certbot','/opt/csqtt-core-certbot'),
 ('/var/log/csqtt-install.log','/var/log/csqtt-core-install.log'),
 ('/run/csqtt','/run/csqtt-core'),
 ('/tmp/.csqtt-upload','/tmp/.csqtt-core-upload'),
 ('csqtt1','csqtt-core0'),
 ('CSQTT_MANAGED','CSQTT_CORE_MANAGED'),
 ('99-csqtt','99-csqtt-core'),
 ('csqtt-letsencrypt','csqtt-core-letsencrypt'),
 ('csqtt-docker-prereq','csqtt-core-docker-prereq'),
 ('csqtt-docker-','csqtt-core-docker-'),
 ('csqtt:2.5.0','csqtt-core:2.5.0'),
 ('CSQTT_PEER_PORT:-46000','CSQTT_PEER_PORT:-47000'),
 ('CSQTT_WEB_PORT:-46002','CSQTT_WEB_PORT:-47002'),
]:
 assert old in s,old
 s=s.replace(old,new)
# Namespaced service/container and standalone binary references; do not modify CSQTT_* protocol fields.
s=re.sub(r'(?<![A-Za-z0-9_-])csqtt(?![A-Za-z0-9_-])','csqtt-core',s)
# Original cleanup targets shared legacy resources. Never touch them in Core.
legacy_start=s.index('cleanup_legacy_proxy_interfaces() {')
legacy_end=s.index('\n}\n',legacy_start)+2
s=s[:legacy_start]+"cleanup_legacy_proxy_interfaces() {\n    : # Core owns no legacy csqtp* interfaces; do not delete another install\n}"+s[legacy_end:]
setup_start=s.index('setup_csqtt_binary() {')
setup_end=s.index('\n}\n',setup_start)+2
section=s[setup_start:setup_end]
first=section.index('    rm -f /usr/local/bin/hev-socks5-tunnel')
last=section.index('    mkdir -p "$CSQTT_CONFIG_DIR"',first)
section=section[:first]+'    # Preserve unrelated legacy csqtt-cascade and hev-socks5 resources.\n\n' +section[last:]
s=s[:setup_start]+section+s[setup_end:]
s=s.replace('    rm -f /usr/local/bin/hev-socks5-tunnel /usr/local/bin/hev-socks5-server\n','')
# Disable host-global sysctl/GRUB mutation: Core must not rewrite the host network policy.
start=s.index('setup_sysctl() {')
end=s.index('\n}\n',start)+2
s=s[:start]+"setup_sysctl() {\n    [ \"$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || echo 0)\" = 1 ] || die \"IPv4 forwarding must be configured by host administrator; Core will not change host sysctl\"\n}"+s[end:]
start=s.index('tune_cpu_mitigations() {')
end=s.index('\n}\n',start)+2
s=s[:start]+"tune_cpu_mitigations() {\n    : # Never disable CPU security mitigations or edit GRUB\n}"+s[end:]
# Only remove Core-tagged netfilter rules; upstream legacy tags may belong to another install.
s=s.replace('for marker in \"$IPT_COMMENT\" CSQTT_MIRRORED CSQTT_TPROXY CSQTT_LOCAL_SOCKS CSQTT_LOCAL_SOCKS_MARK CSQTT_SOCKS CSQTT_CASCADE_NO_QUIC; do', 'for marker in \"$IPT_COMMENT\"; do')
# Avoid a global sysctl reload when uninstalling Core.
s=s.replace('    sysctl --system >/dev/null 2>&1 || true\n','')
# Embedded network helper must not change global forwarding either.
s=s.replace('[ -w /proc/sys/net/ipv4/ip_forward ] && echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true', ': # host forwarding preconfigured')
# Fail closed before any package install, network mutation or cutover.
# Core is still in a controlled testing phase; explicit gate is required.
s=s.replace('    check_root\n    mkdir -p "$(dirname "$LOG_FILE")"', '    check_root\n    if [ "$action" = install ] || [ "$action" = --install ] || [ "$action" = -i ] || [ "$action" = uninstall ] || [ "$action" = --uninstall ] || [ "$action" = -u ]; then\n        [ "${CSQTT_CORE_ALLOW_MUTATION:-}" = "isolated-lab-only" ] || {\n            printf "%s\\n" "REFUSED: CSQTT-Core install/uninstall requires CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only" >&2\n            exit 42\n        }\n    fi\n    if [ "$action" = status ] || [ "$action" = --status ] || [ "$action" = -s ]; then\n        do_status\n        return\n    fi\n    mkdir -p "$(dirname "$LOG_FILE")"'  )
# DB filenames must stay csqtt.db because original Rust binary uses that basename.
s=s.replace('csqtt-core.db','csqtt.db').replace('csqtt-core.db-wal','csqtt.db-wal').replace('csqtt-core.db-shm','csqtt.db-shm')
# Explicit human-review safety gate: do not silently claim deployment-ready.
output=Path('server-assets/deploy-csqtt-core-250.sh')
output.write_text(s)
result=subprocess.run(['bash','-n',str(output)],capture_output=True,text=True)
assert result.returncode==0,result.stderr
print('Generated',output,'SHA256',hashlib.sha256(output.read_bytes()).hexdigest())
