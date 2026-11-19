#!/usr/bin/env python3
"""Original CSQTT 2.5 server dialog and tab layout in desktop/mobile Chromium.

Only local file:// HTML is opened. No API writes, credentials, or live backend.
"""
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

html = (Path(__file__).parent / 'csqtt-server-panel-upstream-250.html').resolve().as_uri()
with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True, executable_path=os.getenv('CSQTT_TEST_CHROMIUM') or None)
    for width in (360, 390, 1440):
        page = browser.new_page(viewport={'width':width,'height':900})
        errors=[]
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(html)
        dialogs = page.locator('dialog[id]')
        assert dialogs.count() == 19, dialogs.count()
        ids = dialogs.evaluate_all('(nodes) => nodes.map(n => n.id)')
        for dialog_id in ids:
            dialog = page.locator('#'+dialog_id)
            dialog.evaluate('(node) => node.showModal()')
            assert dialog.is_visible(), (width, dialog_id)
            assert dialog.evaluate('(node) => node.open'), (width, dialog_id)
            dialog.evaluate('(node) => node.close()')
            assert not dialog.evaluate('(node) => node.open'), (width, dialog_id)
        expected = ("monitoring", "clients", "logs", "settings")
        assert page.locator('.tabs button.tab-btn').evaluate_all(
            '(nodes) => nodes.map(n => n.dataset.tab)'
        ) == list(expected), (width, "tab order")
        for tab in expected:
            button = page.locator(f'.tabs button.tab-btn[data-tab="{tab}"]')
            button.click()
            active = page.locator('.tabs button.tab-btn.active')
            assert active.count() == 1 and active.get_attribute('data-tab') == tab, (width, tab)
            section = page.locator(f'#{tab}-section')
            assert section.is_visible(), (width, tab)
            for other in expected:
                if other != tab:
                    assert page.locator(f'#{other}-section').is_hidden(), (width, tab, other)
            overflow = page.evaluate(
                'Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth'
            )
            assert overflow <= 1, (width, tab, overflow)
        print(f'PASS width={width} dialogs={len(ids)} tabs={len(expected)} overflow=0')
        assert all(error == 'Failed to fetch' for error in errors), errors[:5]  # offline file:// has no API
        page.close()
    browser.close()
