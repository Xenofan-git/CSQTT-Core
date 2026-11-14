#!/usr/bin/env python3
"""Inject original v2.5 panel + Core palette into a copy of original Rust backend.

Offline-only. Never writes to the upstream source or touches running services.
The original login/auth middleware, API, and server routing are preserved.
"""
import argparse
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--upstream', type=Path, required=True)
parser.add_argument('--output-dir', type=Path, required=True)
a = parser.parse_args()
source = a.upstream.read_text()
asset = (Path(__file__).parent / 'csqtt-server-panel-upstream-250.html').read_text()
start = 'const PANEL_HTML: &str = r##"'
assert source.count(start) == 1
before, rest = source.split(start, 1)
original, after = rest.split('"##;', 1)
assert original.lstrip().startswith('<!DOCTYPE html>')
assert 'id="apiDocsDlg"' in asset and 'id="importExportDlg"' in asset
assert 'auth_middleware' in before and 'fn render_root' in before
assert 'const LOGIN_HTML: &str' in before
assert '.route("/api/import", post(api_import_post))' in before
assert 'x-csqtt-stealth-rst' in before
assert '"##;' not in asset
# include_str relative to web_panel.rs, placed in same output directory.
patched = before + 'const PANEL_HTML: &str = include_str!("csqtt-core-panel.html");' + after
out = a.output_dir
out.mkdir(parents=True, exist_ok=True)
(out / 'web_panel.rs').write_text(patched)
(out / 'csqtt-core-panel.html').write_text(asset)
assert patched.count('include_str!("csqtt-core-panel.html")') == 1
assert patched.count('auth_middleware') == source.count('auth_middleware')
print(f'Prepared isolated Rust backend integration in {out}; upstream unchanged')
