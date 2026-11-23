# CSQTT Core — branded login on NanoPi ARM64, final focus/brand refinement

Date: 2026-11-23. Scope: CSQTT Core only; no HydraRoute/M-UI/Tailscale or global routing modification.

## User-requested visual changes

- Browser-native HTTP Basic popup replaced with a dedicated, centered responsive login form with username, password, reveal button and localized error message.
- In light theme CSQTT is blue (#2475e6), Core near-black (#111215). In dark theme Core uses contrasting light text.
- Remove the outer blue focus outline and glowing box-shadow on username/password inputs, while retaining the single blue input border and non-input keyboard focus feedback.

## Code and test

- Login is fully embedded with no CDN, extra font or network dependencies. Same Go controller keeps API Basic compatibility without WWW-Authenticate challenge to browsers. Cookie session is random, HttpOnly, SameSite Strict, held only in memory, 8-hour lifetime. Cross-Origin POST rejected; repeated wrong passwords limited to 8 per 5min; logout revokes the session.
- Added custom login, logout and rate-limiting Go tests, and included login.html in four ARM64 GitHub build workflows. Existing CSQTT web panel includes a visible logout icon.
- Browser Chromium responsive CSS computed checks on 1440px desktop and 390px phone for light/dark modes: CSQTT rgb(36,117,230), Core rgb(17,18,21) in light mode, input outline none, boxShadow none, border blue. Additional 360px/1080px dimensions and password reveal have been tested.
- ARM64 binary SHA256: 5e64dd280af5273383888e2f7a5070c73333b13f677ba534a12591ad87036262.
- Embedded login HTML SHA256: 0c1aa6d2be2a89bf89e383d39434ee48c10c8d8327e3f6ca8f4e6eed1b5773d4.

## Actual NanoPi deploy and checks

- Backed up previous live Go binary and unit to Amsterdam VPS private path /srv/projects/CSQTT-Core/backups/nanopi-csqtt-login-ui-20261123/csqtt-core-before-login-colors-20261123.tar.zst, SHA256 56ec41afac7bc7ab25d0cfab894d32ed5c83c3735260f1804b5dca797c9a17ff.
- Transferred built binary Moscow -> Amsterdam -> existing NanoPi SSH bridge through Keenetic; verified compressed package SHA before use. Left a second local backup /var/lib/csqtt-core/upgrade-before-focus-brand-20261123 and scripted rollback.
- Only csqtt-core.service restarted. Final real unit MainPID 474524, NRestarts=0, exactly one app-owned Sing-box process PID 474545. Native root GET HTTP 303 -> /login HTTP 200; HTML SHA matched source. Both Tailscale URLs 100.70.73.57:1061/login and 100.70.73.57:18788/login replied HTTP 200 from Amsterdam VPS.
- Before the last CSS-only change, real NanoPi cookie login, wrong password, logout, API access and Origin checks passed for both ports. After CSS-only binary replacement, verified exact HTML and process/service state; no user-facing screenshot after final switch acquired.
- Default IPv4 route unchanged. HydraRoute Core, M-UI and Tailscale-related service remained active. Did not change CSQTT profiles or attempt live VK TURN.
