#!/usr/bin/env python3
"""Only backport the 2.5.3 independent SSH port control to original Android 2.5.0.

Run AFTER the checksum-pinned SSH host-key confirmation adapter.
Modifies only two original-theme 2.5.0 Kotlin UI files, never touches
ports 46000/46002 routing nor saved credentials or profile model.
"""
import argparse
import hashlib
from pathlib import Path

HOSTKEY_PATCHED_TAB_SHA = '88cad066b94110f139a1a8774134a663bbc528e9d8e8e1d5da9ceee557a03748'
ORIGINAL_SCREEN_SHA = 'e638a850d2bf0b1a04d3601307451e35243cfcbd58831335131d8bf3daceaa14'

def rep(s, old, new, label, n=1):
    if s.count(old)!=n:
        raise ValueError(f"{label}: expected {n} original anchors, got {s.count(old)}")
    return s.replace(old,new,n)

def tab(s):
    s=rep(s,
'''        get() = !manualPorts || listOf(sshPort, peerPort, webPort).all(::isValidPort)''',
'''        get() = isValidPort(sshPort) && (!manualPorts || listOf(peerPort, webPort).all(::isValidPort))''',
'validate SSH port independently of manual peer/web mode')
    s=rep(s,
'''): EffectiveServerPorts {
    if (!manualPorts) {''',
'''): EffectiveServerPorts {
    // Custom SSH always applies, even with default CSQTT tunnel/web ports.
    val verifiedSsh = sshPort.toIntOrNull()?.takeIf { it in 1..65535 }
        ?: CsqttConstants.Network.DEFAULT_SSH_PORT
    if (!manualPorts) {''',
'decouple SSH port selection from server port toggle')
    s=rep(s,
'''            ssh = CsqttConstants.Network.DEFAULT_SSH_PORT,
            peer = CsqttConstants.Network.DEFAULT_SERVER_PEER_PORT,''',
'''            ssh = verifiedSsh,
            peer = CsqttConstants.Network.DEFAULT_SERVER_PEER_PORT,''',
'nonmanual SSH value')
    s=rep(s,
'''        ssh = sshPort.toIntOrNull()?.takeIf { it in 1..65535 }
            ?: CsqttConstants.Network.DEFAULT_SSH_PORT,
        peer = peerPort.toIntOrNull()''',
'''        ssh = verifiedSsh,
        peer = peerPort.toIntOrNull()''',
'manual SSH value')
    s=rep(s,
'''                is DeployAction.ManualPortsChanged -> {
                    if (!action.enabled) {
                        sshPort = CsqttConstants.Network.DEFAULT_SSH_PORT.toString()
                        peerPort = CsqttConstants.Network.DEFAULT_SERVER_PEER_PORT.toString()
                        webPort = CsqttConstants.Network.DEFAULT_SERVER_WEB_PORT.toString()
                        portsEdited = false
                    }
                    scope.launch { settingsStore.saveManualPortsEnabled(action.enabled) }
                }''',
'''                is DeployAction.ManualPortsChanged -> {
                    if (!action.enabled) {
                        // Manual toggles affect CSQTT PEER/WEB only; preserve
                        // the independent SSH port selected for this VPS.
                        peerPort = CsqttConstants.Network.DEFAULT_SERVER_PEER_PORT.toString()
                        webPort = CsqttConstants.Network.DEFAULT_SERVER_WEB_PORT.toString()
                        portsEdited = true
                    }
                    scope.launch { settingsStore.saveManualPortsEnabled(action.enabled) }
                }''',
'preserve SSH port when manual peer/web toggles off')
    return s

def screen(s):
    s=rep(s,
'''                        AnimatedVisibility(
                            visible = state.manualPorts,''',
'''                        IndependentSshPortField(state = state, onAction = onAction)
                        AnimatedVisibility(
                            visible = state.manualPorts,''',
'show SSH port for key/password modes regardless of manual',n=2)
    s=rep(s,
'''@Composable
private fun ManualPortFields(''',
'''@Composable
private fun IndependentSshPortField(
    state: DeployUiState,
    onAction: (DeployAction) -> Unit,
) {
    DeployTextField(
        value = state.sshPort,
        onValueChange = { onAction(DeployAction.SshPortChanged(it)) },
        label = "SSH-порт",
        placeholder = "22",
        enabled = !state.isDeploying,
        isError = state.showValidation && !isValidPort(state.sshPort),
        keyboardType = KeyboardType.Number,
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun ManualPortFields(''',
'new independent field using existing approved components')
    s=rep(s,
'''        DeployTextField(
            value = state.sshPort,
            onValueChange = { onAction(DeployAction.SshPortChanged(it)) },
            label = "SSH-порт",
            placeholder = "22",
            enabled = !state.isDeploying,
            isError = state.showValidation && !isValidPort(state.sshPort),
            keyboardType = KeyboardType.Number,
            modifier = Modifier.weight(1f),
        )
''','',
'remove redundant SSH from manual peer/web pair')
    return s

def main():
    p=argparse.ArgumentParser()
    p.add_argument('--source-root',type=Path,required=True)
    p.add_argument('--check-only',action='store_true')
    a=p.parse_args()
    base=a.source_root.resolve()/'app/src/main/java/com/csqtt/client/ui'
    tab_file=base/'DeployTab.kt'
    screen_file=base/'DeployScreen.kt'
    a0=tab_file.read_text(encoding='utf8')
    b0=screen_file.read_text(encoding='utf8')
    if hashlib.sha256(a0.encode()).hexdigest()!=HOSTKEY_PATCHED_TAB_SHA:
        raise ValueError('DeployTab is not pinned post-hostkey-patch original 2.5.0')
    if hashlib.sha256(b0.encode()).hexdigest()!=ORIGINAL_SCREEN_SHA:
        raise ValueError('DeployScreen is not pinned original 2.5.0')
    a1,b1=tab(a0),screen(b0)
    assert a0!=a1 and b0!=b1
    if not a.check_only:
        tab_file.write_text(a1,encoding='utf8')
        screen_file.write_text(b1,encoding='utf8')
    print('INDEPENDENT_SSH_PORT_253_'+('SOURCE_ANCHORS_PASS' if a.check_only else 'PATCHED'))
    for p,s in ((tab_file,a1),(screen_file,b1)):
        print(p.name,hashlib.sha256(s.encode()).hexdigest())

if __name__=='__main__':main()
