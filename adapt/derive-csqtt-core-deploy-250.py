#!/usr/bin/env python3
"""Derive isolated Core installer from checksum-pinned original CSQTT 2.5.0.
No install execution. Preserve upstream flow; refuse unknown upstream changes.
"""
import hashlib
import re
import subprocess
from pathlib import Path
import argparse
p=argparse.ArgumentParser()
p.add_argument('--upstream-root',required=True,type=Path)
p.add_argument('--candidate-output',type=Path,
               help='Generate the unapproved candidate into a NEW, separate file; never replace the pinned installer')
a=p.parse_args()
if a.candidate_output is not None:
    if a.candidate_output.exists() or a.candidate_output.is_symlink():
        p.error('candidate output must be a new path')
    if a.candidate_output.name == 'deploy-csqtt-core-250.sh':
        p.error('candidate output must have a distinct filename')
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
# Never mutate shared WAN sysctls, default routes, netplan or global sysctl from Core.
s=s.replace('    sysctl -w "net.ipv4.conf.${iface}.rp_filter=2" >>"$LOG_FILE" 2>&1 || true\n    sysctl -w "net.ipv6.conf.${iface}.disable_ipv6=0" >>"$LOG_FILE" 2>&1 || true\n    sysctl -w "net.ipv6.conf.${iface}.accept_ra=2" >>"$LOG_FILE" 2>&1 || true\n    sysctl -w "net.ipv6.conf.${iface}.forwarding=1" >>"$LOG_FILE" 2>&1 || true\n    command -v netplan >/dev/null 2>&1 && netplan apply >>"$LOG_FILE" 2>&1 || true', '    : # Preserve host WAN sysctl and netplan policy')
start=s.index('    local gw_ip\n    gw_ip=',s.index('setup_nat_and_firewall() {'))
end=s.index('    fw_add_input_udp "$PEER_PORT"',start)
s=s[:start]+s[end:]
for line in ('[ -w /proc/sys/net/ipv6/conf/all/forwarding ] && echo 1 > /proc/sys/net/ipv6/conf/all/forwarding 2>/dev/null || true','[ -w /proc/sys/net/ipv4/conf/all/rp_filter ] && echo 2 > /proc/sys/net/ipv4/conf/all/rp_filter 2>/dev/null || true','[ -w /proc/sys/net/ipv4/conf/default/rp_filter ] && echo 2 > /proc/sys/net/ipv4/conf/default/rp_filter 2>/dev/null || true','[ -w /proc/sys/net/ipv4/tcp_notsent_lowat ] && echo 131072 > /proc/sys/net/ipv4/tcp_notsent_lowat 2>/dev/null || true'):
 assert line in s,line
 s=s.replace(line,': # preserve host-global network tuning')
