#!/usr/bin/env python3
"""Check password visibility on the isolated static panel; no credentials/API."""
import os
from pathlib import Path
from playwright.sync_api import sync_playwright
html = (Path(__file__).parent / 'csqtt-server-panel-upstream-250.html').resolve().as_uri()
with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True, executable_path=os.environ.get('CSQTT_TEST_CHROMIUM') or None)
    for width in (390, 1440):
        page = browser.new_page(viewport={'width':width, 'height':844})
        page.goto(html)
        page.get_by_role('button', name='Настройки', exact=True).click()
        field = page.locator('#mainpass')
        assert field.get_attribute('type') == 'password'
        page.get_by_role('button', name='Показать').click()
        assert field.get_attribute('type') == 'text'
        page.get_by_role('button', name='Скрыть').click()
        assert field.get_attribute('type') == 'password'
        print(f'PASS password toggle width={width}')
        page.close()
    browser.close()
