#!/usr/bin/env python3
"""Pure-source regression: SSH mismatch refusal stays intact, trust is explicitly gated.

No Android SDK, SSH connection, password file, real host key or network.
Optional --source-root checks the actual pinned CSQTT v2.5.0 files.
"""
import argparse
import hashlib
import importlib.util
from pathlib import Path

root=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location("host_patch",root/"patch-csqttr-253-android-ssh-change-250.py")
p=importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)

def rejects(call,arg):
    try: call(arg)
    except ValueError: return
    raise AssertionError("unsafe/ambiguous anchor unexpectedly accepted")

h='''package com.csqtt.client
    fun clear(context: Context, host: String, port: Int) {
    }
            if (pinned == presented) {
                return true
            }
            DeployManager.writeError(
                "mismatch"
            )
            return false
'''
u='''package com.csqtt.client.ui
import com.csqtt.client.SettingsStore
import androidx.compose.ui.text.style.TextOverflow
    var showDockerInfoDialog by rememberSaveable(savedSettings.profile) { mutableStateOf(false) }
    fun startDeploy() {
       // original
    }
                    val message = DeployManager.lastResult.value.ifBlank { "Ошибка установки" }
                    scope.launch { snackbarHostState.showSnackbar(message) }
                DeployManager.stopDeploy(message)
                scope.launch { snackbarHostState.showSnackbar(message) }
                    val message = DeployManager.lastResult.value.ifBlank { "Ошибка удаления" }
                    scope.launch { snackbarHostState.showSnackbar(message) }
                DeployManager.stopDeploy(message)
                scope.launch { snackbarHostState.showSnackbar(message) }
    if (showSecretsDialog) {
'''
hp=p.host(h)
assert 'lastChange = KeyChange(hostname, port, pinned, presented)' in hp
assert hp.count('return false')>=2
assert 'if (pinned == presented)' in hp
assert 'if (prefs.getString(entry, null) != change.pinned) return false' in hp
assert 'prefs.edit().putString(entry, change.presented).apply()' in hp
assert 'if (entryKey(change.host, change.port) != entryKey(host, port)) return null' in hp
assert 'if (change.pinned == change.presented) return false' in hp
print("HOST_KEY_REJECT_OLD_TOFU_PRESERVED_PASS")

up=p.ui(u)
assert up.count('showChangedHostKeyIfAny(host, effectiveSshPort)')==4
assert 'pendingHostKey = change' in up
assert 'SshHostKeyStore.trust(context, change)' in up
assert 'новый отпечаток по независимому каналу' in up
assert 'Повторите действие SSH вручную.' in up
assert up.count('if (!showChangedHostKeyIfAny(host, effectiveSshPort))')==4
assert 'startDeploy()' in up
# All new approval is in the actual dialog event, never in an effect/catch.
assert up.index('onClick = {',up.index('pendingHostKey?.let')) < up.index('SshHostKeyStore.trust(context, change)')
assert up.index('SshHostKeyStore.trust(context, change)') > up.index('Text("Новый: "')
print("UI_INSTALL_UNINSTALL_ERROR_AND_CONFIRMATION_PASS")

rejects(p.host,hp)
rejects(p.ui,up)
print("DOUBLE_APPLICATION_FAILS_CLOSED_PASS")
rejects(p.host,h.replace('if (pinned == presented)', 'if (pinned == changed)'))
rejects(p.ui,u.replace('    fun startDeploy() {','    fun executeUnknown() {'))
print("MISSING_REQUIRED_SOURCE_ANCHOR_REFUSED_PASS")
rejects(p.host,h+h)
rejects(p.ui,u+u)
print("DUPLICATE_REQUIRED_SOURCE_ANCHOR_REFUSED_PASS")

ap=argparse.ArgumentParser()
ap.add_argument("--source-root",type=Path)
opts=ap.parse_args()
if opts.source_root:
    source=opts.source_root/"app/src/main/java/com/csqtt/client"
    old_host=(source/"SshHostKeyStore.kt").read_text()
    old_ui=(source/"ui/DeployTab.kt").read_text()
    assert hashlib.sha256(old_host.encode()).hexdigest()==p.HOST_SOURCE_SHA
    assert hashlib.sha256(old_ui.encode()).hexdigest()==p.UI_SOURCE_SHA
    assert hashlib.sha256(p.host(old_host).encode()).hexdigest()=="ce9b4c3a2bbdb348b3f8b5f89a270c2eaaeba364783db3a8f975b971bccd46bc"
    assert hashlib.sha256(p.ui(old_ui).encode()).hexdigest()=="88cad066b94110f139a1a8774134a663bbc528e9d8e8e1d5da9ceee557a03748"
    print("REAL_PINNED_250_SOURCE_HASH_AND_POSTPATCH_PASS")
print("CSQTTR_253_ANDROID_SSH_CHANGE_SOURCE_5_GROUPS_PASS")
