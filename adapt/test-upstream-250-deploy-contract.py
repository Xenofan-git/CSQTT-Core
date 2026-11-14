#!/usr/bin/env python3
"""Offline safety/feature audit of pinned upstream CSQTT v2.5.0 Deploy.
Does not execute installation, uninstall, SSH, or system mutations.
"""
import argparse
import hashlib
import re
import subprocess
from pathlib import Path

p=argparse.ArgumentParser()
p.add_argument('--upstream-root',type=Path,required=True)
a=p.parse_args()
root=a.upstream_root
script=root/'app/src/main/assets/deploy.sh'
ui=root/'app/src/main/java/com/csqtt/client/ui/DeployTab.kt'
manager=root/'app/src/main/java/com/csqtt/client/DeployManager.kt'
assert script.is_file() and ui.is_file() and manager.is_file()
s=script.read_text(); u=ui.read_text(); m=manager.read_text()
assert hashlib.sha256(script.read_bytes()).hexdigest() == '9a82d4ab5f12b0677b4e11be14ca892fb16c0a771eddb7ed5485d8957e9b9895', 'upstream script checksum changed'
assert 'readonly SCRIPT_VERSION="2.5.0"' in s
assert subprocess.run(['bash','-n',str(script)],capture_output=True).returncode==0
for marker in ('run_platform_preflight()', 'setup_csqtt_environment()', 'setup_csqtt_service()',
               'verify_web_panel()', 'do_uninstall()', 'CSQTT_WEB_SECRET_PATH',
               'CSQTT_PEER_PORT:-46000', 'CSQTT_WEB_PORT:-46002',
               'letsencrypt', 'CSQTT_CONFIG_DIR="/etc/csqtt"'):
    assert marker.lower() in s.lower(),f'upstream deploy missing: {marker}'
for marker in ('secretPathEnabled','secretPathUuid','sshKeysMode','sshPort','manualPorts'):
    assert marker in u,f'original Deploy UI missing: {marker}'
for marker in ('startDeploy()', 'stopDeploy(', 'activeSession'):
    assert marker in m,f'original DeployManager missing: {marker}'
# Verify original install entry point retains the preflight before mutation.
branch=s.split('install|--install|-i)',1)[1]
assert 'run_platform_preflight' in branch
print('Original CSQTT 2.5.0 Deploy static contract PASS')
print('Pinned script SHA256:',hashlib.sha256(script.read_bytes()).hexdigest())
print('NOTICE: Original default paths/ports overlap with existing CSQTT deployments.')
print('NOTICE: No live SSH deploy or install was performed.')
