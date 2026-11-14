#!/usr/bin/env python3
"""Exact DOM and JS contract preservation for original 2.5 server panel."""
import os
import re
from pathlib import Path
from playwright.sync_api import sync_playwright

root = Path(__file__).parent
original = Path(os.environ['CSQTT_UPSTREAM_WEB_PANEL']).read_text().split('const PANEL_HTML: &str = r##"', 1)[1].split('"##;', 1)[0]
core = (root / 'csqtt-server-panel-upstream-250.html').read_text()
for pattern in (r'<dialog\s+id="([^"]+)"', r'\b(?:onclick|onchange|oninput)="([a-zA-Z_]\w*)\(', r'data-tab="([^"]+)"'):
    assert re.findall(pattern, original) == re.findall(pattern, core), pattern
with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True, executable_path=os.environ.get('CSQTT_TEST_CHROMIUM') or None)
    for width in (390, 1440):
        page = browser.new_page(viewport={'width':width,'height':844})
        page.goto((root / 'csqtt-server-panel-upstream-250.html').resolve().as_uri())
        assert page.locator('#apiDocsDlg').count() == 1
        assert page.locator('#importExportDlg').count() == 1
        assert page.locator('button[title="API Документация"]').count() == 1
        page.locator('button[title="API Документация"]').click()
        assert page.locator('#apiDocsDlg').is_visible()
        page.locator('#apiDocsDlg button.close-btn').click()
        page.locator('button[title="Импорт / Экспорт конфигурации"]').click()
        assert page.locator('#importExportDlg').is_visible()
        page.locator('#importExportDlg button.close-btn').click()
        page.close()
    browser.close()
print('Original 2.5 panel extraction and header smoke: OK')
