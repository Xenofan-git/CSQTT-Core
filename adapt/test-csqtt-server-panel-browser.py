#!/usr/bin/env python3
"""Chromium regression for the isolated CSQTT 2.5-style server-panel preview.

No backend or credentials are used; privileged actions must remain disabled.
"""
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

HTML = (Path(__file__).parent / "csqtt-server-panel-preview.html").resolve().as_uri()
TABS = ("monitor", "clients", "logs", "settings")

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(
        headless=True,
        executable_path=os.environ.get("CSQTT_TEST_CHROMIUM") or None,
    )
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
        # Preview must not expose a working login or privileged server actions.
        assert page.locator("#login").is_hidden()
        assert page.locator("#dashboard").is_visible()
        page.locator('button[data-tab="monitor"]').click()
        page.locator("#refresh").select_option("10")
        assert page.locator("#serverState").inner_text() == "Нет данных"
        assert "без подключения" in page.locator("#refresh").get_attribute("aria-label")
        assert page.locator("#clients button:disabled").count() >= 4
        page.locator('button[data-tab="clients"]').click()
        assert page.locator("#clientList .client-card").count() == 3
        search = page.locator("#search")
        search.fill("Пример клиента 2")
        assert page.locator("#clientList .client-card").count() == 1
        assert page.locator("#clientList .client-card h3").inner_text() == "Пример клиента 2"
        page.locator("#clearSearch").click()
        assert search.input_value() == ""
        assert page.locator("#clientList .client-card").count() == 3
        search.fill("несуществующий клиент")
        assert page.locator("#clientList .client-card").count() == 0
        assert page.locator("#clientList .empty-clients").inner_text() == "Ничего не найдено"
        search.fill("")
        assert page.locator("body").evaluate(
            "(element) => element.scrollWidth <= window.innerWidth + 1"
        ), f"horizontal overflow at {width}px"
        page.locator('button[data-tab="monitor"]').click()
        # A flex item blockifies inline-flex to computed flex. Check the
        # visible layout instead: compact and centered on desktop, full-width
        # on mobile, with all four buttons on the same row.
        layout = page.locator("nav.tabs").evaluate("""element => {
            const rect = element.getBoundingClientRect();
            const parent = element.parentElement.getBoundingClientRect();
            return {
                display: getComputedStyle(element).display,
                width: rect.width, parentWidth: parent.width,
                centerOffset: (rect.left + rect.right - parent.left - parent.right) / 2,
                buttons: [...element.querySelectorAll('button')].map(button => {
                    const box = button.getBoundingClientRect();
                    return {top: box.top, bottom: box.bottom, left: box.left, right: box.right};
                }),
                left: rect.left, right: rect.right
            };
        }""")
        assert layout["display"] == "flex", (width, layout)
        assert abs(layout["centerOffset"]) <= 1, (width, layout)
        buttons = layout["buttons"]
        assert max(b["top"] for b in buttons) - min(b["top"] for b in buttons) <= 1, (width, layout)
        assert all(b["left"] >= layout["left"] and b["right"] <= layout["right"] for b in buttons), (width, layout)
        assert all(a["right"] <= b["left"] for a, b in zip(buttons, buttons[1:])), (width, layout)
        if width > 768:
            assert 0 < layout["width"] < layout["parentWidth"] * 0.8, (width, layout)
            assert page.locator(".grid").evaluate(
                "(element) => getComputedStyle(element).gridTemplateColumns.split(' ').length >= 3"
            )
        else:
            assert abs(layout["width"] - layout["parentWidth"]) <= 1, (width, layout)
        page.close()
    browser.close()
print("server-panel responsive navigation smoke: OK")
