#!/usr/bin/env python3
"""Verify integration changes only PANEL_HTML and preserves Rust auth/API source."""
import os
import subprocess
import tempfile
from pathlib import Path

root = Path(__file__).parent
upstream = Path(os.environ['CSQTT_UPSTREAM_WEB_PANEL'])
original = upstream.read_text()
with tempfile.TemporaryDirectory() as temp:
    subprocess.run(['python3', str(root/'integrate-original-server-panel.py'), '--upstream', str(upstream), '--output-dir', temp], check=True)
    patched = (Path(temp)/'web_panel.rs').read_text()
    asset = (Path(temp)/'csqtt-core-panel.html').read_text()
    start = 'const PANEL_HTML: &str = r##"'
    before, rest = original.split(start, 1)
    _, after = rest.split('"##;', 1)
    assert patched == before + 'const PANEL_HTML: &str = include_str!("csqtt-core-panel.html");' + after
    assert asset == (root/'csqtt-server-panel-upstream-250.html').read_text()
    assert 'async fn api_export_get' in patched and 'async fn api_import_post' in patched
    assert 'auth_middleware' in patched and 'fn render_root' in patched
    assert 'app.web_secret_path' in patched
    assert (Path(temp)/'csqtt-core-panel.html').exists()
print('Backend Rust source integration: auth/API/secret-path unchanged; only panel asset replaced')
