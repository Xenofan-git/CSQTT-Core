#!/usr/bin/env python3
"""Chromium regression for the isolated CSQTT 2.5-style server-panel preview.

No backend or credentials are used; privileged actions must remain disabled.
"""
from pathlib import Path
from playwright.sync_api import sync_playwright

HTML = (Path(__file__).parent / "csqtt-server-panel-preview.html").resolve().as_uri()
TABS = ("monitor", "clients", "logs", "settings")

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    for width, height in ((1440, 900), (390, 844)):
        page = browser.new_page(viewport={"width": width, "height": height})
        page.goto(HTML)
        assert page.locator("nav.tabs button").count() == 4
        for tab in TABS:
            page.locator(f'button[data-tab="{tab}"]').click()
            assert page.locator(f'#{tab}').evaluate(
                "(element) => getComputedStyle(element).display !== 'none'"
            ), (width, tab)
            assert page.locator("nav.tabs button.active").count() == 1
        assert page.locator("button:disabled").count() >= 8
        assert page.locator("body").evaluate(
            "(element) => element.scrollWidth <= window.innerWidth + 1"
        ), f"horizontal overflow at {width}px"
        page.locator('button[data-tab="monitor"]').click()
        if width > 768:
            assert page.locator(".tabs").evaluate(
                "(element) => getComputedStyle(element).display === 'inline-flex'"
            )
            assert page.locator(".grid").evaluate(
                "(element) => getComputedStyle(element).gridTemplateColumns.split(' ').length >= 3"
            )
        else:
            assert page.locator(".tabs").evaluate(
                "(element) => getComputedStyle(element).display === 'flex'"
            )
        page.close()
    browser.close()
print("server-panel responsive navigation smoke: OK")
