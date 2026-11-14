#!/usr/bin/env python3
"""Extract byte-identical original CSQTT 2.5 panel markup and add only Core palette.

No backend is installed or changed. Never expose this HTML as a public service
without original CSQTT auth middleware and server-secret routing.
"""
import argparse
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--upstream', type=Path, required=True)
a = p.parse_args()
source = a.upstream.read_text()
start = 'const PANEL_HTML: &str = r##"'
assert source.count(start) == 1
panel = source.split(start, 1)[1].split('"##;', 1)[0]
assert panel.lstrip().startswith('<!DOCTYPE html>') and '</html>' in panel
# All original dialogs, functions, scripts, and API behavior remain untouched.
# The separate palette stylesheet changes colors only; it cannot alter handlers.
palette = '''<style id="csqtt-core-midnight-violet">
:root{--primary:#9869ff!important;--primary-hover:#7c4de6!important;--accent:#9869ff!important;--bg-color:#f6f3fb!important;--surface:#ffffff!important;--surface-hover:#eee7fa!important;--text-main:#211832!important;--text-muted:#665879!important;--border:#d8cde9!important}
[data-theme="dark"]{--primary:#9869ff!important;--primary-hover:#b18bff!important;--accent:#9869ff!important;--bg-color:#0a0a16!important;--surface:#171426!important;--surface-hover:#201b33!important;--text-main:#f5f1ff!important;--text-muted:#a99fc2!important;--border:#39304f!important}
</style>'''
assert '</head>' in panel
panel = panel.replace('</head>', palette + '\n</head>', 1)
output = Path(__file__).parent / 'csqtt-server-panel-upstream-250.html'
output.write_text(panel)
print(f'Extracted upstream panel: {len(panel)} chars, {panel.count("<dialog ")} dialogs')
