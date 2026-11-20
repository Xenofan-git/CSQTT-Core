# CSQTT Core CAPTCHA Bridge

The optional Manifest V3 bridge observes VK Smart CAPTCHA completion on `id.vk.com` / `id.vk.ru` and returns the resulting `success_token` to the waiting CSQTT Core panel.

## Configure the trusted panel first

1. Open the extension's **Details** page.
2. Open **Extension options**.
3. Enter the exact CSQTT Core panel origin, for example `https://nanopi.example.com` or `http://192.168.1.10:47002`.
4. Save the setting, then open the VK CAPTCHA flow from the panel.

Only the explicitly saved origin is used as the return destination. The bridge ignores `lalune_panel` URL fragments and `document.referrer`. If no valid trusted origin is configured, the token is not redirected anywhere.

HTTPS is accepted for public hosts. HTTP is accepted only for localhost, `.localhost`, `.local`, private IPv4 ranges, loopback, private IPv6, and Tailscale's CGNAT IPv4 range. The configured value must be an origin only (scheme, host, optional port), without a path, query, or fragment.

## How the bridge is split

- `captcha-bridge-main.js` runs in the VK page's MAIN world only to observe the page's own fetch/XHR response and publish the token to the extension's isolated content script.
- `captcha-bridge.js` runs in the ISOLATED world, reads the trusted origin from extension storage, validates it, checks the pending session token, and performs the redirect.
- `origin-policy.js` applies the same strict origin validation in the extension options page and the isolated content script.

The page fragment/referrer never selects the destination. The panel still consumes the returned fragment and submits it through its same-origin authenticated `/api/captcha/result` endpoint; the backend accepts only the currently pending matching session.

## Desktop Chrome / Chromium

1. Open Extensions.
2. Enable Developer mode.
3. Choose Load unpacked and select this `captcha-bridge` directory.
4. Open the extension's Details → Extension options and configure the panel origin.
5. Open the CSQTT Core panel and connect normally.

## Android

Standard Chrome for Android does not provide the desktop extension workflow. Firefox for Android supports add-ons; use a browser/add-on combination that supports the extension's storage API and options page.

## Security notes

The bridge does not log or persist CAPTCHA tokens. The trusted panel origin is stored in extension-local settings. If it is absent or invalid, redirect is refused. A hostile VK-page script may still observe the CAPTCHA response in that page's own origin; this bridge's security boundary is that it cannot redirect the result to an origin selected by page-controlled URL data or referrer.
