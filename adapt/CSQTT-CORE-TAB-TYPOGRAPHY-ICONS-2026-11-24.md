# CSQTT Core — slightly larger, lighter seven-tab labels and icons

Date: 2026-11-24. Scope: visual adjustments of the seven-tab navigation bar in the existing Debian/Ubuntu ARM64 web panel; no change to credentials, routing, data, tunnel, API, services or user settings.

## Requested change
- Desktop labels increase 12px -> 13px.
- Phone labels increase 10px -> 11px; under 370px viewport (two-row 4+3 dock) labels increase 11px -> 12px.
- Font weight in all seven tabs decreases 650 -> 500 (lighter).
- Desktop icon font size increases 22px -> 25px; SVG icons 23px -> 26px; line-height 26px -> 29px.
- Phone icon font size increases 20px -> 23px; SVG icons 21px -> 24px; line-height 23px -> 26px.
- No changes to the tab controls' actual functionality, order, color, button size, or roundness. The seven-tab compact-label implementation remains.
- Browser regression now checks exact label font size and 500 weight, icon sizes, unclipped labels and no horizontal scroll at desktop/tablet/phone widths; static contract checks CSS tokens.

## Testing completed
- Real headless Chromium measured widths 320, 360, 390, 430, 768, 1280 and 1920 px. Every viewport passed: exactly seven tabs, no clipped labels, no horizontal overflow, all tab buttons in dock, font weight 500, computed label sizes and computed icon sizes as requested.
- Existing static contract passes (54 buttons, seven tabs, 35 API paths). Browser regression syntax validated.
- Built actual Go Linux ARM64 ELF SHA256 dcae6749bc02433b528a378ec6825d1e12c6c6f672cec3b2f8aeb62ac4ace7b2; embedded HTML SHA256 a5d05fd244bffdaa88af284a96901a4246be72bcee8825e713a36ddaeead43a7.
- Compressed build staged safely without installation on Amsterdam VPS at /srv/projects/CSQTT-Core/staging/csqtt-login-20261123/csqtt-core-nav-typography-arm64.zst, size 10507931 bytes, SHA256 dd6f4fc8b6cb430aea8a873fdc0f039d52b03c6d0a5665eda9d9b3053bd4373e; hash and zstd integrity verified on Amsterdam VPS.

## Deployment blocker
- At the time of this build, NanoPi and Keenetic were unreachable by Tailscale. On Amsterdam VPS, tailscale ping timed out to both 100.106.59.54 and 100.70.73.57, and SSH bridge 127.0.0.1:2229 had no available connection. Direct NanoPi Tailnet SSH unavailable. The authorized Android/Termux Desktop Commander direct LAN SSH to 192.168.4.26:22 also timed out.
- This new binary has NOT been installed on NanoPi. Do not claim production uses it.
- When access returns, check the live old binary SHA, save fresh binary+unit backup with CRC/hash first, stage verified package to NanoPi, then atomically replace ONLY csqtt-core.service with rollback on failure. Verify /login, auth protected / exact embedded HTML SHA, seven tabs/browser after login, one sing-box, unchanged default route, HydraRoute/M-UI/Tailscale services, and journal readback.
- Do not change network, DNS, firewall, Tailscale routing or NanoPi services to work around the access outage. No user credentials or VPN tokens appear in this report.
