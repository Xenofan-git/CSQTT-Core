#!/usr/bin/env python3
"""Chromium regression for the isolated CSQTT 2.5-style server-panel preview.

No backend or credentials are used; privileged actions must remain disabled.
"""
import os
import atexit
import functools
import threading
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from playwright.sync_api import sync_playwright

# file:// pages have opaque origins; mocked fetch() is browser-dependent.
# Serve only the local preview on an ephemeral loopback HTTP port so Chromium
# exercises a genuine same-origin request, on both CI and development hosts.
class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

server = ThreadingHTTPServer(
    ('127.0.0.1', 0),
    functools.partial(QuietHandler, directory=str(Path(__file__).parent)),
)
threading.Thread(target=server.serve_forever, daemon=True).start()
atexit.register(server.server_close)
atexit.register(server.shutdown)
HTML = f'http://127.0.0.1:{server.server_port}/csqtt-server-panel-preview.html'
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
        page.locator('#apiDocsOpen').click()
        assert page.locator('#apiDocsDlg').is_visible()
        assert '/api/clients' in page.locator('#apiDocsDlg').inner_text()
        page.locator('#apiDocsClose').click()
        assert page.locator('#apiDocsDlg').is_hidden()
        page.locator('#importExportOpen').click()
        assert page.locator('#importExportDlg').is_visible()
        assert page.locator('#importExportDlg button:disabled').count() == 2
        page.locator('#importExportClose').click()
        page.locator('#themeToggle').click()
        assert page.locator('html').get_attribute('data-theme') == 'light'
        page.reload()
        assert page.locator('html').get_attribute('data-theme') == 'light'
        page.locator('#themeToggle').click()
        assert page.locator('html').get_attribute('data-theme') == 'dark'
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
    # Ordinary preview never contacts a server, even when opened in Chromium.
    page = browser.new_page()
    default_requests = []
    page.on('request', lambda request: default_requests.append(request.url) if '/api/' in request.url else None)
    page.goto(HTML)
    assert default_requests == []
    page.close()
    # Same-origin monitoring: no real backend or credentials are used.
    page = browser.new_page()
    calls = []
    def mock_stats(route):
        calls.append(route.request.url)
        route.fulfill(status=200, content_type='application/json', body='{"status":"Active","active":7,"cpu_normalized":13,"ram":"80 MB","up":1048576,"down":2097152}')
    page.route('**/api/stats', mock_stats)
    page.route('**/api/login', lambda route: route.fulfill(status=200, body='ok'))
    page.goto(HTML + '?connect=1')
    assert page.locator('#login').is_visible()
    assert page.locator('#dashboard').is_hidden()
    page.locator('#user').fill('admin')
    page.locator('#pass').fill('example-secret')
    page.locator('#signIn').click()
    page.wait_for_function("document.querySelector('#serverState').textContent === 'Работает'")
    assert page.locator('#sessionCount').inner_text() == '7'
    assert page.locator('#cpuValue').inner_text() == '13%'
    assert page.locator('#trafficValue').inner_text() == '↑ 1.0 МБ / ↓ 2.0 МБ'
    assert len(calls) == 1 and calls[0].endswith('/api/stats')
    page.route('**/api/logs', lambda route: route.fulfill(status=200, content_type='application/json', body='{"path":"/var/log/csqtt.log","active":true,"lines":["<script>window.__logInjected=true</script>","test line"]}'))
    page.locator('[data-tab="logs"]').click()
    page.wait_for_function("document.querySelector('#logs pre').textContent.includes('test line')")
    assert page.locator('#logs pre').inner_text().startswith('<script>')
    assert page.evaluate('window.__logInjected') is None
    assert page.locator('#logs .log-source code').inner_text() == '/var/log/csqtt.log'
    page.route('**/api/clients', lambda route: route.fulfill(status=200, content_type='application/json', body='[{"name":"Реальный тестовый клиент","password":"PRIVATE-KEY","link":"SECRET-LINK","active":true,"active_sessions":2,"group_id":"g1"}]'))
    page.route('**/api/groups', lambda route: route.fulfill(status=200, content_type='application/json', body='[{"id":"g1","name":"Тестовая группа","admin_uuid":"PRIVATE-UUID"}]'))
    page.locator('[data-tab="clients"]').click()
    page.wait_for_function("document.querySelector('#clientList').textContent.includes('Реальный тестовый клиент')")
    assert 'Тестовая группа' in page.locator('#clientList').inner_text()
    assert 'PRIVATE-KEY' not in page.locator('body').inner_text()
    assert 'SECRET-LINK' not in page.locator('body').inner_text()
    assert 'PRIVATE-UUID' not in page.locator('body').inner_text()
    page.close()
    page = browser.new_page()
    page.route('**/api/stats', lambda route: route.fulfill(status=401, body='Unauthorized'))
    page.route('**/api/login', lambda route: route.fulfill(status=200, body='ok'))
    page.goto(HTML + '?connect=1')
    page.locator('#user').fill('admin')
    page.locator('#pass').fill('example-secret')
    page.locator('#signIn').click()
    page.wait_for_function("document.querySelector('#serverState').textContent === 'Требуется вход'")
    assert page.locator('#sessionCount').inner_text() == '—'
    assert page.locator('#dashboard').is_hidden()
    assert page.locator('#login').is_visible()
    assert page.locator('#loginError').inner_text() == 'Не удалось подтвердить сессию'
    page.close()
    # Original CSQTT returns HTTP 200 with a stealth-reset marker when unauthenticated.
    page = browser.new_page()
    page.route('**/api/login', lambda route: route.fulfill(status=200, body='ok'))
    page.route('**/api/stats', lambda route: route.fulfill(status=200, headers={'x-csqtt-stealth-rst':'1'}, body=''))
    page.goto(HTML + '?connect=1')
    page.locator('#user').fill('admin')
    page.locator('#pass').fill('example-secret')
    page.locator('#signIn').click()
    page.wait_for_function("document.querySelector('#loginError').textContent.includes('сессию')")
    assert page.locator('#dashboard').is_hidden()
    page.close()
    browser.close()
print("server-panel responsive navigation smoke: OK")
