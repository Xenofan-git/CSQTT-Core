#!/usr/bin/env python3
"""Opt-in narrow CSQTTR 2.5.3 SSH host-key confirmation for pinned Android 2.5.0.

Changes only SshHostKeyStore.kt, ui/DeployTab.kt, and adds one focused
dialog file using existing Material3 theme. No deploy/SSH operation run.
A mismatch must still be rejected until a user explicitly approves exactly
the fingerprint the failed connection presented.
"""
import argparse
import hashlib
from pathlib import Path

HOST_SOURCE_SHA = '63134110bf978e1e2b090bf30480681096da447b2c87831e736cf338e6b5f004'
UI_SOURCE_SHA = 'f5a1ebb5f7dc3fa4e7ab294a92285095b9a9b3dc19f0cea227a23a7bc12d7e61'

def unique(src, old, new, name):
    if src.count(old)!=1: raise ValueError(f"{name}: expected 1 source anchor, got {src.count(old)}")
    return src.replace(old,new,1)

def host(s):
    s=unique(s,
'''    fun clear(context: Context, host: String, port: Int) {''',
'''    data class KeyChange(
        val host: String,
        val port: Int,
        val pinned: String,
        val presented: String,
    )

    @Volatile private var lastChange: KeyChange? = null

    fun takeKeyChange(host: String, port: Int): KeyChange? {
        val change = lastChange ?: return null
        if (entryKey(change.host, change.port) != entryKey(host, port)) return null
        lastChange = null
        return change
    }

    // Called ONLY from the visible fingerprint confirmation dialog.
    // Protect against accepting an obsolete mismatch if the key was
    // already changed from another screen/connection in the meantime.
    fun trust(context: Context, change: KeyChange): Boolean {
        if (change.pinned == change.presented) return false
        val prefs = context.applicationContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val entry = entryKey(change.host, change.port)
        if (prefs.getString(entry, null) != change.pinned) return false
        prefs.edit().putString(entry, change.presented).apply()
        if (lastChange == change) lastChange = null
        return true
    }

    fun clear(context: Context, host: String, port: Int) {''',
    'fingerprint confirmation state')
    s=unique(s,
'''            if (pinned == presented) {
                return true
            }
            DeployManager.writeError(''',
'''            if (pinned == presented) {
                // An old warning is stale after the real pinned key returns.
                if (lastChange?.let { entryKey(it.host, it.port) } == entry) lastChange = null
                return true
            }
            lastChange = KeyChange(hostname, port, pinned, presented)
            DeployManager.writeError(''',
    'capture mismatch while continuing to refuse host')
    if 'return false' not in s.split('lastChange = KeyChange(hostname, port, pinned, presented)',1)[1]:
        raise ValueError('mismatch no longer returns false')
    return s

