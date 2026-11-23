# CSQTT Core — desktop and mobile main panel visual restoration

Date: 2026-11-23. Target: Linux ARM64 Go headless web panel on NanoPi R3S LTS, without changing routes, service settings, passwords, profiles, or Xray/Mihomo processes.

## Defect and fix

A prior source block forced HTML min-width 390px, BODY width 390px, and floating seven-tab navigation width 370px regardless of viewport. On a laptop or desktop the app appeared as a tiny phone preview centered in a huge white area, cramped toolbar icons and clipped bottom labels; the main connect card used excessive top margin and blank space. Header also displayed obsolete upstream v2.1.9 while current project is based on Rust 2.5.0.

The UI source adapt/csqtt-panel-preview.html now uses a responsive 1080px-max centered desktop shell, 850px-max seven-tab dock, 480px-max proportional Connect card, accessible header icons and a readable API badge. The original seven-tab order and all element IDs, handlers, sing-box, VPN/route functionality are unchanged. Brand subtitle is Core · Linux ARM64, not outdated version or NanoPi-specific product name.

At widths <=700px, app uses actual viewport width, 100%-width cards, compressed spacing, and a short accessible visual label for first tab (Связь with full aria-label=Подключение). At <=370px dock uses 4+3 two-row grid to show every tab legibly. Settings, Route modal, Sing-box, Deploy, Logs and other cards use responsive max-widths; theme and reduced motion remain supported.

## Verification

- Native Chromium headless on Moscow VPS measured widths 320, 360, 390, 430, 768, 1280, 1920 with no horizontal document overflow, header icons not overlapping brand, centered connection card, all seven tabs inside dock and unclipped. 1920px: body 1080px, dock 850px, connection card 480px. 390px: body 390px and dock 374px; 360px: 4+3 rows.
- Native Chromium clicked all seven tabs at widths 360, 390, 768, 1280 and checked selected page/no overflow.
- Static contract adapt/test-csqtt-panel-contract.py PASS (54 fixed UI buttons, seven tabs, 35 API paths). Updated adapt/test-csqtt-panel-browser.py regression from fixed 390px to adaptive widths. Full Playwright test not runnable locally because Python playwright package absent; CI browser runner is separate.
- Real Linux ARM64 Go headless binary SHA256 88ee3ac5d8f3d7ca85384f394b269beed27da63d74d980f38576944ea9b986e9. Embedded frontend/index.html SHA256 82a5bc471c5be082e1b5de4029d7e4b524971af6505c033f1df8c82a48c55acf.
- Amsterdam VPS pre-deploy binary/unit backup at /srv/projects/CSQTT-Core/backups/nanopi-csqtt-responsive-ui-20261123/csqtt-core-before-responsive-web-ui-20261123.tar.zst, SHA256 e9306a5dccb35513e064575d715af269fdb9645450c6d1048706369f247ad879. NanoPi local rollback backup at /var/lib/csqtt-core/upgrade-before-responsive-ui-20261123. Transfer Moscow -> Amsterdam -> Keenetic SSH bridge -> NanoPi verified compressed hash, atomic replacement protected by rollback.
- Actual NanoPi: csqtt-core.service active MainPID 487019, NRestarts 0, exactly one app-owned sing-box PID 487043. Live login, API cookie auth/logout passed on 1061 and 18788; authenticated response / exact embedded HTML SHA. Actual SOCKS5 CONNECT to Go API port 1062 PASS. SQLite retained zero new profiles. HydraRoute Core, M-UI and tailscale-xray-proxy remained active; IPv4 default route via 192.168.4.1 dev eth0 unchanged.
- No external VK TURN verification was attempted during visual-only updates.
