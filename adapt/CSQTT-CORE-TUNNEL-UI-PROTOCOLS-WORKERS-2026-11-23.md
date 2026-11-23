# CSQTT Core — profile and tunnel UI, Go worker floor, and source audit

Date: 2026-11-23. Scope: CSQTT Core Debian/Ubuntu Linux ARM64 only, NanoPi deployment; no VPN or system routes changed.

## User request and changes
1. Native select arrows on Profile Manager and Tunnel looked like odd thin scroll handles. Applied consistent single-chevron SVG CSS icon via native select appearance:none and contrast-aware arrow for light and dark.
2. All Tunnel select fields retain actual drop-down semantics.
3. Worker floor raised from 1 to 4 in HTML range, JS settings serialization, and patched pinned upstream Go Libs/common.go MinWorkers. The existing default 9 and max 127 remain unchanged. Added guard patch and native Go regression to all four Go ARM64 CI build workflows.
4. Manual hashes are normalized by original Bridge.buildCommand: comma, space, tab, newline accepted; empties discarded and only first 6 hashes used. A selected profile gets hash values from its link and renders that textarea read-only. UI helper now documents entry format. Do not claim unselected manual textarea connects without a saved profile.
5. Default VK client IDs 8202606,6287487 are hardcoded by original pinned Libs/common.go initial settings and were repeated as a web UI fallback; not derived from the signed-in VK user.
6. allowHashRedistribution and validateVkHashes toggles are left-aligned with their labels, preserving state and behavior.
7. Both saveVkSettings and saveSettings perform the same POST /settings through saveSettings(), with only distinct notifications. No functional distinction; buttons retained because user asked for explanation, not deletion.
8. Reserve browser authentication opens VK OAuth URL in the user's browser; user may paste final redirect/token into /vk/import over a private authenticated panel. Never publish URLs bearing tokens.
9. Purple dark theme replaced with blue/navy theme: new root data-theme=dark overrides for --bg, --surface, --surface2, --line, --blue, --csqtt-accent and related colors, including Sing-box. Light mode remains blue.
10. Check core triggers read-only GET /updates/core/check calling original AppCore.CheckUpdateSync() which compares remote LATEST tag with local marker.
11. Update core triggers POST /updates/core/wait after confirmation. Original Go app checks and downloads Endlad2/csqtt-core LATEST from raw.githubusercontent.com and binary from github.com/Endlad2/csqtt-core/releases/download, fallback via an external HTTP proxy 31.77.148.203:8855. It DOES NOT update this CSQTT-Core repository or verify the private fork's pinned Rust client. It is unsafe for current adapted production until a release-asset trust/pinning/rollback process is implemented. Added explicit caution in UI and more informative confirmation, without invoking update.

## Protocol support reality
The Go source registry lists CSQTT, FreeTurn, OlcRTC, OpenFlux and ToTS and has best-effort downloader/runner support. But on live NanoPi the additional four protocol binaries are absent from the app library directories, and their different command-line contracts and actual TCP/VK traffic have not been end-to-end validated. They are therefore marked experimental in the profile chooser. Do not claim they are working or auto-installed. CSQTT itself remains deployed with one Go controller and one Sing-box process.

## Validation and deployment
- Native Chromium headless at 360, 390, 1280px in light and dark: actual select appearance:none and SVG background arrow on profile+Tunnel, toggle switch left of text, no overflow, workers input min4 max127, dark background #101720 and blue accent #4195ff.
- Local tests: Go Libs test with patched MinWorkers=4, source guard test adapt/test-csqtt-min-workers-four.py, web static contract 54 buttons/7 tabs/35 API paths.
- Real ARM64 Go binary SHA256 f0469d478c588bd16a8688f84046ccb6a7b0195e1fccbe409cc12d6d08236721, embedded index HTML SHA256 1374d194b7129377d0f9a3aa826007b60ddf98df5cd7bc3aa8817ea5ad9acae3.
- Before deployment, backed up original NanoPi binary+unit on Amsterdam VPS: /srv/projects/CSQTT-Core/backups/nanopi-csqtt-controls-workers4-20261123/csqtt-core-before-ui-controls-workers4-20261123.tar.zst SHA256 ccbc7c509cc7aafd8336791272fd78b1850272c85736f5b5b9d9498d85d95f26. Local NanoPi rollback copy /var/lib/csqtt-core/upgrade-before-ui-controls-workers4-20261123.
- Binary transferred Moscow -> Amsterdam -> existing Keenetic SSH localhost bridge 127.0.0.1:2229 -> NanoPi. Checked SHA of compressed package at each stop, decompressed ELF hash before swap, restarted only csqtt-core.service and auto-rolled back if acceptance failed. Deployed service active MainPID 501943, NRestarts=0, exactly one owned Sing-box PID 501967.
- Real authenticated GET / on both local ports 1061 and 18788 matched embedded SHA; cookie login/logout and GET /settings returned workers9, autoApiWorkers9, authMode manual (unchanged); no production settings modified. HydraRoute Core, M-UI, tailscale-xray-proxy active, same default IPv4 route via 192.168.4.1 dev eth0.
- No real VK TURN session or external core update attempted in this UI stage.
