#!/usr/bin/env python3
from pathlib import Path
g=Path('adapt/headless_main.go').read_text()
h=Path('adapt/csqtt-panel-preview.html').read_text()
assert 'csqttUpdateRepo = "Xenofan-git/CSQTT-Core"' in g
assert 'csqttUpdateAssetName = "csqtt-core-headless-linux-arm64"' in g
x=g.split('mux.HandleFunc("/updates/core",',1)[1].split('mux.HandleFunc("/updates/core/check",',1)[0]
assert 'app.UpdateCore()' not in x
assert 'app.UpdateCoreAndWait()' not in x
assert 'http.StatusGone' in x
assert 'prepareVerifiedCSQTTCoreUpdate' in x
assert 'privateClient(r)' in x
for x in ['csqttApprovedAsset','csqttAssetURL','sha256:','csqttValidateCandidate',
          'csqttDownloadAsset','main.buildCommit=','/compare/','systemd-run',
          'rollback','BEFORE_ROUTE','hydraroute-core.service']:
    assert x in g,x
assert 'id="updateCore" disabled' in h
assert 'update.disabled=!v.canInstall' in h
assert 'Xenofan-git/CSQTT-Core' in h
assert Path('adapt/CSQTT-CORE-UPDATE-RELEASE-CONTRACT.md').exists()
print('CSQTT_CORE_SAFE_UPDATER_CONTRACT_PASS')