# Preserve WAN routes and interface sysctl inside the generated network-up helper.
s=s.replace('[ -w "/proc/sys/net/ipv4/conf/\\$WAN_IFACE/rp_filter" ] && echo 2 > "/proc/sys/net/ipv4/conf/\\$WAN_IFACE/rp_filter" 2>/dev/null || true', ': # WAN rp_filter belongs to host')
start=s.index('# Для /32 сетей с onlink-шлюзом обеспечиваем явный host route к шлюзу')
end=s.index('if ip link show "\\$CSQTT_IFACE"',start)
s=s[:start]+': # Do not add or replace host routes\n\n'+s[end:]
# Fail closed before any package install, network mutation or cutover.
# Core is still in a controlled testing phase; explicit gate is required.
s=s.replace('    check_root\n    mkdir -p "$(dirname "$LOG_FILE")"', '    check_root\n    if [ "$action" = install ] || [ "$action" = --install ] || [ "$action" = -i ] || [ "$action" = uninstall ] || [ "$action" = --uninstall ] || [ "$action" = -u ]; then\n        [ "${CSQTT_CORE_ALLOW_MUTATION:-}" = "isolated-lab-only" ] || {\n            printf "%s\\n" "REFUSED: CSQTT-Core install/uninstall requires CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only" >&2\n            exit 42\n        }\n    fi\n    if [ "$action" = status ] || [ "$action" = --status ] || [ "$action" = -s ]; then\n        do_status\n        return\n    fi\n    mkdir -p "$(dirname "$LOG_FILE")"'  )
# Verify TUN/kernel compatibility BEFORE removing an existing Core runtime.
s=s.replace('            DEPLOY_PHASE="cutover"\n            run_timed "переключение runtime" csqtt_cleanup\n            run_timed "preflight" run_platform_preflight','            run_timed "preflight" run_platform_preflight\n            DEPLOY_PHASE="cutover"\n            run_timed "переключение runtime" csqtt_cleanup')
# Reject foreign-architecture payloads before touching any existing Core runtime.
# ELF e_machine: x86_64=62, aarch64=183, ARM=40 (little-endian Linux).
needle='    chmod 0755 "$UPLOAD_BINARY" || die "Не удалось подготовить загруженный бинарник csqtt-core" "$EXIT_PREFLIGHT_FAILED"'
assert needle in s
s=s.replace(needle, '    local elf_magic elf_machine expected_machine\n    elf_magic="$(od -An -tx1 -N4 "$UPLOAD_BINARY" | tr -d \' \\n\')"\n    [ "$elf_magic" = 7f454c46 ] || die "Payload is not an ELF binary" "$EXIT_PREFLIGHT_FAILED"\n    elf_machine="$(od -An -tu2 -j18 -N2 "$UPLOAD_BINARY" | tr -d \' \\n\')"\n    case "$(uname -m)" in\n        aarch64|arm64) expected_machine=183 ;;\n        x86_64|amd64) expected_machine=62 ;;\n        armv7l|armv7|armhf) expected_machine=40 ;;\n        *) die "Unsupported host architecture" "$EXIT_PREFLIGHT_FAILED" ;;\n    esac\n    [ "$elf_machine" = "$expected_machine" ] || die "ELF architecture mismatch: host expects $expected_machine, got $elf_machine" "$EXIT_PREFLIGHT_FAILED"\n' + '\n'+needle)
# DB filenames must stay csqtt.db because original Rust binary uses that basename.
s=s.replace('csqtt-core.db','csqtt.db').replace('csqtt-core.db-wal','csqtt.db-wal').replace('csqtt-core.db-shm','csqtt.db-shm')
# Explicit human-review safety gate: do not silently claim deployment-ready.
# Preserve independently reviewed 2.5.3 port-owner fail-closed logic when
# rebuilding the derived installer from pinned original 2.5.0 sources.
import importlib.util
guard_path = Path(__file__).with_name('patch-csqttr-253-selective-deploy-ports.py')
spec=importlib.util.spec_from_file_location('csqtt_selective_port_guard',guard_path)
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
s=module.patch(s)
# The read-only CSQTTR 2.5.3-style server inventory is also part
# of the canonical regeneration path; never lose it on re-derive.
inventory_path=Path(__file__).with_name('patch-csqttr-253-selective-deploy-inventory.py')
inventory_spec=importlib.util.spec_from_file_location('csqtt_selective_inventory',inventory_path)
inventory=importlib.util.module_from_spec(inventory_spec)
inventory_spec.loader.exec_module(inventory)
s=inventory.patch(s)
# Existing verified rollback improvements are NOT in the original 2.5.0
# pinned upstream. Reapply only the exact audited overlay in a temp dir.
# SHA checks fail closed if source, selective guards or rollback ever drift.
import tempfile
SOURCE_BEFORE_ROLLBACK='8a3b1878dc65549937f90190ee459d22ce2c2296c52b8db7dfac8f42b2804605'
RELEASE_WITH_ROLLBACK='18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68'
assert hashlib.sha256(s.encode()).hexdigest()==SOURCE_BEFORE_ROLLBACK, 'derived installer drift; refusing rollback overlay'
overlay=Path(__file__).with_name('csqtt-core-existing-rollback-preservation.patch')
with tempfile.TemporaryDirectory(prefix='csqtt-core-rollback-replay-') as tmp:
    private=Path(tmp)/'server-assets'
    private.mkdir()
    candidate=private/'deploy-csqtt-core-250.sh'
    candidate.write_text(s)
    result=subprocess.run(['patch','--batch','--fuzz=0','--forward',
                           '--no-backup-if-mismatch','-p1','-i',str(overlay)],
                          cwd=tmp,capture_output=True,text=True,timeout=12)
    assert result.returncode==0, 'rollback overlay refused: '+result.stderr[-1500:]
    s=candidate.read_text()
assert hashlib.sha256(s.encode()).hexdigest()==RELEASE_WITH_ROLLBACK, 'rollback reproduction mismatch; refusing generation'
if a.candidate_output is not None:
    from csqtt_deploy_candidate import candidate_bytes
    data=candidate_bytes(s.encode())
    output=a.candidate_output
    with output.open('xb') as f:
        f.write(data)
else:
    output=Path('server-assets/deploy-csqtt-core-250.sh')
    output.write_text(s)
result=subprocess.run(['bash','-n',str(output)],capture_output=True,text=True)
assert result.returncode==0,result.stderr
print('Generated',output,'SHA256',hashlib.sha256(output.read_bytes()).hexdigest())
