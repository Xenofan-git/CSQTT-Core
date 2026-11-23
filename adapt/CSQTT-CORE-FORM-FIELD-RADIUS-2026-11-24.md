# CSQTT Core — subtly rounded input and select fields

Date: 2026-11-24. Scope: visual styling only in Linux ARM64 CSQTT Core Go web panel, no service logic/config changes.

User request: all form fields throughout the panel were excessively round/pill-shaped; make their corners slightly rounded.

## Exact change
- New CSS variable --csqtt-field-radius:12px in adapt/csqtt-panel-preview.html and a final CSS rule applying 12px to text-like input, all select, all textarea, and password-inline wrapper, overriding scattered 26px/32px field radii in the seven-tab panel.
- Checkbox/radio/range/hidden, button/submit/reset intentionally excluded. Toggle switches, range sliders, icons and all button corners unchanged.
- The login page already has subtly rounded 13px fields, and remains unchanged to preserve approved styling.
- New radius regression checks added to adapt/test-csqtt-panel-contract.py and real-browser computed-style tests in adapt/test-csqtt-panel-browser.py.

## Verification
- Chromium on Moscow VPS at 390x844 and 1280x900, both light and dark theme: all 47 form controls computed exactly 12px; no horizontal overflow; checkbox radii 999px, slider 28px, nav buttons 16/20px remained unchanged.
- Static panel contract PASS (54 buttons, 7 tabs, 35 API paths); browser test Python syntax PASS.
- Built Go Linux ARM64 ELF SHA256 a1608feeffb9bf0a702b9a25a92646811a6495e79cf45e84cfe3a3507d5f0006, embedded frontend/index.html SHA256 d519f19316b31453f5e22ce486d867a3878695f8a080f72fb14956e0030a8805.
- Previous NanoPi binary/systemd unit backed up on Amsterdam VPS: /srv/projects/CSQTT-Core/backups/nanopi-csqtt-form-radius12-20261124/csqtt-core-before-form-radius12-20261124.tar.zst, zstd verified, SHA256 20cc866c3063e8e0f1de9327ad94a8742205b3ccb2f44569ef4b62f227ed4e68. NanoPi local rollback binary /var/lib/csqtt-core/upgrade-before-fields-radius12-20261124.
- Transfer Moscow -> Amsterdam -> Keenetic SSH bridge -> NanoPi verified zstd SHA at each hop, ELF hash before atomic swap, auto-rollback if readiness/route checks failed. Restarted only csqtt-core.service.
- Actual NanoPi post-deploy: csqtt-core.service active MainPID 516052, NRestarts 0; single Sing-box process PID 516078; default IPv4 route via 192.168.4.1 dev eth0 unchanged. Login HTTP200, unauth root HTTP303; authenticated HTML SHA matched exact source before an extra GET /settings response JSON parsing check stopped early; no settings written. No VK TURN test.

Changes are only visual; no claim about external tunnel connectivity.
