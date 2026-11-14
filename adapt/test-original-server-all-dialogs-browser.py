#!/usr/bin/env python3
"""All original 2.5 server dialogs render in desktop/mobile Chromium.
No actions, no API writes, no credentials; isolated local HTML only.
"""
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

html = (Path(__file__).parent / 'csqtt-server-panel-upstream-250.html').resolve().as_uri()
with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True, executable_path=os.getenv('CSQTT_TEST_CHROMIUM') or None)
    for width in (390, 1440):
        page = browser.new_page(viewport={'width':width,'height':900})
        errors=[]
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(html)
        dialogs = page.locator('dialog[id]')
        assert dialogs.count() >= 20, dialogs.count()
        ids = dialogs.evaluate_all('(nodes) => nodes.map(n => n.id)')
        for dialog_id in ids:
            dialog = page.locator('#'+dialog_id)
            dialog.evaluate('(node) => node.showModal()')
            assert dialog.is_visible(), (width, dialog_id)
            assert dialog.evaluate('(node) => node.open'), (width, dialog_id)
            dialog.evaluate('(node) => node.close()')
            assert not dialog.evaluate('(node) => node.open'), (width, dialog_id)
        print(f'PASS width={width} dialogs={len(ids)}')
        assert not errors, errors[:5]
        page.close()
    browser.close()
