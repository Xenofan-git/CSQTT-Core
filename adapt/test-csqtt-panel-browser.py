#!/usr/bin/env python3
"""Chromium UI smoke and API-contract interaction tests for the CSQTT panel.

The actual panel is served over HTTP. API calls are intercepted with a deterministic
fixture, so profile selection/settings persistence are exercised without touching
a real account, VK token, server, or network.
"""
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import threading

from playwright.sync_api import sync_playwright

ROOT = Path(".").resolve()
HTML = ROOT / "adapt" / "csqtt-panel-preview.html"
EXPECTED = ["connect", "settings", "deploy", "logs", "direct", "route", "singbox"]
WIDTHS = [390, 768, 1280, 1440, 1920]


def choose_theme(page, mode):
    """Select light/dark using the actual bulb-menu interaction."""
    assert mode in ("light", "dark")
    # Some legacy color-regression checks execute with a blocking modal
    # visible. In those tests the header cannot be clicked by design.
    if page.locator(".overlay:not(.hidden)").count():
        page.locator(f'[data-theme-choice="{mode}"]').evaluate("(button) => button.click()")
    else:
        page.locator("#themeMode").click()
        page.locator(f'[data-theme-choice="{mode}"]').click()
    assert page.locator("#themeChooser").get_attribute("open") is None
    assert page.evaluate("document.documentElement.dataset.theme") == mode


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


