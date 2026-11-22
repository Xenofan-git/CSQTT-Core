#!/usr/bin/env python3
"""Verify one specific upstream 2.5.3 SSH vs peer/web port logic improvement.

No emulator, Android SDK, VPS or network; exact real Kotlin source
contract with negative anchor and double-application checks.
"""
import argparse
from hashlib import sha256
import importlib.util
from pathlib import Path
src=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location("ports",src/"patch-csqttr-253-android-independent-ssh-port-250.py")
p=importlib.util.module_from_spec(spec);spec.loader.exec_module(p)
def must_fail(func,text):
    try:func(text)
    except ValueError:return
    raise AssertionError('source guard accepted invalid/double patch')

ap=argparse.ArgumentParser()
ap.add_argument('--source-root',type=Path,required=True)
args=ap.parse_args()
base=args.source_root/'app/src/main/java/com/csqtt/client/ui'
tab=(base/'DeployTab.kt').read_text(encoding='utf8')
screen=(base/'DeployScreen.kt').read_text(encoding='utf8')
assert sha256(tab.encode()).hexdigest()==p.HOSTKEY_PATCHED_TAB_SHA
assert sha256(screen.encode()).hexdigest()==p.ORIGINAL_SCREEN_SHA
print('PINNED_ANDROID_250_POST_HOSTKEY_SOURCE_PASS')
t=p.tab(tab)
s=p.screen(screen)
assert sha256(t.encode()).hexdigest()=='e75b35a9eab728f614c12a80c1bccdcad8879a58c8e0b8ad68ea5a02e90289d8'
assert sha256(s.encode()).hexdigest()=='444a6d4117e75f9a35d73e1d483e63130e8165d059013e057a1d852327620f0b'
print('EXACT_MINIMAL_SSH_PORT_SOURCE_TRANSFORM_PASS')
assert 'isValidPort(sshPort) && (!manualPorts || listOf(peerPort, webPort).all(::isValidPort))' in t
assert 'val verifiedSsh = sshPort.toIntOrNull()?.takeIf { it in 1..65535 }' in t
assert t.count('ssh = verifiedSsh')==2
assert 'sshPort = CsqttConstants.Network.DEFAULT_SSH_PORT.toString()' not in t.split('is DeployAction.ManualPortsChanged -> {',1)[1].split('is DeployAction.SshKeysModeChanged',1)[0]
assert 'portsEdited = true' in t.split('is DeployAction.ManualPortsChanged -> {',1)[1].split('is DeployAction.SshKeysModeChanged',1)[0]
assert 'peerPort = CsqttConstants.Network.DEFAULT_SERVER_PEER_PORT.toString()' in t
assert 'webPort = CsqttConstants.Network.DEFAULT_SERVER_WEB_PORT.toString()' in t
print('SSH_PORT_SURVIVES_MANUAL_PEER_WEB_TOGGLE_PASS')
assert s.count('IndependentSshPortField(state = state, onAction = onAction)')==2
assert s.count('value = state.sshPort')==1
assert s.count('onAction(DeployAction.SshPortChanged(it))')==1
assert s.count('value = state.peerPort')==1
assert s.count('value = state.webPort')==1
assert 'modifier = Modifier.fillMaxWidth()' in s[s.index('private fun IndependentSshPortField'):s.index('private fun ManualPortFields')]
print('ORIGINAL_THEME_VISIBLE_SSH_IN_PASSWORD_AND_KEY_MODES_PASS')
must_fail(p.tab,t)
must_fail(p.screen,s)
must_fail(p.tab,tab.replace('get() = !manualPorts || listOf(sshPort, peerPort, webPort).all(::isValidPort)','get() = true'))
must_fail(p.screen,screen.replace('visible = state.manualPorts','visible = !state.manualPorts'))
print('REAPPLICATION_AND_UNKNOWN_LAYOUT_FAIL_CLOSED_PASS')
print('CSQTTR_253_ANDROID_INDEPENDENT_SSH_PORT_5_GROUPS_PASS')