def ui(s):
    s=unique(s,
'''import com.csqtt.client.SettingsStore
''',
'''import com.csqtt.client.SettingsStore
import com.csqtt.client.SshHostKeyStore
''','add imported host key verifier')
    s=unique(s,
'''import androidx.compose.ui.text.style.TextOverflow
''',
'''import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.font.FontFamily
''','existing Material3 text style')
    s=unique(s,
'''    var showDockerInfoDialog by rememberSaveable(savedSettings.profile) { mutableStateOf(false) }
''',
'''    var showDockerInfoDialog by rememberSaveable(savedSettings.profile) { mutableStateOf(false) }
    var pendingHostKey by remember(savedSettings.profile) {
        mutableStateOf<SshHostKeyStore.KeyChange?>(null)
    }
''','profile-scoped pending changed key')
    s=unique(s,
'''    fun startDeploy() {
''',
'''    fun showChangedHostKeyIfAny(target: String, port: Int): Boolean {
        val change = SshHostKeyStore.takeKeyChange(target, port) ?: return false
        scope.launch { pendingHostKey = change }
        return true
    }

    fun startDeploy() {
''','compose host-key notification hook')
    s=unique(s,
'''                    val message = DeployManager.lastResult.value.ifBlank { "Ошибка установки" }
                    scope.launch { snackbarHostState.showSnackbar(message) }''',
'''                    val message = DeployManager.lastResult.value.ifBlank { "Ошибка установки" }
                    if (!showChangedHostKeyIfAny(host, effectiveSshPort)) {
                        scope.launch { snackbarHostState.showSnackbar(message) }
                    }''','installation false return')
    s=unique(s,
'''                    val message = DeployManager.lastResult.value.ifBlank { "Ошибка удаления" }
                    scope.launch { snackbarHostState.showSnackbar(message) }''',
'''                    val message = DeployManager.lastResult.value.ifBlank { "Ошибка удаления" }
                    if (!showChangedHostKeyIfAny(host, effectiveSshPort)) {
                        scope.launch { snackbarHostState.showSnackbar(message) }
                    }''','uninstall false return')
    block='''                DeployManager.stopDeploy(message)
                scope.launch { snackbarHostState.showSnackbar(message) }'''
    if s.count(block)!=2:
        raise ValueError(f"expected 2 exception handlers, found {s.count(block)}")
    s=s.replace(block,
'''                DeployManager.stopDeploy(message)
                if (!showChangedHostKeyIfAny(host, effectiveSshPort)) {
                    scope.launch { snackbarHostState.showSnackbar(message) }
                }''')
    s=unique(s,
'''    if (showSecretsDialog) {
''',
'''    pendingHostKey?.let { change ->
        AlertDialog(
            onDismissRequest = { pendingHostKey = null },
            title = { Text("SSH-ключ сервера изменился") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Сервер " + change.host + ":" + change.port +
                        " предъявил другой ключ. Если вы не переустанавливали VPS, " +
                        "это может означать перехват подключения. Сначала проверьте " +
                        "новый отпечаток по независимому каналу.")
                    Text("Предыдущий: " + change.pinned, fontFamily = FontFamily.Monospace)
                    Text("Новый: " + change.presented, fontFamily = FontFamily.Monospace)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    pendingHostKey = null
                    if (SshHostKeyStore.trust(context, change)) {
                        scope.launch {
                            snackbarHostState.showSnackbar(
                                "Ключ сохранён. Повторите действие SSH вручную."
                            )
                        }
                    } else {
                        scope.launch {
                            snackbarHostState.showSnackbar(
                                "SSH-ключ изменился снова. Проверьте сервер повторно."
                            )
                        }
                    }
                }) { Text("Доверять новому ключу") }
            },
            dismissButton = {
                TextButton(onClick = { pendingHostKey = null }) { Text("Отмена") }
            },
        )
    }

    if (showSecretsDialog) {
''','confirmed fingerprint dialog, no auto-redeploy')
    return s

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument('--source-root',required=True,type=Path)
    ap.add_argument('--check-only',action='store_true')
    args=ap.parse_args()
    root=args.source_root.resolve()/'app/src/main/java/com/csqtt/client'
    key=root/'SshHostKeyStore.kt'
    tab=root/'ui/DeployTab.kt'
    dialog=root/'ui/dialogs'/'HostKeyChangedDialog.kt'
    if dialog.exists(): raise ValueError('unexpected preexisting HostKeyChangedDialog; fail closed')
    originals={key:key.read_text(encoding='utf8'),tab:tab.read_text(encoding='utf8')}
    for file,expected in ((key,HOST_SOURCE_SHA),(tab,UI_SOURCE_SHA)):
        if hashlib.sha256(originals[file].encode()).hexdigest()!=expected:
            raise ValueError('not the checksum-pinned CSQTT 2.5.0 source: '+file.name)
    modified={key:host(originals[key]),tab:ui(originals[tab])}
    if not args.check_only:
        for file,content in modified.items():file.write_text(content,encoding='utf8')
    for file,content in modified.items():
        print(file.name, 'new_sha256='+hashlib.sha256(content.encode()).hexdigest())
    print('CSQTTR_253_ANDROID_SSH_CONFIRM_'+('SOURCE_CHECK_PASS' if args.check_only else 'PATCHED'))

if __name__=='__main__':main()