def main():
    profiles = [
        {"id": 1, "name": "Profile one", "protocol": "CSQTT", "hashes": "hash-one", "peer": "203.0.113.15"},
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
    singbox_saved = {}
    settings = {
        "authMode": "manual", "workers": 9, "autoApiWorkers": 9,
        "obfs": "video", "fingerprint": "firefox", "turnTransport": "udp",
        "captchaMode": "auto", "clientIds": "8202606,6287487",
        "directDomains": "", "directIPs": "", "deviceId": "fixture-device",
    }
    socks5_state = {"interface":"eth1","protocol":"SOCKS5","auth":"no-auth","monitoring":True,"listening":True,"address":"192.168.4.26:12500"}
    calls = []
    deploy_auth_saved = {}  # backend fixture keeps secrets across browser reloads
    ssh_password_saved = {}  # root-only SSH store emulated for browser checks
    fail_paths = set()
    fail_http_status = {}
    core_release_available = {"value": False}
    core_update_server_state = {"value":"idle"}
    fail_requests = set()  # (verb, path): inject only a readback failure, not a failed save.
    readback_unconfigured = {"value": False}

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
        if path in fail_paths or (method, path) in fail_requests or path in fail_http_status:
            status_code=fail_http_status.get(path,503)
            route.fulfill(status=status_code, content_type="application/json", body=json.dumps({"error": "fixture unavailable"}))
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
            body = {"configured": True, "config": singbox_saved or {"ListenAddress":"127.0.0.1:19180","CSQTTAddress":"127.0.0.1:12500","CSQTTEnabled":False,"FinalOutbound":"socks-hydra","DirectDomains":["example.ru"],"DirectIPCIDRs":[],"SOCKS5":[{"ID":"hydra","Server":"192.0.2.10","ServerPort":1080,"Enabled":True},{"ID":"disabled","Server":"192.0.2.11","ServerPort":1080,"Enabled":False}],"Rules":[]}}
            if readback_unconfigured["value"]:
                body = {"configured": False}
        elif path == "/singbox/config" and method == "PUT":
            singbox_saved.update(json.loads(request.post_data or "{}"))
            body = {"ok": True, "applied": True}
        elif path == "/singbox/logs":
            body = "fixture sing-box log"
        elif path == "/singbox/start" and method == "POST":
            body = {"ok": True}
        elif path == "/singbox/stop" and method == "POST":
            body = {"ok": True}
        elif path == "/socks5/status":
            body = dict(socks5_state)
        elif path == "/deploy/ssh-credentials" and method == "GET":
            from urllib.parse import parse_qs
            host=parse_qs(urlparse(request.url).query).get("host",[""])[0].lower()
            body={"configured":bool(ssh_password_saved.get(host))}
        elif path == "/deploy/ssh-credentials" and method == "PUT":
            content=json.loads(request.post_data or "{}")
            assert set(content)=={"host","password"}
            assert content["password"] and len(content["password"])<=1024
            ssh_password_saved[content["host"].lower()]=content["password"]
            body={"ok":True,"configured":True}
        elif path == "/deploy/ssh-credentials" and method == "DELETE":
            from urllib.parse import parse_qs
            host=parse_qs(urlparse(request.url).query).get("host",[""])[0].lower()
            ssh_password_saved.pop(host,None)
            body={"ok":True}
        elif path == "/deploy/credentials" and method == "GET":
            from urllib.parse import parse_qs
            host = parse_qs(urlparse(request.url).query).get("host", [""])[0].lower()
            saved = deploy_auth_saved.get(host)
            body = {"configured": bool(saved), "webLogin":saved["webLogin"] if saved else ""}
            assert "mainPassword" not in body and "webPassword" not in body
        elif path == "/deploy/credentials" and method == "PUT":
            content = json.loads(request.post_data or "{}")
            assert set(content) == {"host","mainPassword","webLogin","webPassword"}
            host = content["host"].lower()
            deploy_auth_saved[host] = content
            body = {"ok":True,"configured":True}
        elif path == "/deploy/credentials" and method == "DELETE":
            from urllib.parse import parse_qs
            host=parse_qs(urlparse(request.url).query).get("host",[""])[0].lower()
            deploy_auth_saved.pop(host,None)
            body = {"ok":True}
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
        elif path == "/updates/core/check" and method == "GET":
            yes = core_release_available["value"]
            body = {
                "repository":"Xenofan-git/CSQTT-Core",
                "installedCommit":"d1cc2b58076413faca283cfa0183d6f33068a571",
                "sourceCommit":"d1cc2b58076413faca283cfa0183d6f33068a571",
                "sourceAhead":False,
                "releaseTag":"csqtt-core-v1.0.0" if yes else "",
                "update":yes,"canInstall":yes,
                "message":"Доступен проверенный релиз" if yes else "Совместимого ARM64-релиза пока нет",
                "error":"",
            }
        elif path == "/updates/core/status" and method == "GET":
            body = {"downloading":core_update_server_state["value"].startswith(("scheduled:","applying:")),"state":core_update_server_state["value"]}
        elif path == "/updates/core/wait" and method == "POST":
            if core_release_available["value"]:
                core_update_server_state["value"] = "scheduled:csqtt-core-v1.0.0"
                body = {"ok":True,"scheduled":True,"releaseTag":"csqtt-core-v1.0.0","message":"Отдельная systemd-служба запущена"}
            else:
                status = 409
                body = {"error":"Нет проверенного ARM64-релиза"}
        elif path == "/updates/lalune":
            body = {"hasUpdate": False}
        else:
            status = 404
            body = {"error": f"unexpected API request: {method} {path}"}
        route.fulfill(status=status, content_type="application/json", body=json.dumps(body))

    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(
                headless=True,
                **({"executable_path": os.environ["CSQTT_TEST_CHROMIUM"]} if os.environ.get("CSQTT_TEST_CHROMIUM") else {}),
            )
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
            assert page.locator("#topHeart, #topSettings").count() == 0, "old donation and gear buttons must be absent"
            assert page.locator("#topSwitch").count() == 1, "profile selector should be retained"
            # Only the bulb icon is visible until activated. Theme choices
            # appear in a small popover next to Profiles, without OS mode.
            theme = page.locator("#themeMode")
            assert theme.count() == 1
            assert page.locator("#page-settings #themeMode").count() == 0
            assert page.locator("#topSwitch + #themeChooser").count() == 1
            assert theme.get_attribute("aria-label") == "Выбрать тему оформления"
            assert theme.locator("svg").count() == 1
            assert page.locator("select#themeMode").count() == 0
            assert page.locator("#themeChooser").get_attribute("open") is None
            assert page.locator(".theme-options").is_hidden()
            theme.click()
            assert page.locator(".theme-options").is_visible()
            assert page.locator("[data-theme-choice]").all_text_contents() == ["Светлая", "Тёмная"]
            assert page.locator("[data-theme-choice]").evaluate_all("(els) => els.map(el => el.dataset.themeChoice)") == ["light", "dark"]
            assert page.locator('[data-theme-choice="light"]').get_attribute("aria-pressed") == "true"
            theme.press("Escape")
            assert page.locator(".theme-options").is_hidden()
            choose_theme(page, "dark")
            assert page.locator('[data-theme-choice="dark"]').get_attribute("aria-pressed") == "true"
            assert page.evaluate("localStorage.getItem('csqtt-theme-mode')") == "dark"
            page.reload(wait_until="domcontentloaded")
            assert page.locator('[data-theme-choice="dark"]').get_attribute("aria-pressed") == "true"
            assert page.evaluate("document.documentElement.dataset.theme") == "dark"
            choose_theme(page, "light")
            page.reload(wait_until="domcontentloaded")
            assert page.locator('[data-theme-choice="light"]').get_attribute("aria-pressed") == "true"
            theme.click()
            assert page.locator(".theme-options").is_visible()
            page.locator(".brand").click()
            assert page.locator(".theme-options").is_hidden()
            # Migrate the previous automatic preference to light explicitly.
            page.evaluate("localStorage.setItem('csqtt-theme-mode', 'system')")
            page.emulate_media(color_scheme="dark")
            page.reload(wait_until="domcontentloaded")
            assert page.locator('[data-theme-choice="light"]').get_attribute("aria-pressed") == "true"
            assert page.evaluate("localStorage.getItem('csqtt-theme-mode')") == "light"
            assert page.evaluate("document.documentElement.dataset.theme") == "light"
            page.emulate_media(color_scheme="light")
            assert page.evaluate("document.documentElement.dataset.theme") == "light"
            # Header content must not collide even on a 320 px phone.
            for vw in (320, 360, 390, 768, 1280, 1440, 1920):
                page.set_viewport_size({"width":vw, "height":840})
                bounds=page.evaluate("""() => {
                  const bounds=s=>{const b=document.querySelector(s).getBoundingClientRect();return {left:b.left,right:b.right,width:b.width}};
                  return {brand:bounds('.top .brand'), profile:bounds('#topSwitch'),
                    theme:bounds('#themeMode'), github:bounds('#topGitHub'),
                    logout:bounds('.top-logout-button'),
                    scroll:document.documentElement.scrollWidth-document.documentElement.clientWidth};
                }""")
                assert bounds["brand"]["right"] <= bounds["profile"]["left"] + 1, (vw,bounds)
                assert bounds["profile"]["right"] < bounds["theme"]["left"] + 3, (vw,bounds)
                assert bounds["theme"]["right"] < bounds["github"]["left"] + 3, (vw,bounds)
                assert bounds["github"]["right"] < bounds["logout"]["left"] + 3, (vw,bounds)
                assert bounds["scroll"] <= 1, (vw,bounds)
            page.set_viewport_size({"width":1280, "height":850})

            github=page.locator("#topGitHub")
            assert github.count() == 1
            assert github.get_attribute("href") == "https://github.com/Xenofan-git/CSQTT-Core"
            assert github.get_attribute("target") == "_blank"
            assert set((github.get_attribute("rel") or "").split()) >= {"noopener","noreferrer"}
            assert github.get_attribute("aria-label") == "Репозиторий CSQTT Core на GitHub"
            assert github.locator("svg.github-mark path").count() == 1
            assert page.locator("#topQr, #qrImage").count() == 0, "QR controls must remain absent"
            assert page.locator("#power .power-symbol").count() == 1, "the Connect button must use the power icon, not C"
            assert page.locator("#power").inner_text().strip() == "", "old C glyph is still visible"
            navicons = page.locator('.tabs button[data-page="direct"] path').get_attribute("d"), page.locator('.tabs button[data-page="route"] path').get_attribute("d")
            assert navicons[0] != navicons[1], "Direct and Route must have different symbols"
            assert 'm7 4-4 4' in navicons[0], "old Route arrow icon must move to Direct"
            assert 'M3 5.5h18' in navicons[1] and 'M9 16.5h6' in navicons[1], "Route must use decreasing centered bars"
            assert page.locator('.tabs button[data-page="settings"] svg.nav-key').count() == 1
            assert page.locator('.tabs button[data-page="settings"] svg.nav-key circle').count() == 1
            assert page.locator('.tabs button[data-page="settings"] svg.nav-key path').count() == 1
            assert page.locator('.tabs button[data-page="singbox"] svg.nav-singbox-logo').count() == 1
            assert page.locator('.tabs button[data-page="singbox"] svg.nav-singbox-logo path').count() >= 7
            assert 'SagerNet/sing-box' in page.locator('svg.nav-singbox-logo').inner_html()
            nav_style = page.evaluate("""() => {
                const key=document.querySelector('.tabs [data-page="settings"] svg.nav-key');
                const keyPath=key.querySelector('path');
                const logo=document.querySelector('.tabs [data-page="singbox"] svg.nav-singbox-logo');
                const logoTape=logo.querySelectorAll('path')[5];
                return {
                    keyFill:getComputedStyle(keyPath).fill,
                    keyStroke:getComputedStyle(keyPath).stroke,
                    keyWidth:parseFloat(getComputedStyle(key).width),
                    logoWidth:parseFloat(getComputedStyle(logo).width),
                    tapeColor:getComputedStyle(logoTape).fill
                };
            }""")
            assert nav_style['keyFill']=='none' and nav_style['keyStroke']!='none', nav_style
            assert nav_style['keyWidth']>=28 and nav_style['logoWidth']>=28, nav_style
            assert nav_style['tapeColor']!='none', nav_style

            # The release update UI is fail-closed by default and never
            # offers the legacy Endlad2/csqtt-core download path.
            assert page.locator('#updateCore').is_disabled()
            page.locator('button[data-page="settings"]').click()
            page.locator('#checkCoreUpdate').click()
            page.wait_for_function("document.querySelector('#updateMessage').textContent.includes('Совместимого ARM64-релиза пока нет')")
            assert page.locator('#updateCore').is_disabled()
            assert page.locator('#updateMessage').is_visible()
            assert page.locator('#coreUpdatePopup').is_visible()
            assert page.locator('#coreUpdatePopupTitle').inner_text() == "Проверка CSQTT Core завершена"
            assert 'Совместимого ARM64-релиза пока нет' in page.locator('#coreUpdatePopupDescription').inner_text()
            assert 'Endlad2/csqtt-core' not in page.locator('#updateMessage').inner_text()
            page.locator('#coreUpdatePopupClose').click()
            assert page.locator('#coreUpdatePopup').is_hidden()
            assert not any(method == 'POST' and path == '/updates/core/wait' for method,path,_ in calls)
            core_release_available["value"] = True
            page.locator('#checkCoreUpdate').click()
            page.wait_for_function("!document.querySelector('#updateCore').disabled")
            assert page.locator('#updateMessage').inner_text().startswith('Доступен проверенный релиз')
            assert page.locator('#coreUpdatePopupTitle').inner_text() == "Доступно обновление CSQTT Core"
            page.locator('#updateCore').click()
            page.wait_for_function("document.querySelector('#updateMessage').textContent.includes('systemd-служба запущена')")
            assert any(method == 'POST' and path == '/updates/core/wait' for method,path,_ in calls)
            assert page.locator('#updateCore').is_disabled()
            assert 'Обновление CSQTT Core' in page.locator('#coreUpdatePopupTitle').inner_text()
            assert 'успешно обновлён' not in page.locator('#coreUpdatePopupTitle').inner_text()
            # Mimic the panel's restart while a detached updater completes.
            core_update_server_state["value"] = "success:" + "a"*64
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#coreUpdatePopupTitle').textContent==='CSQTT Core успешно обновлён'")
            assert page.locator('#coreUpdatePopup').is_visible()
            assert 'aaaaaaaaaaaa' in page.locator('#coreUpdatePopupDescription').inner_text()
            # A failed replacement must never appear as a successful update.
            core_update_server_state["value"] = "rolled-back:" + "b"*64
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#coreUpdatePopupTitle').textContent.includes('восстановлен')")
            assert page.locator('#coreUpdatePopup').get_attribute('data-kind') == "error"
            # An API failure should be a distinct visible error notification.
            core_update_server_state["value"] = "idle"
            fail_paths.add("/updates/core/check")
            page.locator('button[data-page="settings"]').click()
            page.locator('#checkCoreUpdate').click()
            page.wait_for_function("document.querySelector('#coreUpdatePopupTitle').textContent==='Ошибка проверки CSQTT Core'")
            assert page.locator('#coreUpdatePopup').get_attribute('data-kind') == "error"
            fail_paths.discard("/updates/core/check")
            core_release_available["value"] = False

            # Regression for the real tunnel/profile UI controls.
            # Switch to the *visible* Tunnel page before measuring element
            # geometry. Hidden pages return zero rectangles in Chromium.
            # CSS must make a single chevron visible instead of OS-native
            # double scrollbar/dropdown controls, including dark theme.
            page.locator('button[data-page="settings"]').click()
            page.wait_for_function("document.querySelector('#page-settings').classList.contains('active')")
            for theme in ("light", "dark"):
                page.evaluate("(theme) => document.documentElement.dataset.theme = theme", theme)
                controls = page.evaluate("""() => {
                  const first = document.querySelector('#newProtocol');
                  const tunnel = document.querySelector('#authMode');
                  const toggle = document.querySelector('#allowHashRedistribution');
                  const text = toggle.closest('label').querySelector('span');
                  const arrow = getComputedStyle(tunnel);
                  const rt = toggle.getBoundingClientRect(), rl = text.getBoundingClientRect();
                  return {
                    profileAppearance: getComputedStyle(first).appearance,
                    profileChevron: getComputedStyle(first).backgroundImage.includes('svg'),
                    tunnelAppearance: arrow.appearance,
                    tunnelChevron: arrow.backgroundImage.includes('svg'),
                    minWorkers: document.querySelector('#workers').min,
                    autoWorkers: (() => {
                      const el=document.querySelector('#autoApiWorkers'), cs=getComputedStyle(el);
                      const numberInputs=[...document.querySelectorAll('input[type="number"]')]
                        .map(n=>({id:n.id,appearance:getComputedStyle(n).appearance}));
                      const rules=[...document.styleSheets].flatMap(sheet=>[...sheet.cssRules])
                        .filter(rule=>rule.selectorText?.includes('#autoApiWorkers::-webkit-slider'))
                        .map(rule=>({selector:rule.selectorText,height:rule.style.height,
                          width:rule.style.width,marginTop:rule.style.marginTop,
                          radius:rule.style.borderRadius}));
                      return {type:el.type,min:el.min,max:el.max,step:el.step,value:el.value,
                        border:cs.borderTopWidth,appearance:cs.appearance,
                        label:document.querySelector('#autoApiWorkersValue').textContent,
                        markers:[...document.querySelectorAll('.auto-api-workers-field .range-endpoints span')].map(e=>e.textContent),
                        numberInputs,rules};
                    })(),
                    toggleLeftOfDescription: rt.right < rl.left,
                    palette: getComputedStyle(document.documentElement).getPropertyValue('--csqtt-accent').trim(),
                    formRadii: [...new Set([...document.querySelectorAll(
                      'input:not([type="checkbox"]):not([type="radio"]):not([type="range"]):not([type="hidden"]):not([type="button"]):not([type="submit"]):not([type="reset"]),select,textarea,.password-inline'
                    )].map(el=>getComputedStyle(el).borderTopLeftRadius))],
                    formBorders: [...document.querySelectorAll(
                      'input:not([type="checkbox"]):not([type="radio"]):not([type="range"]):not([type="hidden"]):not([type="button"]):not([type="submit"]):not([type="reset"]):not(.password-inline input),select,textarea,.password-inline'
                    )].map(el=>({id:el.id,width:getComputedStyle(el).borderTopWidth})),
                    passwordInnerBorder: getComputedStyle(document.querySelector('.password-inline input')).borderTopWidth,
                    passwordOuterBorder: getComputedStyle(document.querySelector('.password-inline')).borderTopWidth,
                    focusedFieldOutline: (() => {const field=document.querySelector('#vkHashes');field.focus();return getComputedStyle(field).outlineWidth;})(),
                    fieldCount: document.querySelectorAll('input:not([type="checkbox"]):not([type="radio"]):not([type="range"]):not([type="hidden"]):not([type="button"]):not([type="submit"]):not([type="reset"]),select,textarea,.password-inline').length,
                    switchRadius: getComputedStyle(toggle).borderTopLeftRadius,
                    sliderRadius: getComputedStyle(document.querySelector('#workers')).borderTopLeftRadius
                  };
                }""")
                assert controls["profileAppearance"] == "none" and controls["profileChevron"], controls
                assert controls["tunnelAppearance"] == "none" and controls["tunnelChevron"], controls
                assert controls["minWorkers"] == "9", controls
                auto=controls["autoWorkers"]
                assert (auto["type"],auto["min"],auto["max"],auto["step"]) == ("range","9","27","1"), auto
                assert auto["value"] == "9" and auto["label"] == "9", auto
                assert auto["markers"] == ["9","27"], auto
                assert auto["border"] == "0px" and auto["appearance"] == "none", auto
                assert any('runnable-track' in rule["selector"] and rule["height"]=="4px"
                           for rule in auto["rules"]), auto
                assert any('slider-thumb' in rule["selector"] and rule["width"]=="20px"
                           and rule["height"]=="20px" and rule["marginTop"]=="-8px"
                           and rule["radius"]=="50%" for rule in auto["rules"]), auto
                assert auto["numberInputs"] and all(n["appearance"]=="textfield" for n in auto["numberInputs"]), auto

                assert controls["fieldCount"] >= 40 and controls["formRadii"] == ["12px"], controls
                assert all(field["width"] == "1px" or (field["id"] == "panelLink" and field["width"] == "0px")
                           for field in controls["formBorders"]), controls
                assert controls["passwordInnerBorder"] == "0px" and controls["passwordOuterBorder"] == "1px", controls
                assert controls["focusedFieldOutline"] == "1px", controls
                assert controls["switchRadius"] != "12px" and controls["sliderRadius"] != "12px", controls
                assert controls["toggleLeftOfDescription"], controls
                fresh = page.evaluate("""() => {
                  const r=document.querySelector('#workers'),p=document.querySelector('#fingerprint');
                  const sw=[...document.querySelectorAll('#allowHashRedistribution,#validateVkHashes')].map(el=>({
                    width:el.getBoundingClientRect().width,
                    height:el.getBoundingClientRect().height,
                    border:getComputedStyle(el).borderTopWidth,
                    knobWidth:getComputedStyle(el,'::after').width,
                    knobTop:getComputedStyle(el,'::after').top
                  }));
                  const rs=getComputedStyle(r);
                  const css=[...document.styleSheets].flatMap(sheet=>[...sheet.cssRules])
                    .filter(rule=>rule.selectorText?.includes('#workers::-webkit-slider'))
                    .map(rule=>({selector:rule.selectorText,
                                height:rule.style.height,width:rule.style.width,
                                marginTop:rule.style.marginTop,
                                radius:rule.style.borderRadius}));
                  return {min:r.min,value:r.value,border:rs.borderTopWidth,
                    appearance:rs.appearance,trackRules:css,
                    switch:sw,
                    browsers:[...p.options].map(o=>({value:o.value,text:o.textContent}))};
                }""")
                assert fresh["min"] == "9" and int(fresh["value"]) >= 9, fresh
                assert fresh["border"] == "0px" and fresh["appearance"] == "none", fresh
                assert any("runnable-track" in rule["selector"] and rule["height"] == "4px"
                           for rule in fresh["trackRules"]), fresh
                assert any("slider-thumb" in rule["selector"] and rule["width"] == "20px"
                           and rule["height"] == "20px" and rule["marginTop"] == "-8px"
                           and rule["radius"] == "50%"
                           for rule in fresh["trackRules"]), fresh
                assert all(s["width"] == 52 and s["height"] == 28 and s["border"] == "1px" and s["knobWidth"] == "20px" for s in fresh["switch"]), fresh
                assert fresh["browsers"] == [
                    {"value":"firefox","text":"Firefox"},
                    {"value":"chrome","text":"Chrome"},
                    {"value":"safari","text":"Safari"}], fresh

                assert controls["palette"] == ("#4195ff" if theme == "dark" else "#1e73e8"), controls
            page.evaluate("document.documentElement.dataset.theme = 'light'")
            # The original CSQTT option has no explicit value= attribute;
            # option.value is synthesized by the DOM from its text.
            assert page.locator('#newProtocol option').evaluate_all(
                "(options) => options.map(option => option.value)"
            ) == ['CSQTT','FreeTurn','OlcRTC','OpenFlux','ToTS']
            page.locator('button[data-page="connect"]').click()

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
                if name == "singbox":
                    assert page.locator("#page-deploy #socks5Address").count() == 0
                    assert page.locator("#page-singbox #socks5CsqttRuntime").count() == 1
                    page.wait_for_function("document.querySelector('#socks5Address').value === '192.168.4.26:12500'")
                    assert page.locator("#socks5State").inner_text() == "Слушатель активен"
                    assert page.locator("#copySocks5Address").is_enabled()
                    assert any(method == "GET" and path == "/socks5/status" for method, path, _ in calls)
                    socks5_state["listening"] = False
                    socks5_state["address"] = ""
                    page.evaluate("() => window.dispatchEvent(new Event('focus'))")
                    page.locator('button[data-page="connect"]').click()
                    page.locator('button[data-page="singbox"]').click()
                    page.wait_for_function("document.querySelector('#socks5Address').value === '—'")
                    assert page.locator("#copySocks5Address").is_disabled()
                    socks5_state["listening"] = True
                    socks5_state["address"] = "192.168.4.26:12500"
                if name == "direct":
                    # Direct editor is a regular page, not a fixed full-screen overlay.
                    assert page.locator("#page-direct .route-dialog").evaluate("el => getComputedStyle(el).position") == "static"
                    assert page.locator("#page-direct").evaluate("el => getComputedStyle(el).position") == "static"
                    page.locator("#routeClose").click()
                    assert page.locator("#page-route").evaluate("el => el.classList.contains('active')")

            page.locator('button[data-page="route"]').click()
            assert page.locator("#page-route").evaluate("el => el.classList.contains('active')")
            page.wait_for_function("document.querySelectorAll('#singboxRouteRuleEditor .route-rule').length === 2")
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert route_cards.count() == 2, "Route must start with fixed DIRECT and CSQTT system cards"
            assert route_cards.nth(0).get_attribute("data-system-rule") == "direct"
            assert route_cards.nth(1).get_attribute("data-system-rule") == "csqtt"
            assert route_cards.nth(0).locator("[data-move-route-rule]").count() == 0
            assert route_cards.nth(0).locator("[data-remove-route-rule]").count() == 0
            # System rule toggles belong on their cards, not only in the editor.
            direct_card_toggle = route_cards.nth(0).locator('[data-toggle-route-rule="system-direct"]')
            csqtt_card_toggle = route_cards.nth(1).locator('[data-toggle-route-rule="system-csqtt"]')
            assert direct_card_toggle.count() == 1 and csqtt_card_toggle.count() == 1
            direct_card_toggle.check()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]").is_checked()
            direct_card_toggle = page.locator('[data-toggle-route-rule="system-direct"]')
            direct_card_toggle.uncheck()
            assert not page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]").is_checked()
            page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]").check()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").input_value() == "example.ru", (
                "legacy DirectDomains must migrate into the fixed DIRECT rule editor"
            )
            page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").fill("example.ru\ndirect-route.example")
            page.locator("#singboxAddRouteRule").click()
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert route_cards.count() == 3
            # Mobile regression: Route must not inflate the approved 390px phone canvas.
            original_viewport = page.viewport_size
            page.set_viewport_size({"width": 390, "height": 844})
            width_state = page.evaluate("""() => ({
                viewport: window.innerWidth,
                doc: document.documentElement.scrollWidth,
                queue: document.querySelector('#singboxRouteRuleEditor .route-rule-list').getBoundingClientRect().width,
                cardOverflows: [...document.querySelectorAll('#singboxRouteRuleEditor .route-rule')].some(card => card.scrollWidth > card.clientWidth + 1)
            })""")
            assert width_state["doc"] <= width_state["viewport"], f"Route document overflow on mobile: {width_state}"
            assert width_state["queue"] <= width_state["viewport"] and not width_state["cardOverflows"], width_state
            page.set_viewport_size(original_viewport)
            route_cards.nth(2).locator('[data-select-route-rule]').click()
            page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").fill("user-route.example")
            page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Outbound\"]").select_option("socks-hydra")
            route_cards.nth(1).locator('[data-select-route-rule]').click()
            page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]").check()
            page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").fill("csqtt-route.example")
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Outbound\"]").count() == 0, "CSQTT outbound must be fixed"
            assert page.locator('#singboxRouteRuleEditor .route-rule-editor').get_by_text("Outbound: CSQTT (фиксированный)").is_visible()
            # Route Rule Sets must support independent mobile checkboxes and filtering.
            catalog = [
                {"Type":"remote","Tag":"geo-ru","Format":"binary","URL":"https://example.org/geo-ru.srs"},
                {"Type":"remote","Tag":"geo-foreign","Format":"binary","URL":"https://example.org/geo-foreign.srs"},
            ]
            page.evaluate("(catalog) => { document.getElementById('singboxRuleSets').value = JSON.stringify(catalog) }", catalog)
            page.locator('.route-rule-editor [data-field="Domains"]').evaluate("(el) => el.dispatchEvent(new Event('change', {bubbles:true}))")
            page.locator("#singboxRenderRouteRules").evaluate("(el) => el.click()")
            page.locator('.route-rule-editor [data-ruleset-search]').fill("geo-ru")
            assert page.locator('.route-rule-editor .route-ruleset-option:visible').count() == 1
            page.locator('.route-rule-editor [data-ruleset-tag="geo-ru"]').check()
            page.locator('.route-rule-editor [data-ruleset-search]').fill("geo-foreign")
            assert page.locator('.route-rule-editor .route-ruleset-option:visible').count() == 1
            page.locator('.route-rule-editor [data-ruleset-tag="geo-foreign"]').check()
            page.locator('.route-rule-editor [data-ruleset-search]').fill("")
            assert page.locator('.route-rule-editor [data-ruleset-tag="geo-ru"]').is_checked(), "search filtered away an existing Rule Set selection"
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            route_cards.nth(0).locator('[data-select-route-rule]').click()
            page.locator('.route-rule-editor [data-ruleset-tag="geo-ru"]').check()
            route_cards.nth(1).locator('[data-select-route-rule]').click()
            assert page.locator('.route-rule-editor [data-ruleset-tag="geo-ru"]').is_checked()
            assert page.locator('.route-rule-editor [data-ruleset-tag="geo-foreign"]').is_checked()
            # Deleted registry entries remain visible as warnings, so opening
            # the editor never silently discards a referenced Rule Set.
            page.evaluate("(catalog) => { document.getElementById('singboxRuleSets').value = JSON.stringify(catalog) }", catalog[:1])
            page.locator("#singboxRenderRouteRules").evaluate("(el) => el.click()")
            assert page.locator('.route-rule-editor [data-ruleset-tag="geo-foreign"]').is_checked()
            assert "нет в реестре" in page.locator('.route-rule-editor .route-ruleset-option').filter(has_text="geo-foreign").inner_text()
            page.evaluate("(catalog) => { document.getElementById('singboxRuleSets').value = JSON.stringify(catalog) }", catalog)
            page.locator("#singboxRenderRouteRules").evaluate("(el) => el.click()")
            # Searchable category chips must remain within the phone viewport.
            page.set_viewport_size({"width":390,"height":844})
            assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
            page.set_viewport_size(original_viewport)
            # CSQTT starts immediately after DIRECT; priority controls can move it below user rules and restore it.
            route_cards.nth(1).locator("[data-move-route-rule]").last.click()
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert [route_cards.nth(i).get_attribute("data-rule-id") for i in range(3)] == ["system-direct", "route-1", "system-csqtt"], (
                "CSQTT must be movable below user rules but never above DIRECT"
            )
            route_cards.nth(2).locator("[data-move-route-rule]").first.click()
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert [route_cards.nth(i).get_attribute("data-rule-id") for i in range(3)] == ["system-direct", "system-csqtt", "route-1"]
            route_cards.nth(2).drag_to(route_cards.nth(1))
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert [route_cards.nth(i).get_attribute("data-rule-id") for i in range(3)] == ["system-direct", "route-1", "system-csqtt"]
            route_cards.nth(1).drag_to(route_cards.nth(2))
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert [route_cards.nth(i).get_attribute("data-rule-id") for i in range(3)] == ["system-direct", "system-csqtt", "route-1"]
            route_cards.nth(1).locator('[data-select-route-rule]').click()
            toggle = page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]")
            toggle.uncheck()
            toggle.check()
            page.locator("#singboxSave").click()
            page.wait_for_function("document.querySelector('#singboxConfigMessage').textContent.includes('Конфигурация применена: SOCKS5 sing-box проверен.')")
            assert singbox_saved["Rules"][0]["ID"] == "system-direct" and singbox_saved["Rules"][0]["Domains"] == ["example.ru", "direct-route.example"] and singbox_saved["Rules"][0]["Enabled"] is True
            assert singbox_saved["Rules"][1]["ID"] == "system-csqtt" and singbox_saved["Rules"][1]["Enabled"] is True and singbox_saved["Rules"][1]["Domains"] == ["csqtt-route.example"], f"saved CSQTT rule mismatch: {json.dumps(singbox_saved.get('Rules', []), ensure_ascii=False)}"
            assert singbox_saved["Rules"][2]["Domains"] == ["user-route.example"] and singbox_saved["Rules"][2]["Outbound"] == "socks-hydra"
            assert singbox_saved["Rules"][0]["RuleSets"] == ["geo-ru"]
            assert singbox_saved["Rules"][1]["RuleSets"] == ["geo-ru", "geo-foreign"]
            assert [x["Tag"] for x in singbox_saved["RuleSets"]] == ["geo-ru", "geo-foreign"]
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#profileList .profile') !== null")
            page.locator('button[data-page="route"]').click()
            page.wait_for_function("document.querySelectorAll('#singboxRouteRuleEditor .route-rule').length === 3")
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            assert [route_cards.nth(i).get_attribute("data-rule-id") for i in range(3)] == ["system-direct", "system-csqtt", "route-1"]
            route_cards.nth(0).locator('[data-select-route-rule]').click()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]").is_checked()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").input_value() == "example.ru\ndirect-route.example"
            route_cards.nth(1).locator('[data-select-route-rule]').click()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Enabled\"]").is_checked()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").input_value() == "csqtt-route.example"
            assert page.locator('.route-rule-editor [data-ruleset-tag="geo-ru"]').is_checked()
            assert page.locator('.route-rule-editor [data-ruleset-tag="geo-foreign"]').is_checked()
            route_cards.nth(2).locator('[data-select-route-rule]').click()
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Outbound\"]").input_value() == "socks-hydra"
            assert page.locator("#singboxRouteRuleEditor .route-rule-editor [data-field=\"Domains\"]").input_value() == "user-route.example"
            # The limit counts user rules only; DIRECT and CSQTT system cards are excluded.
            for _ in range(15):
                page.locator("#singboxAddRouteRule").click()
            route_cards = page.locator("#singboxRouteRuleEditor .route-rule")
            user_count = route_cards.evaluate_all("cards => cards.filter(card => !card.dataset.systemRule).length")
            assert user_count == 15, f"user route rule limit mismatch: {user_count}"
            page.locator("#singboxAddRouteRule").click()
            user_count = page.locator("#singboxRouteRuleEditor .route-rule").evaluate_all("cards => cards.filter(card => !card.dataset.systemRule).length")
            assert user_count == 15, "UI accepted a 16th user route rule"

            page.locator('button[data-page="singbox"]').click()
            assert page.locator("#page-singbox").evaluate("el => el.classList.contains('active')")
            assert page.locator("#singboxListen").input_value() == "127.0.0.1:19180"
            assert page.locator("#singboxDirectDomains").input_value() == "", "legacy Direct textarea must be hidden and cleared after migration"
            assert page.locator("#singboxCsqtt").input_value() == "127.0.0.1:12500"
            assert "CSQTT Core" in page.locator("#page-singbox").inner_text()
            assert page.locator("#singboxFinalOutbound").input_value() == "socks-hydra"
            assert page.locator("#singboxDNSMode").input_value() == "local"
            assert page.locator("#singboxDNSStrategy").input_value() == "prefer_ipv4"
            assert page.locator("#singboxDNSUDPFields").is_hidden()
            page.locator("#singboxDNSMode").select_option("udp")
            assert page.locator("#singboxDNSUDPFields").is_visible()
            page.locator("#singboxDNSServer").fill("192.168.4.1")
            page.locator("#singboxDNSPort").fill("53")
            page.locator("#singboxDNSStrategy").select_option("prefer_ipv4")
            outbound_options = page.locator("#singboxFinalOutbound option").all_text_contents()
            assert "socks-hydra" in outbound_options and "CSQTT" in outbound_options and "socks-disabled" not in outbound_options, outbound_options
            page.locator('button[data-page="route"]').click()
            page.locator("#singboxRouteRuleEditor .route-rule").first.locator('[data-select-route-rule]').click()
            page.locator('#singboxRouteRuleEditor .route-rule-editor [data-field="Domains"]').fill("example.ru\napi.example.ru")
            page.locator('button[data-page="singbox"]').click()
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function("document.querySelector('#singboxConfigMessage').textContent.includes('SOCKS5 sing-box проверен')")
            assert any(method == "PUT" and path == "/singbox/config" for method, path, _ in calls)
            assert "CSQTTEnabled" not in singbox_saved and "CSQTTExplicitlyDisabled" not in singbox_saved
            assert singbox_saved.get("CSQTTAddress") == "127.0.0.1:12500"
            assert singbox_saved["Rules"][1]["ID"] == "system-csqtt" and singbox_saved["Rules"][1]["Domains"] == ["csqtt-route.example"], f"Route rules payload mismatch: {singbox_saved.get('Rules')!r}"
            assert singbox_saved.get("FinalOutbound") == "socks-hydra"
            assert singbox_saved.get("DNSMode") == "udp" and singbox_saved.get("DNSServer") == "192.168.4.1", f"DNS payload mismatch: {singbox_saved!r}"
            assert singbox_saved.get("DNSPort") == 53 and singbox_saved.get("DNSStrategy") == "prefer_ipv4"
            assert [x["ID"] for x in singbox_saved.get("SOCKS5", [])] == ["hydra", "disabled"]
            assert page.locator("#singboxLogs").inner_text() == "fixture sing-box log"
            page.locator("#singboxStart").click()
            assert any(method == "POST" and path == "/singbox/start" for method, path, _ in calls)

            # Both Route and Sing-Box need their own unique live status node.
            assert page.locator("#routeConfigMessage").count()==1
            assert page.locator("#singboxConfigMessage").count()==1
            assert page.locator("#singboxConfigMessage").is_visible()
            # PUT may succeed even when the follow-up GET fails. Never report
            # "verified" without reading the saved config back successfully.
            fail_requests.add(("GET", "/singbox/config"))
            prior_put_count = len([1 for method,path,_ in calls
                                   if method=="PUT" and path=="/singbox/config"])
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function(
                """() => document.querySelector('#singboxConfigMessage').textContent.includes('повторное чтение')
                   && document.querySelector('#routeConfigMessage').textContent.includes('повторное чтение')"""
            )
            message=page.locator("#singboxConfigMessage").first.inner_text()
            assert not "проверена sing-box" in message, (
                "READBACK FAILURE MISREPORTED AS SUCCESS:",message)
            assert page.locator("#singboxConfigMessage").first.evaluate(
                "el => el.classList.contains('error')"), message
            assert page.locator("#routeConfigMessage").inner_text()==message
            assert page.locator("#routeConfigMessage").evaluate(
                "el => el.classList.contains('error')")
            assert len([1 for method,path,_ in calls
                        if method=="PUT" and path=="/singbox/config"])==prior_put_count+1
            fail_requests.clear()
            # HTTP 200 with "configured":false also cannot confirm the saved
            # data, even though both requests technically succeeded.
            readback_unconfigured["value"] = True
            page.locator("#singboxConfigMessage").evaluate(
                "el => { el.textContent='Ожидание ответа'; el.className='notice' }")
            page.locator("#routeConfigMessage").evaluate(
                "el => { el.textContent='Ожидание ответа'; el.className='notice' }")
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function(
                """() => document.querySelector('#singboxConfigMessage').classList.contains('error')
                   && document.querySelector('#singboxConfigMessage').textContent.includes('не подтвердило')"""
            )
            assert page.locator("#routeConfigMessage").evaluate(
                "el => el.classList.contains('error')")
            readback_unconfigured["value"] = False
            # A retry after transient readback outage must recover.
            page.locator("#singboxConfigMessage").evaluate(
                "el => { el.textContent='Ожидание ответа'; el.className='notice' }")
            page.locator("#routeConfigMessage").evaluate(
                "el => { el.textContent='Ожидание ответа'; el.className='notice' }")
            page.locator("#singboxSaveEngine").click()
            page.wait_for_function(
                "() => document.querySelector('#singboxConfigMessage').textContent.includes('SOCKS5 sing-box проверен')"
            )
            assert page.locator("#routeConfigMessage").evaluate(
                "el => el.classList.contains('success')")

            # A responsive app is essential: desktop uses a wide shell and
            # full-width seven-tab dock; phones fit the real viewport with
            # legible navigation and no clipped labels or horizontal scroll.
            for width in WIDTHS:
                page.set_viewport_size({"width": width, "height": 850})
                page.wait_for_timeout(70)
                dimensions = page.evaluate("""() => {
                  const body = document.body.getBoundingClientRect();
                  const nav = document.querySelector('.tabs').getBoundingClientRect();
                  const hero = document.querySelector('.connect-hero').getBoundingClientRect();
                  const header = document.querySelector('.top').getBoundingClientRect();
                  const brand = document.querySelector('.brand').getBoundingClientRect();
                  const actions = document.querySelector('.top-actions').getBoundingClientRect();
                  const tabs = [...document.querySelectorAll('.tabs .tab')].map(tab => {
                    const r = tab.getBoundingClientRect();
                    const style=getComputedStyle(tab);
                    const ico=tab.querySelector('.ico');
                    const svg=ico.querySelector('svg');
                    return {left:r.left,right:r.right,client:tab.clientWidth,
                            scroll:tab.scrollWidth, fontSize:parseFloat(style.fontSize),
                            fontWeight:style.fontWeight,
                            textTransform:style.textTransform,
                            iconSize:parseFloat(getComputedStyle(ico).fontSize),
                            svgSize:svg?parseFloat(getComputedStyle(svg).width):null};
                  });
                  return {
                    viewport: document.documentElement.clientWidth,
                    overflow: document.documentElement.scrollWidth-document.documentElement.clientWidth,
                    body: {left:body.left,width:body.width,right:body.right},
                    nav: {left:nav.left,width:nav.width,right:nav.right},
                    hero: {left:hero.left,width:hero.width,right:hero.right},
                    header: {left:header.left,right:header.right},
                    brand: {right:brand.right},actions: {left:actions.left},
                    tabs
                  };
                }""")
                vw = dimensions["viewport"]
                assert dimensions["overflow"] <= 1, (
                    f"horizontal overflow at viewport {width}: {dimensions}")
                target = min(1080, vw)
                assert abs(dimensions["body"]["width"] - target) <= 1, (
                    f"body width is not responsive at {width}: {dimensions}")
                assert abs(dimensions["body"]["left"] - (vw-target)/2) <= 1, (
                    f"body not centered at {width}: {dimensions}")
                assert dimensions["nav"]["width"] >= min(300,vw-50), (
                    f"floating nav collapsed at {width}: {dimensions}")
                assert dimensions["nav"]["left"] >= 0 and dimensions["nav"]["right"] <= vw+1, (
                    f"nav outside screen at {width}: {dimensions}")
                assert dimensions["hero"]["left"] >= 0 and dimensions["hero"]["right"] <= vw+1, (
                    f"connection card outside screen at {width}: {dimensions}")
                assert dimensions["brand"]["right"] <= dimensions["actions"]["left"]+2, (
                    f"header actions overlap brand at {width}: {dimensions}")
                assert len(dimensions["tabs"]) == 7, (
                    f"lost one of seven tabs at {width}: {dimensions}")
                for tab in dimensions["tabs"]:
                    assert tab["scroll"] <= tab["client"]+2, (
                        f"tab label is clipped at {width}: {tab}")
                    assert tab["textTransform"] == "uppercase", (
                        f"tab caption is not uppercase at {width}: {tab}")
                    assert tab["fontWeight"] in ("450", "400"), (
                        f"tab font should be lighter at {width}: {tab}")
                    expected_label_size = 12 if width <= 700 else 14
                    assert tab["fontSize"] == expected_label_size, (
                        f"tab label size unexpected at {width}: {tab}")
                    assert tab["iconSize"] == (27 if width <= 700 else 30), (
                        f"tab icon did not grow at {width}: {tab}")
                    if tab["svgSize"] is not None:
                        assert tab["svgSize"] == (28 if width <= 700 else 31), (
                            f"tab svg icon did not grow at {width}: {tab}")

            # Deploy action group must align exactly with the nearby SOCKS5
            # field/card's inner edges, rather than stretching beyond the cards.
            for width in WIDTHS:
                page.set_viewport_size({"width": width, "height": 850})
                page.locator('button[data-page="deploy"]').click()
                dims=page.evaluate("""() => {
                  const rect=selector=>{
                    const r=document.querySelector(selector).getBoundingClientRect();
                    return {left:r.left,right:r.right,width:r.width,height:r.height,top:r.top};
                  };
                  return {
                    target:rect('#page-deploy>.card:first-of-type .field'),
                    group:rect('#page-deploy .deploy-action-group'),
                    delete:rect('#uninstallCsqtt'),
                    install:rect('#startDeploy'),
                    authorize:rect('#editDeployAuth'),
                    status:rect('#deployState'),
                    overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth,
                  };
                }""")
                target=dims["target"]
                for name in ("authorize",):
                    button=dims[name]
                    assert abs(target["left"]-button["left"]) <= 1.5, (width,name,dims)
                    assert abs(target["right"]-button["right"]) <= 1.5, (width,name,dims)
                assert abs(target["left"]-dims["delete"]["left"]) <= 1.5, (width,dims)
                assert abs(target["right"]-dims["install"]["right"]) <= 1.5, (width,dims)
                assert dims["delete"]["right"]+6 < dims["install"]["left"], (width,dims)
                assert dims["delete"]["height"] >= 50 and dims["install"]["height"] >= 50, (width,dims)
                assert dims["authorize"]["height"] >= 50 and dims["overflow"] <= 1, (width,dims)
                assert abs(target["left"]-dims["status"]["left"]) <= 1.5, (width,dims)
                assert abs(target["right"]-dims["status"]["right"]) <= 1.5, (width,dims)
                assert dims["status"]["top"] >= dims["authorize"]["top"] + dims["authorize"]["height"], (width,dims)
                # Deploy buttons must be structurally and visually inside the SSH card.
                card_check=page.evaluate("""() => {
                    const card=document.querySelector('#page-deploy>.card:first-of-type');
                    const group=card.querySelector('.deploy-action-group');
                    const log=document.querySelector('#deployLog').closest('.card');
                    const b=card.getBoundingClientRect(),g=group.getBoundingClientRect(),l=log.getBoundingClientRect();
                    return {within:card.contains(group),cardBottom:b.bottom,groupBottom:g.bottom,
                            cardLeft:b.left,cardRight:b.right,groupLeft:g.left,groupRight:g.right,logTop:l.top,
                            innerLeft:card.querySelector('.field').getBoundingClientRect().left,
                            innerRight:card.querySelector('.field').getBoundingClientRect().right};
                }""")
                assert card_check["within"],(width,card_check)
                assert abs(card_check["innerLeft"]-card_check["groupLeft"]) <= 1.5,(width,card_check)
                assert abs(card_check["innerRight"]-card_check["groupRight"]) <= 1.5,(width,card_check)
                assert card_check["groupBottom"]<card_check["cardBottom"],(width,card_check)
                assert card_check["cardBottom"]<card_check["logTop"],(width,card_check)

            # Check the other three layouts at the same breakpoints.
            for width in WIDTHS:
                page.set_viewport_size({"width":width,"height":850})
                page.locator('button[data-page="direct"]').click()
                d=page.evaluate("""() => {
                  const box=el=>{const r=el.getBoundingClientRect();return {x:r.x,y:r.y,right:r.right,bottom:r.bottom,width:r.width,height:r.height}};
                  const root=document.querySelector('#page-direct');
                  return {
                    pagePosition:getComputedStyle(root).position,
                    panelPosition:getComputedStyle(root.querySelector('.route-dialog')).position,
                    panel:box(root.querySelector('.route-dialog')),
                    fields:[...root.querySelectorAll('textarea')].map(box),
                    footer:box(root.querySelector('.route-footer')),
                    overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth
                  };
                }""")
                assert d["pagePosition"]=="static" and d["panelPosition"]=="static", (width,d)
                assert all(abs(v["x"]-d["fields"][0]["x"]) < 1 for v in d["fields"]), (width,d)
                assert all(v["right"] <= d["panel"]["right"] - 10 for v in d["fields"]), (width,d)
                assert d["footer"]["y"] > d["fields"][-1]["bottom"], (width,d)
                assert d["overflow"] <= 1, (width,d)

                page.locator('button[data-page="route"]').click()
                route=page.evaluate("""() => {
                  const cards=[...document.querySelectorAll('#singboxRouteRuleEditor .route-rule')];
                  const get=e=>{const b=e.getBoundingClientRect();return {left:b.left,right:b.right,top:b.top}};
                  return {
                    toggles:cards.map(c=>get(c.querySelector('.route-rule-toggle'))),
                    moves:cards.map(c=>get(c.querySelector('.route-rule-moves'))),
                    checks:cards.map(c=>get(c.querySelector('[data-select-route-rule]'))),
                    editorEnabled:get(document.querySelector('.route-rule-editor-header [data-field="Enabled"]')),
                    headerHeight:document.querySelector('.route-rule-editor-header [data-field="Enabled"]').getBoundingClientRect().height,
                    overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth,
                  };
                }""")
                assert len(route["toggles"])==2, (width,route)
                assert abs(route["toggles"][0]["left"]-route["toggles"][1]["left"])<=1.5, (width,route)
                assert all(move["right"] < toggle["left"] for move,toggle in zip(route["moves"],route["toggles"])), (width,route)
                assert route["headerHeight"] <= 30, (width,route)
                assert route["overflow"] <= 1, (width,route)

                page.locator('button[data-page="singbox"]').click()
                sb=page.evaluate("""() => {
                  const rect=selector=>{const r=document.querySelector(selector).getBoundingClientRect();return {left:r.left,right:r.right,width:r.width}};
                  return {
                    field:rect('#page-singbox .card .field'),
                    save:rect('#singboxSaveEngine'),
                    load:rect('#singboxLoadEngine'),
                    upload:rect('#singboxRuleSetUpload'),
                    refresh:rect('#singboxRuleSetRefresh'),
                    overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth,
                  };
                }""")
                assert abs(sb["field"]["left"]-sb["save"]["left"])<=1.5, (width,sb)
                assert abs(sb["field"]["right"]-sb["load"]["right"])<=1.5, (width,sb)
                assert abs(sb["upload"]["left"]-sb["field"]["left"])<=1.5, (width,sb)
                assert abs(sb["refresh"]["right"]-sb["field"]["right"])<=1.5, (width,sb)
                assert sb["overflow"]<=1, (width,sb)

                page.locator('button[data-page="logs"]').click()
                log=page.evaluate("""() => {
                  const buttons=[...document.querySelectorAll('#page-logs .logs-actions button')];
                  const parent=document.querySelector('#page-logs .logs-actions').getBoundingClientRect();
                  return {visible:buttons.every(b=>b.getBoundingClientRect().width>60),
                    names:buttons.map(b=>b.textContent.trim()),
                    inside:buttons.every(b=>b.getBoundingClientRect().left>=parent.left-1&&b.getBoundingClientRect().right<=parent.right+1),
                    overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth};
                }""")
                assert log["visible"] and log["inside"] and log["overflow"]<=1, (width,log)
                assert log["names"]==["Выделить","Копировать","Очистить"], (width,log)


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

            page.evaluate("""() => {
              document.querySelector('#workers').value = '9';
              document.querySelector('#workers').dispatchEvent(new Event('input',{bubbles:true}));
            }""")
            assert page.locator("#workersValue").text_content() == "9"
            page.locator("#workers").evaluate("""el => {
              el.value = "11";
              el.dispatchEvent(new Event("input", {bubbles:true}));
            }""")

            # Auto API worker slider is an independent 9..27 setting,
            # updates its readout, and preserves the backend's numeric key.
            auto=page.locator("#autoApiWorkers")
            assert auto.get_attribute("type") == "range"
            auto.evaluate("""el => {
              el.value = '21';
              el.dispatchEvent(new Event('input',{bubbles:true}));
            }""")
            assert page.locator("#autoApiWorkersValue").text_content() == "21"
            auto.focus()
            auto.press("ArrowRight")
            assert auto.input_value() == "22"
            assert page.locator("#autoApiWorkersValue").text_content() == "22"
            page.locator("#vkAuthMode").select_option("auto_js")
            page.locator("#turnTransport").select_option("tcp_tls")
            page.locator("#captchaMode").select_option("manual")
            page.locator("#saveSettings").click()
            page.wait_for_function("""() => document.querySelector("#workersValue").textContent === "11" """)
            saved_settings = [json.loads(raw) for method, path, raw in calls
                              if method == "POST" and path == "/settings" and raw]
            assert saved_settings and saved_settings[-1]["autoApiWorkers"] == 22, (
                "Auto API workers slider value did not persist through the API"
            )
            assert saved_settings and saved_settings[-1]["workers"] == 11, (
                "Tunnel settings were not persisted"
            )
            assert saved_settings[-1]["vkAuthMode"] == "auto_js" and saved_settings[-1]["turnTransport"] == "tcp_tls" and saved_settings[-1]["captchaMode"] == "manual", (
                "VK credential mode, transport, or CAPTCHA mode did not persist"
            )

            # No duplicated server editor on the Tunnel tab. Runtime peer
            # belongs to the selected CSQTT profile, not to Deploy SSH.
            assert page.locator("#serverPeer").count() == 0
            assert "выбранного профиля" in page.locator("#profilePeerInfo").inner_text()
            previous_peer=selected["profile"].get("peer")
            page.locator('button[data-page="deploy"]').click()
            assert "для установки по SSH" in page.locator("label[for=deployHost]").inner_text()
            page.locator("#deployHost").fill("198.51.100.88")
            page.locator('button[data-page="settings"]').click()
            page.wait_for_timeout(5500)  # regression for the 5s refresh
            assert selected["profile"].get("peer") == previous_peer, "Deploy SSH host must not rewrite the active client peer"
            page.locator('button[data-page="deploy"]').click()
            assert page.locator("#deployHost").input_value() == "198.51.100.88"
            page.locator('button[data-page="settings"]').click()
            # Logs have clearly labeled Select / Copy / Clear actions.
            page.locator('button[data-page="logs"]').click()
            page.wait_for_function("""() => document.querySelector("#logs").textContent.includes("Записей пока нет")""")
            assert any(method == "GET" and path == "/logs" for method, path, _ in calls), (
                "Opening Logs did not automatically load /logs"
            )
            assert page.locator("#refreshLogs").count() == 0, (
                "Logs must match the original copy/clear-only terminal controls"
            )
            page.locator("#selectLogs").click()
            selection=page.evaluate("() => window.getSelection().toString()")
            assert "Записей пока нет" in selection, selection
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
            # Explanatory text and whitespace must not toggle Deploy switches.
            for vw in (390,1280):
                page.set_viewport_size({"width":vw,"height":850})
                assert not page.locator("#sshKeyToggle").is_checked()
                page.locator(".deploy-ssh-toggle > span").click()
                assert not page.locator("#sshKeyToggle").is_checked()
                page.locator("#sshKeyToggle").click()
                assert page.locator("#sshKeyToggle").is_checked()
                assert page.locator("#deployKeyWrap").is_visible()
                page.locator("#sshKeyToggle").click()
                assert not page.locator("#sshKeyToggle").is_checked()
                assert page.locator("#deployKeyWrap").is_hidden()
                assert not page.locator("#manualPorts").is_checked()
                page.locator("#manualPorts + span").click()
                assert not page.locator("#manualPorts").is_checked()
                page.locator("#manualPorts").click()
                assert page.locator("#manualPorts").is_checked()
                assert page.locator("#portFields").is_visible()
                page.locator("#manualPorts").click()
                assert not page.locator("#manualPorts").is_checked()
                assert page.locator("#portFields").is_hidden()
                assert page.locator("#dockerInstall").is_disabled()
                page.locator("#dockerInstall + span").click()
                assert not page.locator("#dockerInstall").is_checked()
            page.set_viewport_size({"width":1280,"height":850})
            page.locator("#deployHost").fill("192.0.2.50")
            page.locator("#deployPassword").fill("fixture-ssh-password")
            # First install attempt without server credentials must open the
            # relevant authorization dialog BEFORE showing install confirmation.
            initial_posts = len([1 for method, path, _ in calls if method == "POST" and path == "/deploy"])
            page.locator("#startDeploy").click()
            page.locator("#deploySecretsOverlay").wait_for(state="visible")
            assert page.locator("#deployConfirmOverlay").is_hidden(), "confirmation shown before authorization"
            assert "НЕ SSH-пароль" in page.locator("#deployAuthMessage").inner_text()
            assert "192.0.2.50" in page.locator("#deployAuthMessage").inner_text()
            assert page.evaluate("document.activeElement === document.querySelector('#mainPassword')")
            assert page.locator("#deployPassword").input_value() == ""
            assert ssh_password_saved["192.0.2.50"] == "fixture-ssh-password"
            assert len([1 for method, path, _ in calls if method == "POST" and path == "/deploy"]) == initial_posts
            page.locator("#cancelDeployAuth").click()
            assert page.locator("#deploySecretsOverlay").is_hidden()
            assert "Авторизация" in page.locator("#authSummary").inner_text()
            # Existing explicit access to the authorization dialog still works.
            page.locator("#editDeployAuth").click()
            page.locator("#deploySecretsOverlay").wait_for(state="visible")
            assert page.locator("#deployAuthMessage").is_hidden()
            # A single blue focus border belongs to the password wrapper.
            # The nested password input must not draw a second offset ring.
            for theme_name in ("light","dark"):
                choose_theme(page, theme_name)
                for vw in (390, 768, 1280):
                    page.set_viewport_size({"width":vw,"height":850})
                    field=page.locator("#mainPassword")
                    field.focus()
                    visual=page.evaluate("""() => {
                      const el=document.querySelector('#mainPassword');
                      const box=el.closest('.password-inline');
                      const a=getComputedStyle(el),w=getComputedStyle(box);
                      const b=box.getBoundingClientRect();
                      return {inputOutline:a.outlineStyle,inputBoxShadow:a.boxShadow,
                        inputBorder:a.borderLeftWidth,wrapperBorder:w.borderLeftWidth,
                        wrapperColor:w.borderLeftColor,themeBlue:(() => { const probe=document.createElement('span');probe.style.color='var(--blue)';box.appendChild(probe);const color=getComputedStyle(probe).color;probe.remove();return color;})(),
                        wrapperRadius:w.borderTopLeftRadius,withinViewport:b.left>=0&&b.right<=innerWidth};
                    }""")
                    assert visual["inputOutline"]=="none",(theme_name,vw,visual)
                    assert visual["inputBoxShadow"]=="none",(theme_name,vw,visual)
                    assert visual["inputBorder"]=="0px",(theme_name,vw,visual)
                    assert visual["wrapperBorder"]=="1px",(theme_name,vw,visual)
                    assert visual["wrapperColor"]==visual["themeBlue"],(theme_name,vw,visual)
                    assert visual["wrapperRadius"]=="12px" and visual["withinViewport"],(theme_name,vw,visual)
                # Focusing another form field removes wrapper's blue border.
                page.locator("#webLogin").focus()
                after=page.evaluate("""() => {
                  const box=document.querySelector('#mainPassword').closest('.password-inline');
                  return getComputedStyle(box).borderLeftColor;
                }""")
                assert after!=visual["themeBlue"],(theme_name,after)
            choose_theme(page, "light")
            page.set_viewport_size({"width":1280,"height":850})
            page.locator("#mainPassword").fill("fixturetunnelpassword")
            page.locator("#webLogin").fill("fixtureadmin")
            page.locator("#webPassword").fill("fixturewebpassword")
            page.locator("#saveDeployAuth").click()
            page.wait_for_function("""() => document.querySelector("#deploySecretsOverlay").classList.contains("hidden")""")
            assert deploy_auth_saved["192.0.2.50"]["mainPassword"] == "fixturetunnelpassword"
            assert deploy_auth_saved["192.0.2.50"]["webLogin"] == "fixtureadmin"
            assert deploy_auth_saved["192.0.2.50"]["webPassword"] == "fixturewebpassword"
            assert page.locator("#mainPassword").input_value() == ""
            assert page.locator("#webPassword").input_value() == ""
            assert "сохранены" in page.locator("#authSummary").inner_text()
            # SSH password is saved on focus loss, controller-side, and never
            # returned in plaintext. It survives page reload with other credentials.
            page.locator("#deployPassword").fill("fixture-ssh-password")
            page.locator("#deployUser").focus()
            page.wait_for_function("""() => document.querySelector('#deploySSHPasswordStatus').textContent.includes('сохранён для этого сервера')""")
            assert ssh_password_saved["192.0.2.50"]=="fixture-ssh-password"
            assert page.locator("#deployPassword").input_value()==""
            page.reload(wait_until="domcontentloaded")
            page.locator('button[data-page="deploy"]').click()
            page.wait_for_function("""() => document.querySelector('#authSummary').textContent.includes('сохранены')""")
            assert page.locator("#deployHost").input_value()=="192.0.2.50"
            page.wait_for_function("""() => document.querySelector('#deploySSHPasswordStatus').textContent.includes('сохранён для этого сервера')""")
            assert page.locator("#deployPassword").input_value()==""
            assert "Сохранён" in page.locator("#deployPassword").get_attribute("placeholder")
            storage=page.evaluate("JSON.stringify(localStorage)")
            assert "fixturetunnelpassword" not in storage and "fixturewebpassword" not in storage and "fixture-ssh-password" not in storage
            assert "fixturetunnelpassword" not in page.content() and "fixturewebpassword" not in page.content() and "fixture-ssh-password" not in page.content()
            page.locator("#editDeployAuth").click()
            page.locator("#deploySecretsOverlay").wait_for(state="visible")
            assert page.locator("#webLogin").input_value()=="fixtureadmin"
            assert page.locator("#mainPassword").input_value()==""
            assert page.locator("#webPassword").input_value()==""
            assert "сохранены" in page.locator("#deployAuthStoredNote").inner_text()
            page.locator("#saveDeployAuth").click()
            page.wait_for_function("""() => document.querySelector("#deploySecretsOverlay").classList.contains("hidden")""")
            assert deploy_auth_saved["192.0.2.50"]["mainPassword"]=="fixturetunnelpassword"
            # Saved SSH credentials work with an empty UI password field.
            assert page.locator("#deployPassword").input_value()==""
            # Install requires a centered in-app confirmation, not a browser dialog.
            # Cancel, Escape and backdrop must never send the Deploy request.
            initial_posts=len([1 for method,path,_ in calls if method=="POST" and path=="/deploy"])
            page.locator("#startDeploy").click()
            page.locator("#deployConfirmOverlay").wait_for(state="visible")
            assert page.locator("#deployConfirmOverlay").is_visible()
            assert page.locator("#deployConfirmHost").inner_text()=="192.0.2.50"
            assert page.locator("#deployConfirmCorePort").inner_text()=="UDP 47000"
            assert page.locator("#deployConfirmPanelPort").inner_text()=="TCP 47002"
            page.locator("#deployConfirmCancel").click()
            assert page.locator("#deployConfirmOverlay").is_hidden()
            assert len([1 for method,path,_ in calls if method=="POST" and path=="/deploy"])==initial_posts
            page.locator("#startDeploy").click()
            page.locator("#deployConfirmOverlay").wait_for(state="visible")
            page.locator("#deployConfirmCancel").press("Escape")
            assert page.locator("#deployConfirmOverlay").is_hidden()
            assert len([1 for method,path,_ in calls if method=="POST" and path=="/deploy"])==initial_posts
            # Modal stays centered for mobile and desktop and in both visual themes.
            for theme_name in ("light","dark"):
                choose_theme(page, theme_name)
                for viewport_width in (390,768,1280,1920):
                    page.set_viewport_size({"width":viewport_width,"height":850})
                    page.locator("#startDeploy").click()
                    page.locator("#deployConfirmOverlay").wait_for(state="visible")
                    dialog=page.evaluate("""() => {
                        const rect=document.querySelector('#deployConfirmOverlay [role="dialog"]').getBoundingClientRect();
                        return {x:rect.x,right:rect.right,centerX:rect.x+rect.width/2,centerY:rect.y+rect.height/2,
                                width:rect.width,height:rect.height,visible:innerWidth,viewHeight:innerHeight,
                                overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth};
                    }""")
                    assert abs(dialog["centerX"]-dialog["visible"]/2)<2,(theme_name,viewport_width,dialog)
                    assert abs(dialog["centerY"]-dialog["viewHeight"]/2)<2,(theme_name,viewport_width,dialog)
                    assert dialog["x"]>=0 and dialog["right"]<=viewport_width+1 and dialog["overflow"]<=1,(theme_name,viewport_width,dialog)
                    page.locator("#deployConfirmCancel").click()
            page.set_viewport_size({"width":1280,"height":850})
            choose_theme(page, "light")
            page.locator("#startDeploy").click()
            page.locator("#deployConfirmOverlay").wait_for(state="visible")
            page.locator("#deployConfirmAccept").click()
            page.wait_for_function("""() => document.querySelector("#toast").textContent.includes("Деплой запущен")""")
            deploy_requests = [json.loads(raw) for method, path, raw in calls
                               if method == "POST" and path == "/deploy" and raw]
            assert deploy_requests, "Deploy button did not call /deploy"
            assert deploy_requests[-1]["host"] == "192.0.2.50"
            assert deploy_requests[-1]["deviceId"] == "fixture-device"
            assert deploy_requests[-1]["corePort"] == 47000 and deploy_requests[-1]["warpPort"] == 47002
            assert not any(k in deploy_requests[-1] for k in ("mainPassword","webPassword","webUser")), "browser must not resend saved server passwords"
            assert deploy_requests[-1]["password"]=="", "browser must not resend saved SSH password"

            # Remove only saved SSH password and ensure web/tunnel auth survives.
            page.locator("#deleteDeploySSHPassword").click()
            page.wait_for_function("""() => document.querySelector('#deploySSHPasswordStatus').textContent.includes('не сохранён')""")
            assert "192.0.2.50" not in ssh_password_saved
            assert deploy_auth_saved["192.0.2.50"]["mainPassword"]=="fixturetunnelpassword"
            # Host isolation: switching away must never reuse another host's SSH secret.
            page.locator("#deployHost").fill("192.0.2.51")
            page.locator("#deployHost").press("Tab")
            page.wait_for_function("""() => document.querySelector('#deploySSHPasswordStatus').textContent.includes('не сохранён')""")
            assert page.locator("#deployPassword").input_value()==""
            assert page.locator("#deleteDeploySSHPassword").is_hidden()
            page.locator("#deployHost").fill("192.0.2.50")
            page.locator("#deployHost").press("Tab")
            # A failed status endpoint must not be misreported as a real disconnect,
            # and deployment actions must remain blocked when operation state is unknown.
            fail_paths.update({"/vpn/status", "/deploy/status", "/deploy/log"})
            page.reload(wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#apiStatus').textContent.includes('API')")
            assert page.locator("#connState").inner_text() == "Статус недоступен", "VPN API failure was presented as disconnected"
            assert page.locator("#power").is_disabled(), "connect toggle stayed enabled while VPN state was unknown"
            assert "Не удалось получить состояние деплоя" in page.locator("#deployState").inner_text(), "unknown deploy state was presented as a completed/idle operation"
            assert page.locator("#startDeploy").is_disabled(), "deploy remained enabled while deploy state was unknown"
            assert "HTTP 503" in page.locator("#deployState").inner_text()
            assert page.locator("#refreshDeployStatus").is_enabled()
            page.locator('button[data-page="deploy"]').click()
            fail_paths.discard("/deploy/status")
            initial_deploy_posts=len([1 for method,path,_ in calls if method=="POST" and path=="/deploy"])
            page.locator("#refreshDeployStatus").click()
            page.locator("#deployStatusOverlay").wait_for(state="visible")
            page.wait_for_function("() => document.querySelector('#deployStatusPill').textContent.includes('Контроллер отвечает')")
            assert page.locator("#deployStatusApi").inner_text()=="Доступен"
            assert page.locator("#deployStatusOperation").inner_text()=="Нет активной операции"
            assert page.locator("#startDeploy").is_enabled(), "retry did not re-enable deploy after confirmed idle status"
            assert "не подтверждает установку" in page.locator("#deployStatusOverlay").inner_text()
            for theme_name in ("light","dark"):
                choose_theme(page, theme_name)
                for vw in (390,768,1280,1920):
                    page.set_viewport_size({"width":vw,"height":850})
                    rect=page.evaluate("""() => {
                      const r=document.querySelector('#deployStatusOverlay [role="dialog"]').getBoundingClientRect();
                      return {x:r.x,right:r.right,midX:r.left+r.width/2,midY:r.top+r.height/2,w:innerWidth,h:innerHeight,
                        horizontalOverflow:document.documentElement.scrollWidth-document.documentElement.clientWidth};
                    }""")
                    assert abs(rect["midX"]-rect["w"]/2)<2,(theme_name,vw,rect)
                    assert abs(rect["midY"]-rect["h"]/2)<2,(theme_name,vw,rect)
                    assert rect["x"]>=0 and rect["right"]<=rect["w"]+1 and rect["horizontalOverflow"]<=1,(theme_name,vw,rect)
            page.set_viewport_size({"width":1280,"height":850})
            choose_theme(page, "light")
            page.locator("#deployStatusClose").press("Escape")
            assert page.locator("#deployStatusOverlay").is_hidden()
            fail_http_status["/deploy/status"]=403
            page.locator("#refreshDeployStatus").click()
            page.wait_for_function("() => document.querySelector('#deployStatusApi').textContent.includes('HTTP 403')")
            assert page.locator("#deployStatusPill").get_attribute("data-kind")=="error"
            assert "Tailscale" in page.locator("#deployStatusExplanation").inner_text()
            assert page.locator("#startDeploy").is_disabled(), "403 deployment denial must fail closed"
            fail_http_status.pop("/deploy/status")
            page.locator("#deployStatusRetry").click()
            page.wait_for_function("() => document.querySelector('#deployStatusApi').textContent === 'Доступен'")
            assert page.locator("#startDeploy").is_enabled()
            assert len([1 for method,path,_ in calls if method=="POST" and path=="/deploy"])==initial_deploy_posts, "status check triggered Deploy"
            page.locator("#deployStatusOverlay").click(position={"x":3,"y":3})
            assert page.locator("#deployStatusOverlay").is_hidden()
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
    print(f"CSQTT_CHROMIUM_UI_SMOKE_PASS widths={','.join(map(str, WIDTHS))} tabs=7 singbox-config-api=PASS profile-api=PASS connect-disconnect=PASS tunnel-settings=PASS direct-settings-api=PASS deploy-api=PASS socks5-live-status=PASS socks5-address-hidden-when-inactive=PASS logs=PASS captcha-bridge-trusted-origin=PASS captcha-bridge-fail-closed=PASS")


if __name__ == "__main__":
    main()
