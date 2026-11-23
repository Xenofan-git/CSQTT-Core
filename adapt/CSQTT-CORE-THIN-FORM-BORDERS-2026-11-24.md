# CSQTT Core — thin 1px form field borders and lightweight larger tab icons

Date: 2026-11-24. Scope: web panel CSS and regression tests. No changes to Go API, VK TURN, clients, config data, passwords, system services or default routes.

## Latest UI request
After increasing tab label/icon sizes and decreasing tab font weight, user also requested slimmer borders where form field outlines were thick. The panel now enforces border-width:1px!important on text-like input controls, selects, textareas and .password-inline outer frame. Keeps existing 12px field corner radius. Underlying .password-inline input remains borderless to avoid a double frame. Intentionally do not change borders on the 2px switch controls, range sliders, buttons, dock or other UI surfaces. For focused input/textarea/select the formerly heavy ring is now outline:1px and outline-offset:1px with a 1px soft blue shadow; focus remains visible and accessible.

This preserves the earlier committed nav typography change from commit 17c443346e604128fae3eb66a2747958c5f1936a: desktop labels 13px, phone labels 11px (12px <=370px), tab font-weight 500, desktop icon 25px/SVG 26px, phone icon 23px/SVG 24px. Seven tab names/order/handlers have not changed.

## Validation
- Native Chromium on Moscow VPS validated both light/dark at widths 360,390,1280,1920. All form controls remain border-radius 12px, ordinary field outer borders exactly 1px (except preexisting intentionally borderless #panelLink), nested password inner border 0px/outer frame 1px. Focused #vkHashes outline exactly 1px, focus blue shadow exactly 1px. Toggle border retained at 2px. No horizontal overflow; seven tab labels weight 500.
- Updated static CSQTT panel contract and Playwright browser acceptance to check field border widths, password wrapper exception, keyboard focus outline, while continuing tab font/icon checks. Static contract (54 buttons, seven tabs, 35 API paths) passed; local full Chromium visual acceptance passed.
- Cross-compiled real Go Linux ARM64 ELF SHA256 fd09a6eadef5172ad528e829621a91727b49a30d2fe900e869d1c9d5c2c5e20a, embedded frontend HTML SHA256 a6a3b07b2498230efdf35a979280fb56b9b9cd165435e137393078d55ea5d653. Compressed 10MB package SHA256 23534815a264147fb45f15f8442cfb219cc0d03b2b04b35d498c17f53b52437e. Verified and staged to Amsterdam VPS at /srv/projects/CSQTT-Core/staging/csqtt-login-20261123/csqtt-core-nav-thin-fields-arm64.zst.

## Browser CI refinement
The initial browser acceptance detected that the nested password input still inherited the legacy 32px radius after its border was exempted. A follow-up fix explicitly retains radius12px on the borderless inner password input while keeping its wrapper frame at 1px. Native Chromium revalidated all field radii (including password) and 1px borders in both themes at 390 and 1280px. This fix is included in the next staged ARM64 package and must be present before any eventual NanoPi deployment.

## Deployment blocker and safe continuation
- NanoPi R3S LTS and Keenetic were offline/unreachable through both direct Tailscale and existing Keenetic SSH bridge (Amsterdam service had no loopback port 2229 listener); Android/Termux Desktop Commander direct LAN 192.168.4.26 also timed out. Thus this updated binary is NOT YET DEPLOYED to NanoPi. Do not report installed, restarted, successful live login or updated NanoPi journal.
- When SSH returns, verify current NanoPi binary/version, existing Sing-box process count and old routes first; make a new Amsterdam zstd backup of the then-live binary+unit; install only after verifying stage and new SHA; restart only csqtt-core.service with rollback on failed checks. Read back authenticated frontend SHA, test login/logout, ensure exactly one existing app-managed Sing-box and all other services/routes unchanged. Then append NanoPi project journals. No need to alter Tailscale keys/ACLs, firewall, routing or other services.
