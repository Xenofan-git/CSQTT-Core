#!/usr/bin/env python3
"""Chromium UI smoke and API-contract interaction tests for the CSQTT panel.

The actual panel is served over HTTP. API calls are intercepted with a deterministic
fixture, so profile selection/settings persistence are exercised without touching
a real account, VK token, server, or network.
"""
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading

from playwright.sync_api import sync_playwright

ROOT = Path(".").resolve()
HTML = ROOT / "adapt" / "csqtt-panel-preview.html"
EXPECTED = ["connect", "settings", "deploy", "logs", "direct", "route", "singbox"]
WIDTHS = [390, 768, 1280]


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


def main():
    profiles = [
        {"id": 1, "name": "Profile one", "protocol": "CSQTT", "hashes": "hash-one"},
        {"id": 2, "name": "Profile two", "protocol": "CSQTT", "hashes": "hash-two"},
    ]
    selected = {"profile": profiles[0]}
    vpn_connected = {"value": False}
    hold_connecting = {"value": False}
    captcha_state = {"pending": False}
    captcha_results = []
    vk_token = {"hasToken": True}
    # A fresh device has no tunnel password; the success path provisions one below.
    tunnel_auth = {"configured": False}
    singbox_config = {"ListenAddress":"127.0.0.1:19180","CSQTTAddress":"","CSQTTEnabled":False,"CSQTTRule":{"ID":"csqtt","Enabled":False,"RuleSets":[],"Domains":[],"DomainSuffixes":[],"DomainKeywords":[],"DomainRegexes":[],"IPCIDRs":[],"Outbound":"csqtt"},"DirectEnabled":True,"RouteOrder":[],"FinalOutbound":"socks-hydra","DirectDomains":["example.ru"],"DirectIPCIDRs":[],"SOCKS5":[{"ID":"hydra","Server":"192.0.2.10","ServerPort":1080,"Enabled":True},{"ID":"disabled","Server":"192.0.2.11","ServerPort":1080,"Enabled":False}],"Rules":[]}
    singbox_saved = {}
    settings = {
        "authMode": "manual", "workers": 9, "autoApiWorkers": 9,
        "obfs": "video", "fingerprint": "firefox", "turnTransport": "udp",
        "captchaMode": "auto", "clientIds": "8202606,6287487",
        "directDomains": "", "directIPs": "", "deviceId": "fixture-device",
    }
    socks5_state = {"interface":"eth1","protocol":"SOCKS5","auth":"no-auth","monitoring":True,"listening":True,"address":"192.168.4.26:12500"}
    calls = []
    fail_paths = set()

    server = ThreadingHTTPServer(
        ("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT))
    )
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    base_url = f"http://127.0.0.1:{server.server_port}/adapt/csqtt-panel-preview.html"

    def api_fixture(route):
        request = route.request
        from urllib.parse import urlparse
        path = urlparse(request.url).path.removeprefix("/api")
        method = request.method
        calls.append((method, path, request.post_data))
        body = {}
        status = 200
        if path in fail_paths:
            route.fulfill(status=503, content_type="application/json", body=json.dumps({"error": "fixture unavailable"}))
            return
        if path == "/configs" and method == "GET":
            body = profiles
        elif path == "/configs/selected" and method == "GET":
            body = selected["profile"]
        elif path == "/configs/selected" and method in ("POST", "PUT"):
            candidate = json.loads(request.post_data or "{}")
            selected["profile"] = candidate
            body = {"ok": True}
        elif path == "/settings" and method == "GET":
            body = settings
        elif path == "/settings" and method in ("POST", "PUT"):
            settings.clear()
            settings.update(json.loads(request.post_data or "{}"))
            body = {"ok": True}
        elif path == "/tunnel/auth" and method == "GET":
            body = dict(tunnel_auth)
        elif path == "/tunnel/auth" and method == "PUT":
            tunnel_auth["configured"] = True
            body = {"ok": True, "configured": True}
        elif path == "/vpn/status":
            body = {"connected": vpn_connected["value"]}
        elif path == "/vpn/connect" and method == "POST":
            if not hold_connecting["value"]:
                vpn_connected["value"] = True
            body = {"ok": True}
        elif path == "/vpn/disconnect" and method == "POST":
            vpn_connected["value"] = False
            body = {"ok": True}
        elif path == "/vk/state":
            body = {**vk_token, "fetching": False}
        elif path == "/captcha/state":
            body = dict(captcha_state)
        elif path == "/captcha/result" and method == "POST":
            result = json.loads(request.post_data or "{}")
            captcha_results.append(result)
            if result.get("sessionToken") == captcha_state.get("sessionToken") and result.get("result"):
                captcha_state["pending"] = False
                body = {"ok": True}
            else:
                body = {"ok": False}
        elif path == "/singbox/status":
            body = {"running": False, "pid": 0, "configured": True, "log_path": "/tmp/sing-box.log"}
        elif path == "/singbox/config" and method == "GET":
            body = {"configured": True, "config": dict(singbox_config)}
        elif path == "/singbox/config" and method == "PUT":
            singbox_saved.clear()
            singbox_saved.update(json.loads(request.post_data or "{}"))
            singbox_config.clear()
            singbox_config.update(singbox_saved)
            body = {"ok": True}
        elif path == "/singbox/logs":
            body = "fixture sing-box log"
        elif path == "/singbox/start" and method == "POST":
            body = {"ok": True}
        elif path == "/singbox/stop" and method == "POST":
            body = {"ok": True}
        elif path == "/socks5/status":
            body = dict(socks5_state)
        elif path == "/deploy/status":
            body = {"deploying": False}
        elif path == "/deploy/log":
            body = ""
        elif path == "/version":
            body = {"backend": "fixture", "ui": "test"}
        elif path == "/logs":
            body = []
        elif path == "/logs/clear" and method == "POST":
            body = {"ok": True}
        elif path == "/deploy" and method == "POST":
            body = {"ok": True}
        elif path == "/updates/lalune":
            body = {"hasUpdate": False}
        else:
            status = 404
            body = {"error": f"unexpected API request: {method} {path}"}
        route.fulfill(status=status, content_type="application/json", body=json.dumps(body))

    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            page = browser.new_page()
            page_errors = []
            page.on("pageerror", lambda error: page_errors.append(str(error)))
            page.on("dialog", lambda dialog: dialog.accept())
            page.route("**/api/**", api_fixture)
            page.goto(base_url, wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#profileList .profile') !== null")
            page.wait_for_timeout(100)

            tabs = page.locator("button[data-page]")
            actual = [tabs.nth(i).get_attribute("data-page") for i in range(tabs.count())]
            assert actual == EXPECTED, f"seven-tab order mismatch: {actual}"
            assert page.locator("#page-connect > .connect-hero").count() == 1
            assert page.locator("#page-connect > .card").count() == 1, "Connect must retain the clean 2.5.0 single-card composition"
            assert page.locator("#topHeart").count() == 1 and page.locator("#topSwitch").count() == 1 and page.locator("#topSettings").count() == 1
            assert page.locator("#topQr, #qrImage").count() == 0, "QR controls must remain absent"

            click_events = []
            page.evaluate("""() => {
                window.__tabClickEvents = [];
                document.addEventListener('click', event => {
                    const button = event.target.closest('button[data-page]');
                    window.__tabClickEvents.push({
                        target: event.target.tagName,
                        page: button?.dataset.page || null,
                        defaultPreventedAtDocument: event.defaultPrevented,
                        path: event.composedPath().slice(0, 5).map(node => node?.tagName || node?.nodeName)
                    });
                }, true);
            }""")
            for name in EXPECTED:
                page.locator(f'button[data-page="{name}"]').click()
                active_state = page.locator(f"#page-{name}").evaluate(
                    "(el) => ({className: el.className, hidden: el.hidden, display: getComputedStyle(el).display})"
                )
                tab_state = page.locator(f'button[data-page="{name}"]').evaluate(
                    "(el) => ({className: el.className, ariaCurrent: el.getAttribute('aria-current'), parent: el.parentElement?.className})"
                )
                assert "active" in active_state["className"].split(), (
                    f"tab did not activate: {name}; page={active_state}; tab={tab_state}; "
                    f"pageErrors={page_errors}; tabs={page.locator('.tabs').count()}; "
                    f"activePages={page.locator('.page.active').evaluate_all('(els) => els.map(el => el.id)')}; "
                    f"clickEvents={page.evaluate('window.__tabClickEvents')}; "
                    f"onclick={page.locator(f'button[data-page=\"{name}\"]').evaluate('(el) => ({onclick: String(el.onclick), disabled: el.disabled, connected: el.isConnected, rect: (() => {const r=el.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,cx:r.x+r.width/2,cy:r.y+r.height/2}})(), hit: (() => {const r=el.getBoundingClientRect();const h=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);return {tag:h?.tagName,page:h?.dataset?.page,className:h?.className}})(), nav: (() => {const r=el.parentElement.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,scrollWidth:el.parentElement.scrollWidth,clientWidth:el.parentElement.clientWidth}})()})')}"
                )
                assert page.locator(f'button[data-page="{name}"]').get_attribute("aria-current") == "page", (
                    f"active tab missing aria-current state: {name}"
                )
                assert page.locator(f'button[data-page="{name}"]').get_attribute("aria-pressed") is None
                if name == "deploy":
                    page.wait_for_function("document.querySelector('#socks5Address').value === '192.168.4.26:12500'")
                    assert page.locator("#socks5State").inner_text() == "Слушатель активен"
                    assert page.locator("#copySocks5Address").is_enabled()
                    assert any(method == "GET" and path == "/socks5/status" for method, path, _ in calls)
                    socks5_state["listening"] = False
                    socks5_state["address"] = ""
                    page.evaluate("() => window.dispatchEvent(new Event('focus'))")
                    page.locator('button[data-page="connect"]').click()
                    page.locator('button[data-page="deploy"]').click()
                    page.wait_for_function("document.querySelector('#socks5Address').value === '—'")
                    assert page.locator("#copySocks5Address").is_disabled()
                    socks5_state["listening"] = True
                    socks5_state["address"] = "192.168.4.26:12500"
                if name == "route":
                    page.wait_for_function("document.querySelectorAll('#singboxRouteRuleEditor .route-rule').length === 2")
                    cards = page.locator("#singboxRouteRuleEditor .route-rule")
                    assert cards.nth(0).get_attribute("data-system-card") == "direct"
                    assert cards.nth(1).get_attribute("data-system-card") == "csqtt"
                    assert cards.nth(0).locator("[data-remove-route-rule], [data-move-route-card]").count() == 0, "Direct must be immutable and pinned"
                    assert cards.nth(1).locator("[data-remove-route-rule]").count() == 0, "CSQTT system card cannot be deleted"
                    page.locator("#singboxAddRouteRule").click()
                    user_card = page.locator('#singboxRouteRuleEditor .route-rule[data-card-type="user"]')
                    assert user_card.count() == 1
                    user_card.locator('[data-field="Domains"]').fill("priority.example")
                    user_card.locator('[data-move-route-card="up"]').click()
                    ordered = page.locator("#singboxRouteRuleEditor .route-rule[data-movable='true']").evaluate_all("(els) => els.map(el => el.dataset.routeId)")
                    assert ordered[0].startswith("route-") and ordered[1] == "csqtt", ordered
                    assert page.locator("#singboxRouteRuleEditor .route-rule").first.get_attribute("data-system-card") == "direct"
                    assert page.locator("#singboxRouteRuleEditor [data-system-enabled='direct']").is_checked()
                    assert page.locator("#singboxRouteRuleEditor [data-system-enabled='csqtt']").is_checked() is False
                    page.locator("#singboxRouteRuleEditor [data-system-enabled='csqtt']").check()
                    assert page.locator("#singboxCsqttEnabled").is_checked()
                    user_card.locator('[data-field="Outbound"]').select_option("csqtt")
                    page.locator("#singboxRouteRuleEditor [data-system-enabled='csqtt']").uncheck()
                    assert user_card.locator('[data-field="Outbound"]').input_value() == "csqtt", "disabling CSQTT must not rewrite a disabled rule's selected outbound"
                    assert user_card.locator('[data-field="Outbound"] option:checked').is_disabled()
                    page.locator('#singboxRouteRuleEditor [data-card-type="user"] [data-remove-route-rule]').click()
                    assert page.locator('#singboxRouteRuleEditor [data-card-type="user"]').count() == 0
                if name == "direct":
                    # Direct editor is a full-screen dialog; close it before using bottom navigation again.
                    page.locator("#routeClose").click()
                    assert page.locator("#page-connect").evaluate("el => el.classList.contains('active')")

            # Hold the first saved-config response while the user edits the form.
            # Releasing a stale response must not overwrite those in-progress edits.
            page.evaluate("""() => {
                const originalFetch = window.fetch.bind(window);
                let gateUsed = false;
                window.__singboxConfigGate = { requested: false, release: null };
                window.fetch = (input, init = {}) => {
                    const url = typeof input === 'string' ? input : input.url;
                    const method = (init.method || (typeof input === 'object' && input.method) || 'GET').toUpperCase();
                    if (!gateUsed && method === 'GET' && url.includes('/api/singbox/config')) {
                        gateUsed = true;
                        window.__singboxConfigGate.requested = true;
                        return new Promise(resolve => {
                            window.__singboxConfigGate.release = () => {
                                resolve(new Response(
                                    JSON.stringify({configured: true, config: {
                                        ListenAddress: "127.0.0.1:19180",
                                        CSQTTAddress: "",
                                        CSQTTEnabled: false,
                                        FinalOutbound: "socks-hydra",
                                        DirectDomains: ["stale-response.example"],
                                        DirectIPCIDRs: [],
                                        SOCKS5: [{ID: "hydra", Server: "192.0.2.10", ServerPort: 1080, Enabled: true}],
                                        Rules: []
                                    }}),
                                    {status: 200, headers: {"Content-Type": "application/json"}}
                                ));
                                setTimeout(() => { window.__singboxConfigGate.settled = true; }, 0);
                            };
                        });
                    }
                    return originalFetch(input, init);
                };
            }""")
            page.locator('button[data-page="singbox"]').click()
            page.wait_for_function("window.__singboxConfigGate?.requested === true")
            page.locator("#singboxListen").fill("127.0.0.1:19222")
            page.locator("#singboxDirectDomains").fill("user-edit.example")
            page.evaluate("window.__singboxConfigGate.release()")
            page.wait_for_function("window.__singboxConfigGate?.settled === true")
            assert page.locator("#singboxListen").input_value() == "127.0.0.1:19222"
            assert page.locator("#singboxDirectDomains").input_value() == "user-edit.example"
            # Reload normally to restore the baseline fixture for the remaining
            # default-outbound lifecycle assertions below.
            page.locator("#singboxLoadEngine").click()
            page.wait_for_function("document.querySelector('#singboxListen').value === '127.0.0.1:19180' && document.querySelector('#singboxDirectDomains').value === 'example.ru'")
            assert page.locator("#page-singbox").evaluate("el => el.classList.contains('active')")
            assert page.locator("#singboxListen").input_value() == "127.0.0.1:19180"
            assert page.locator("#singboxDirectDomains").input_value() == "example.ru"
            assert page.locator("#singboxCsqttEnabled").is_checked() is False
            assert page.locator("#singboxFinalOutbound").input_value() == "socks-hydra"
            outbound_options = page.locator("#singboxFinalOutbound option").all_text_contents()
            assert "socks-hydra" in outbound_options and "csqtt" not in outbound_options and "socks-disabled" not in outbound_options, outbound_options
            # Exercise the full default-outbound lifecycle in the real Chromium UI:
            # Direct -> CSQTT -> external SOCKS5, saving and reloading each choice.
            page.locator("#singboxFinalOutbound").select_option("direct")
            assert page.locator("#singboxFinalOutbound").input_value() == "direct"
            page.locator("#singboxEngineConfigMessage").evaluate("(el) => { el.textContent = ''; el.className = 'notice'; }")
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function("document.querySelector('#singboxEngineConfigMessage').textContent.includes('проверена sing-box')")
            assert singbox_saved.get("FinalOutbound") == "direct"
            page.locator("#singboxLoadEngine").click()
            page.wait_for_function("document.querySelector('#singboxFinalOutbound').value === 'direct'")
            assert page.locator("#singboxCsqttEnabled").is_checked() is False

            page.locator("#singboxCsqttEnabled").check()
            assert page.locator("#singboxCsqtt").input_value() == "127.0.0.1:12500"
            page.locator("#singboxFinalOutbound").select_option("csqtt")
            assert page.locator("#singboxFinalOutbound").input_value() == "csqtt"
            page.locator("#singboxEngineConfigMessage").evaluate("(el) => { el.textContent = ''; el.className = 'notice'; }")
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function("document.querySelector('#singboxEngineConfigMessage').textContent.includes('проверена sing-box')")
            assert singbox_saved.get("CSQTTEnabled") is True
            assert singbox_saved.get("CSQTTAddress") == "127.0.0.1:12500"
            assert singbox_saved.get("CSQTTRule", {}).get("Enabled") is True
            assert singbox_saved.get("DirectEnabled") is True
            assert "csqtt" in singbox_saved.get("RouteOrder", [])
            assert singbox_saved.get("FinalOutbound") == "csqtt"
            page.locator("#singboxLoadEngine").click()
            page.wait_for_function("document.querySelector('#singboxFinalOutbound').value === 'csqtt'")
            assert page.locator("#singboxCsqttEnabled").is_checked() is True

            # Disabling CSQTT must not silently rewrite an explicit final choice.
            page.locator("#singboxCsqttEnabled").uncheck()
            assert page.locator("#singboxFinalOutbound").input_value() == "csqtt"
            assert page.locator("#singboxFinalOutbound option:checked").is_disabled()
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function("document.querySelector('#singboxEngineConfigMessage').textContent.includes('CSQTT выключен')")
            assert singbox_saved.get("FinalOutbound") == "csqtt", "invalid save must not reach the API"
            page.locator("#singboxCsqttEnabled").check()
            assert page.locator("#singboxFinalOutbound").input_value() == "csqtt"
            assert page.locator("#singboxFinalOutbound option:checked").is_enabled()

            page.locator("#singboxFinalOutbound").select_option("socks-hydra")
            assert page.locator("#singboxFinalOutbound").input_value() == "socks-hydra"
            page.locator("#singboxDirectDomains").fill("example.ru\\napi.example.ru")
            assert page.locator("#singboxFinalOutbound").input_value() == "socks-hydra"
            page.locator("#singboxEngineConfigMessage").evaluate("(el) => { el.textContent = ''; el.className = 'notice'; }")
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function("document.querySelector('#singboxEngineConfigMessage').textContent.includes('проверена sing-box')")
            assert any(method == "PUT" and path == "/singbox/config" for method, path, _ in calls)
            assert singbox_saved.get("CSQTTEnabled") is True and singbox_saved.get("CSQTTAddress") == "127.0.0.1:12500"
            page.wait_for_function("() => document.querySelector('#singboxFinalOutbound').value === 'socks-hydra' && document.querySelector('#singboxEngineConfigMessage').textContent.includes('проверена sing-box')")
            assert singbox_saved.get("FinalOutbound") == "socks-hydra", {"saved":dict(singbox_saved), "recent_calls":calls[-8:]}
            assert [x["ID"] for x in singbox_saved.get("SOCKS5", [])] == ["hydra", "disabled"]
            page.locator("#singboxLoadEngine").click()
            page.wait_for_function("document.querySelector('#singboxFinalOutbound').value === 'socks-hydra'")
            assert page.locator("#singboxCsqttEnabled").is_checked() is True
            assert page.locator("#singboxLogs").inner_text() == "fixture sing-box log"
            page.locator("#singboxStart").click()
            assert any(method == "POST" and path == "/singbox/start" for method, path, _ in calls)

            # The agreed design is a fixed 390px native-phone canvas on every
            # device. Desktop must center that same canvas; it must not stretch,
            # reflow, or switch to a desktop layout at wider viewport sizes.
            for width in WIDTHS:
                page.set_viewport_size({"width": width, "height": 850})
                page.wait_for_timeout(40)
                dimensions = page.evaluate("""() => {
                  const body = document.body.getBoundingClientRect();
                  const nav = document.querySelector('.tabs').getBoundingClientRect();
                  return {
                    viewport: innerWidth,
                    body: {left: body.left, width: body.width, right: body.right},
                    nav: {left: nav.left, width: nav.width, right: nav.right}
                  };
                }""")
                expected_left = (width - 390) / 2
                assert abs(dimensions["body"]["width"] - 390) <= 1, (
                    f"phone canvas changed width at {width}px: {dimensions}"
                )
                assert abs(dimensions["body"]["left"] - expected_left) <= 1, (
                    f"phone canvas is not centered at {width}px: {dimensions}"
                )
                assert abs(dimensions["nav"]["width"] - 370) <= 1, (
                    f"bottom navigation changed width at {width}px: {dimensions}"
                )
                assert abs(dimensions["nav"]["left"] - (width - 370) / 2) <= 1, (
                    f"bottom navigation is not centered at {width}px: {dimensions}"
                )

            # Selecting another profile must round-trip through the API and refresh
            # the profile-specific hash shown on the Tunnel page.
            page.locator('button[data-page="connect"]').click()
            page.locator("#topSwitch").click()
            page.wait_for_function("!document.querySelector('#profileManagerOverlay').classList.contains('hidden')")
            page.locator('.profile[data-id="2"]').click()
            page.wait_for_function("document.querySelector('#profileManagerOverlay').classList.contains('hidden')")
            page.wait_for_function("document.querySelector('#vkHashes').value === 'hash-two'")
            assert any(m == "POST" and path == "/configs/selected" for m, path, _ in calls), (
                "profile selection did not reach backend API"
            )
            assert page.locator("#vkHashes").is_disabled() is False
            assert page.locator("#vkHashes").evaluate("el => el.readOnly")

            # Match the real panel guard: without a configured tunnel password,
            # Connect must not call /vpn/connect. Provision the fixture only after
            # proving the guarded path, then exercise connect/disconnect.
            page.locator("#power").click()
            page.wait_for_function('''() => document.querySelector("#toast").textContent.includes("Сначала задайте пароль туннеля")''')
            assert not any(method == "POST" and path == "/vpn/connect" for method, path, _ in calls), (
                "Connect bypassed the required tunnel-password guard"
            )
            assert page.locator("#page-settings").evaluate("el => el.classList.contains('active')"), (
                "missing tunnel password did not direct the user to Tunnel settings"
            )
            page.locator('button[data-page="settings"]').click()
            page.locator("#editTunnelAuth").click()
            page.locator("#tunnelPassword").fill("fixture-tunnel-pass_123")
            page.locator("#saveTunnelAuth").click()
            page.wait_for_function('''() => document.querySelector("#tunnelAuthOverlay").classList.contains("hidden")''')
            assert any(method == "PUT" and path == "/tunnel/auth" for method, path, _ in calls), "Tunnel password dialog did not persist to API"
            page.wait_for_function('''() => document.querySelector("#tunnelAuthSummary").textContent.includes("сохранён")''')
            page.locator('button[data-page="connect"]').click()

            # With tunnel auth configured, power button must send connect/disconnect.
            page.locator("#power").click()
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Подключение запрошено")""")
            page.wait_for_function("""() => document.querySelector("#power").getAttribute("aria-pressed") === "true" """)
            page.locator("#power").click()
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Отключение запрошено")""")
            page.wait_for_function("""() => document.querySelector("#power").getAttribute("aria-pressed") === "false" """)
            assert any(method == "POST" and path == "/vpn/connect" for method, path, _ in calls)
            assert any(method == "POST" and path == "/vpn/disconnect" for method, path, _ in calls)

            # Hash acquisition mode contract: all three original CSQTT modes
            # remain selectable and persist independently of VK credential mode.
            page.locator('button[data-page="settings"]').click()
            # Manual mode is usable without a VK token; both automatic modes
            # must refuse persistence until token state confirms authorization.
            vk_token["hasToken"] = False
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#profileList .profile') !== null")
            page.locator('button[data-page="settings"]').click()
            page.wait_for_function("document.querySelector('#vkBadge').textContent.includes('Нет токена')")
            page.locator("#authMode").select_option("manual")
            page.locator("#saveSettings").click()
            page.wait_for_function("document.querySelector('#toast').textContent.includes('Настройки сохранены')")
            saved_before_token = len([1 for method, path, raw in calls if method == "POST" and path == "/settings" and raw])
            for mode in ("autoApi", "autoVk"):
                page.locator("#authMode").select_option(mode)
                page.locator("#saveSettings").click()
                page.wait_for_function("document.querySelector('#toast').textContent.includes('сначала авторизуйтесь в VK')")
                saved_now = len([1 for method, path, raw in calls if method == "POST" and path == "/settings" and raw])
                assert saved_now == saved_before_token, f"{mode} was persisted without a VK token"
            vk_token["hasToken"] = True
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#profileList .profile') !== null")
            page.locator('button[data-page="settings"]').click()
            page.wait_for_function("document.querySelector('#vkBadge').textContent.includes('Токен есть')")
            for mode in ("manual", "autoApi", "autoVk"):
                page.locator("#authMode").select_option(mode)
                page.locator("#saveSettings").click()
                page.wait_for_function(
                    "(mode) => document.querySelector('#authMode').value === mode",
                    arg=mode,
                )
                page.wait_for_function("document.querySelector('#toast').textContent.includes('Настройки сохранены')")
                stored_modes = [json.loads(raw) for method, path, raw in calls
                                if method == "POST" and path == "/settings" and raw]
                assert stored_modes[-1]["authMode"] == mode, f"hash mode {mode} did not persist"

            # Exhausted automatic CAPTCHA attempts are represented by a pending
            # manual challenge from the backend. The real modal must appear only
            # after Connect has armed the current connection attempt.
            page.locator('button[data-page="connect"]').click()
            hold_connecting["value"] = True
            captcha_state.update({
                "pending": True,
                "mode": "manual",
                "sessionToken": "fixture-manual-captcha-session",
                "redirectUri": "https://id.vk.ru/not_robot_captcha?expired_at=4102444800",
            })
            page.locator("#power").click()
            page.wait_for_function(
                "() => !document.querySelector('#captchaOverlay').classList.contains('hidden')"
            )
            assert "Автоматические попытки завершены" in page.locator("#captchaModalText").inner_text()
            assert page.locator("#openCaptchaModal").is_visible()
            page.locator("#captchaModalAnswer").fill("fixture-manual-vk-success-token")
            page.locator("#submitCaptchaModal").click()
            page.wait_for_function(
                "() => document.querySelector('#captchaOverlay').classList.contains('hidden')"
            )
            assert captcha_results and captcha_results[-1] == {
                "sessionToken": "fixture-manual-captcha-session",
                "result": "fixture-manual-vk-success-token",
            }, "manual CAPTCHA answer did not reach /captcha/result with the active session"
            assert captcha_state["pending"] is False, "fixture backend did not complete the pending CAPTCHA"

            # Stale/foreign session tokens must be rejected by the API contract.
            stale = page.evaluate("""async () => {
              const r = await fetch('/api/captcha/result', {
                method: 'POST', headers: {'Content-Type':'application/json'},
                body: JSON.stringify({sessionToken:'old-or-foreign-session', result:'forged-answer'})
              });
              return {status:r.status, body:await r.json()};
            }""")
            assert stale["status"] == 200 and stale["body"].get("ok") is False, (
                f"stale CAPTCHA session was not rejected: {stale!r}"
            )
            hold_connecting["value"] = False
            vpn_connected["value"] = True
            page.wait_for_timeout(5500)
            page.wait_for_function(
                "() => document.querySelector('#power').getAttribute('aria-pressed') === 'true'"
            )
            vpn_connected["value"] = False
            captcha_state.update({"pending": False, "mode": "", "sessionToken": "", "redirectUri": ""})

            # Direct rules are validated and persisted through the settings API.
            page.locator('button[data-page="direct"]').click()
            assert page.locator("#page-direct").is_visible(), "Direct tab did not activate"
            assert page.locator("#directDomains").is_visible(), "Direct domains field is not visible"
            assert page.locator("#directIPs").is_visible(), "Direct IP field is not visible"
            page.locator("#directDomains").fill("example.com\napi.example.com")
            page.locator("#directIPs").fill("192.0.2.1\n2001:db8::1")
            page.locator("#saveDirectRules").click()
            page.wait_for_function("document.querySelector('#routeMessage').textContent.includes('Правила сохранены')")
            saved = [json.loads(raw) for method, path, raw in calls
                     if method == "POST" and path == "/settings" and raw]
            assert saved and saved[-1]["directDomains"] == "example.com\napi.example.com", (
                "Direct domains were not persisted separately by the settings API"
            )
            assert saved[-1]["directIPs"] == "192.0.2.1\n2001:db8::1", (
                "Direct IPv4/IPv6 rules were not persisted separately by the settings API"
            )
            prior_saves = len(saved)
            page.locator("#directIPs").fill("192.0.2.999\nhttps://example.com")
            page.locator("#saveDirectRules").click()
            page.wait_for_function("document.querySelector('#routeMessage').textContent.includes('Некорректный Direct IP:')")
            saved_after_invalid = [raw for method, path, raw in calls if method == "POST" and path == "/settings" and raw]
            assert len(saved_after_invalid) == prior_saves, "invalid Direct IP input reached the settings API"

            assert page.locator("#page-direct").is_visible()
            assert not page.locator("#deploySecretsOverlay").is_visible()
            page.locator("#routeClose").click()

            # Tunnel settings: changing the worker count must persist through /settings.
            page.locator('button[data-page="settings"]').click()
            page.locator("#workers").evaluate("""el => {
              el.value = "11";
              el.dispatchEvent(new Event("input", {bubbles:true}));
            }""")
            page.locator("#serverPeer").fill("192.0.2.55")
            page.locator("#serverPeer").press("Tab")
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Адрес сервера сохранён") """)
            assert selected["profile"]["peer"] == "192.0.2.55", "server peer did not persist through selected-profile API"
            page.locator("#vkAuthMode").select_option("auto_js")
            page.locator("#turnTransport").select_option("tcp_tls")
            page.locator("#captchaMode").select_option("manual")
            page.locator("#saveSettings").click()
            page.wait_for_function("""() => document.querySelector("#workersValue").textContent === "11" """)
            saved_settings = [json.loads(raw) for method, path, raw in calls
                              if method == "POST" and path == "/settings" and raw]
            assert saved_settings and saved_settings[-1]["workers"] == 11, (
                "Tunnel settings were not persisted"
            )
            assert saved_settings[-1]["vkAuthMode"] == "auto_js" and saved_settings[-1]["turnTransport"] == "tcp_tls" and saved_settings[-1]["captchaMode"] == "manual", (
                "VK credential mode, transport, or CAPTCHA mode did not persist"
            )

            # Logs follow the original CSQTT LogsTab: opening the tab loads the
            # live log stream; the terminal exposes copy and clear actions only.
            # There is intentionally no manual refresh control in the original UI.
            page.locator('button[data-page="logs"]').click()
            page.wait_for_function("""() => document.querySelector("#logs").textContent.includes("Записей пока нет")""")
            assert any(method == "GET" and path == "/logs" for method, path, _ in calls), (
                "Opening Logs did not automatically load /logs"
            )
            assert page.locator("#refreshLogs").count() == 0, (
                "Logs must match the original copy/clear-only terminal controls"
            )
            page.locator("#copyLogs").click()
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Журнал выделен") || document.querySelector("#toast").textContent.includes("Журнал скопирован")""")
            page.locator("#clearLogs").click()
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Журнал очищен")""")
            assert any(method == "POST" and path == "/logs/clear" for method, path, _ in calls), (
                "Logs clear action did not reach backend"
            )

            # Deploy: validate the one-button flow against a deterministic API fixture.
            # This proves the page constructs the expected request without SSHing anywhere.
            page.locator('button[data-page="deploy"]').click()
            page.locator("#deployHost").fill("192.0.2.50")
            page.locator("#deployPassword").fill("fixture-ssh-password")
            page.locator("#editDeployAuth").click()
            page.locator("#mainPassword").fill("fixturetunnelpassword")
            page.locator("#webLogin").fill("fixtureadmin")
            page.locator("#webPassword").fill("fixturewebpassword")
            page.locator("#saveDeployAuth").click()
            page.wait_for_function("""() => document.querySelector("#deploySecretsOverlay").classList.contains("hidden")""")
            page.locator("#startDeploy").click()
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Деплой запущен")""")
            deploy_requests = [json.loads(raw) for method, path, raw in calls
                               if method == "POST" and path == "/deploy" and raw]
            assert deploy_requests, "Deploy button did not call /deploy"
            assert deploy_requests[-1]["host"] == "192.0.2.50"
            assert deploy_requests[-1]["deviceId"] == "fixture-device"
            assert deploy_requests[-1]["corePort"] == 47000 and deploy_requests[-1]["warpPort"] == 47002

            # A failed status endpoint must not be misreported as a real disconnect,
            # and deployment actions must remain blocked when operation state is unknown.
            fail_paths.update({"/vpn/status", "/deploy/status", "/deploy/log"})
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#apiStatus').textContent.includes('API')")
            assert page.locator("#connState").inner_text() == "Статус недоступен", "VPN API failure was presented as disconnected"
            assert page.locator("#power").is_disabled(), "connect toggle stayed enabled while VPN state was unknown"
            assert "Не удалось получить состояние деплоя" in page.locator("#deployState").inner_text(), "unknown deploy state was presented as a completed/idle operation"
            assert page.locator("#startDeploy").is_disabled(), "deploy remained enabled while deploy state was unknown"
            assert not page_errors, f"uncaught browser errors: {page_errors}"

            # Exercise the split MAIN/ISOLATED CAPTCHA Bridge. Page-controlled
            # fragments and referrer must never select the redirect destination.
            bridge_main_source = (ROOT / "captcha-bridge" / "captcha-bridge-main.js").read_text()
            bridge_isolated_source = (ROOT / "captcha-bridge" / "captcha-bridge.js").read_text()
            origin_policy_source = (ROOT / "captcha-bridge" / "origin-policy.js").read_text()
            # The extension bridge and the panel share the same fixture
            # backend. Arm the exact session embedded in the VK fixture so the
            # panel's authenticated result endpoint accepts this bridge result.
            captcha_state.update({
                "pending": True,
                "mode": "auto",
                "sessionToken": "fixture-bridge-session",
                "redirectUri": "",
            })
            panel_origin = f"http://127.0.0.1:{server.server_port}"
            vk_origin = "https://id.vk.com"
            bridge_page = browser.new_page()
            bridge_page.add_init_script("""
              Object.defineProperty(document, 'referrer', { configurable: true, get: () => 'https://attacker.example/' });
              Object.defineProperty(window, 'browser', { configurable: true, value: { storage: { local: {
                get: (key) => Promise.resolve({ trustedPanelOrigin: PANEL_ORIGIN })
              } } } });
            """.replace("PANEL_ORIGIN", json.dumps(panel_origin)) + "\n" +
                origin_policy_source + "\n" + bridge_main_source + "\n" + bridge_isolated_source)
            bridge_page.route("**/api/**", api_fixture)
            bridge_page.route(
                panel_origin + "/",
                lambda route: route.fulfill(
                    status=200, content_type="text/html", body=HTML.read_text()
                ),
            )
            def vk_bridge_fixture(token):
                def handle(route):
                    if "captchaNotRobot.check" in route.request.url:
                        route.fulfill(
                            status=200,
                            content_type="application/json",
                            body=json.dumps({"response": {"success_token": token}}),
                        )
                    else:
                        route.fulfill(
                            status=200,
                            content_type="text/html",
                            body="<html><body>VK fixture</body></html>",
                        )
                return handle

            bridge_page.route(vk_origin + "/**", vk_bridge_fixture("fixture-bridge-success-token"))
            bridge_page.goto(
                vk_origin + "/captcha-fixture#"
                + "lalune_panel=https%3A%2F%2Fattacker.example"
                + "&lalune_session=fixture-bridge-session",
                wait_until="domcontentloaded",
            )
            bridge_page.evaluate("() => { void fetch('/captchaNotRobot.check'); }")
            try:
                bridge_page.wait_for_function("() => location.origin === " + json.dumps(panel_origin) + " && location.pathname === '/'", timeout=10000)
                bridge_page.wait_for_function(
                    "() => document.querySelector('#toast').textContent.includes('CAPTCHA автоматически передана')",
                    timeout=10000,
                )
            except Exception as error:
                raise AssertionError(f"bridge failed to return to configured panel: {bridge_page.url}") from error
            assert bridge_page.url.startswith(panel_origin + "/"), (
                f"bridge did not return to the configured trusted panel origin: {bridge_page.url}"
            )
            bridge_posts = [
                json.loads(raw) for method, path, raw in calls
                if method == "POST" and path == "/captcha/result" and raw
            ]
            assert bridge_posts and bridge_posts[-1] == {
                "sessionToken": "fixture-bridge-session",
                "result": "fixture-bridge-success-token",
            }, f"CAPTCHA bridge did not reach panel same-origin API: {bridge_posts!r}"
            bridge_page.close()

            # Missing or malformed trusted configuration must fail closed even
            # when an attacker supplies both a destination fragment and referrer.
            for configured_origin in ("", "https://panel.example.com/untrusted/path"):
                blocked_page = browser.new_page()
                blocked_page.add_init_script("""
                  Object.defineProperty(document, 'referrer', { configurable: true, get: () => 'https://attacker.example/' });
                  Object.defineProperty(window, 'browser', { configurable: true, value: { storage: { local: {
                    get: (key) => Promise.resolve({ trustedPanelOrigin: CONFIGURED_ORIGIN })
                  } } } });
                """.replace("CONFIGURED_ORIGIN", json.dumps(configured_origin)) + "\n" +
                    origin_policy_source + "\n" + bridge_main_source + "\n" + bridge_isolated_source)
                blocked_page.route(vk_origin + "/**", vk_bridge_fixture("must-not-redirect"))
                blocked_page.goto(
                    vk_origin + "/captcha-fixture#"
                    + "lalune_panel=https%3A%2F%2Fattacker.example"
                    + "&lalune_session=fixture-blocked-session",
                    wait_until="domcontentloaded",
                )
                blocked_page.evaluate("() => { void fetch('/captchaNotRobot.check'); }")
                blocked_page.wait_for_timeout(300)
                assert blocked_page.url.startswith(vk_origin + "/"), (
                    f"bridge redirected without a valid configured origin: {blocked_page.url}"
                )
                blocked_page.close()

            browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
    print("CSQTT_CHROMIUM_UI_SMOKE_PASS widths=320,360,390,768,1280 tabs=7 singbox-config-api=PASS profile-api=PASS connect-disconnect=PASS tunnel-settings=PASS direct-settings-api=PASS deploy-api=PASS socks5-live-status=PASS socks5-address-hidden-when-inactive=PASS logs=PASS captcha-bridge-trusted-origin=PASS captcha-bridge-fail-closed=PASS")


if __name__ == "__main__":
    main()
