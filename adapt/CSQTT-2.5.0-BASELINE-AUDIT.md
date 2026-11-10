# CSQTT 2.5.0 as the authoritative project base — source audit

**Audit date:** 2026-11-10  
**Audit branch:** `refactor/csqtt-2.5.0-core`  
**Audited integration branch:** `fix/explicit-singbox-default-outbound`  
**Audited integration HEAD:** `c775f56d90db1d7ec2c60d54aafff31a0dea5e7a`  
**Last code-tested commit:** `c086798e052b33a0367e225f79a7fb7ec602a7a6`  
**Authoritative CSQTT source:** supplied `csqtt-2.5.0.tar.gz`; package manifests identify app/client/server as 2.5.0.  
**LaLune VK helper source:** `Endlad2/LaLune@e4a6d08b63aef6025bf8ad8f77da660ea552e04b`, specifically `Core/LaLuneTokenFetcher`.

## 1. Correct product boundary

The project is a Linux/ARM64 adaptation of **CSQTT 2.5.0**. CSQTT is the source of truth for the client, server, protocol, application behavior, VK modes, Deploy behavior, settings, recovery and product UI semantics.

The only component intentionally borrowed from LaLune is its VK access-token acquisition helper:
- `Core/LaLuneTokenFetcher/Playwright`;
- `Core/LaLuneTokenFetcher/Libs/LaLuneTokenFetcher.Core`;
- the minimum runtime/launcher integration required to invoke it and safely pass its resulting token to CSQTT.

Do not use LaLune's Flutter application, Wails/Go desktop backend, Linux TUN runner, SOCKS5 implementation, DeployManager, deployment flow, settings model, routing implementation or UI as the product foundation.

**sing-box is our independent routing engine.** It owns the local SOCKS5 entrypoint, ordered routing rules, DNS policy, sniffing/domain matching and selection among DIRECT, CSQTT and user-configured SOCKS5 outbounds. CSQTT remains an independent tunnel core and must expose a narrowly scoped local SOCKS5 service to sing-box. No global route, TUN auto-route, auto_redirect or system-wide traffic interception is to be introduced by sing-box.

## 2. Evidence from the current repository and CI

The active branch is not yet based on CSQTT 2.5.0. The current workflow `.github/workflows/build-original-linux-arm64.yml` does all of the following:

1. Clones the entire `Endlad2/LaLune` repository at `e4a6d08b63aef6025bf8ad8f77da660ea552e04b` into `upstream-lalune`.
2. Runs `adapt/apply-pinned-original-linux-arm64-policy.py` and `adapt/apply-lalune-socks5.py` against LaLune source.
3. Builds Flutter Web from `upstream-lalune/Frontend/Core`, then replaces the generated `index.html` with `adapt/csqtt-panel-preview.html`.
4. Builds the Go Linux application from `upstream-lalune/Desktop/Linux`, importing `upstream-lalune/Desktop/Linux/Libs`.
5. Builds LaLune's Rust `Core/DeployManager`, then patches it to invoke a CSQTT-derived deployment script.
6. Separately downloads the official CSQTT **2.1.9** APK/server assets and extracts the Linux server binaries and provenance.
7. Separately clones `Endlad2/csqtt-core` at `3eafb7ee868f5f2690edf3b0326153813f88eb5f` for the Linux client build.
8. Builds the VK helper from LaLune's `Core/LaLuneTokenFetcher/Playwright/LaLuneTokenFetcher.Playwright.csproj`.

The CI itself explicitly checks `"core": "csqtt-server/2.1.9"` and a deploy script with `SCRIPT_VERSION="2.1.9"`. The workflow therefore does **not** build the supplied CSQTT 2.5.0 source as its product core.

The Go backend in `adapt/headless_main.go` imports `lalune-desktop/Libs`, reports `backend: original-go` and `core: csqtt-server/2.1.9`, and uses the LaLune API/application model. The actual panel HTML is CSQTT-styled, but its runtime API/backend is still hosted by this LaLune-derived application. This is a mixed implementation, not a clean CSQTT 2.5.0 port.

The current SOCKS5 implementation is `adapt/lalune_socks5_linux.go`, injected into `upstream-lalune/Desktop/Linux`. It is a LaLune Linux dataplane adaptation and must not be treated as an allowed LaLune dependency under the corrected product boundary.

The current Deploy path is also hybrid: `adapt/patch-csqtt-deploy-manager.py` patches LaLune's Rust DeployManager to call an adapted CSQTT 2.1.9 `deploy.sh`. It is not the original CSQTT 2.5.0 Deploy implementation.

## 3. Source provenance clarification for VK token acquisition

The user-supplied LaLune archive `LaLune-26.09.12.20.58.tar(1).gz` contains 100 files and does not include `Core/LaLuneTokenFetcher`. Its VK references are primarily manual `vkHashes` configuration and propagation.

The repository's pinned LaLune commit `e4a6d08b63aef6025bf8ad8f77da660ea552e04b` does contain the standalone helper:
- `Core/LaLuneTokenFetcher/Playwright/Program.cs`;
- `Core/LaLuneTokenFetcher/Playwright/PlaywrightTokenFetcher.cs`;
- `Core/LaLuneTokenFetcher/Libs/LaLuneTokenFetcher.Core/VkTokenParser.cs`;
- `Core/LaLuneTokenFetcher/Libs/LaLuneTokenFetcher.Core/TokenStorage.cs`;
- `Desktop/Libs/vktoken.go`, `vkfetcher_launch.go`, `vkplaywright.go`, `tokenfile.go` are LaLune's broader application integration, not all of which should be carried forward.

The helper uses Playwright/Chromium, a persistent browser profile, VK OAuth redirect parsing, and a bounded retry for VK's `silent_token` response before accepting a real `access_token`. It stores the token in a JSON file. Keep the standalone fetcher logic and only the minimum launcher/storage bridge needed by our CSQTT backend. Do not bring over LaLune's Go application, settings, UI or broader VK API client just because those files currently wrap the fetcher.

The CSQTT 2.5.0 source already has its own VK functionality:
- `VkApi.kt`, `VkAuthWebViewManager.kt`, `VkAutoCallsManager.kt`, `VkCallPolicies.kt`, `VkHashValidator.kt`, `VkModePolicy.kt`, `VkTokenScraper.kt`;
- Rust `vk_js_calls.rs`;
- Rust client stdin handling for `VK_JS_BOOTSTRAP:` in `rust-client/main.rs`;
- Kotlin `TunnelManager.kt` writes the bootstrap to the client stdin.

Do not replace CSQTT's VK calls, modes, token validation, hash behavior or bootstrap protocol with LaLune's implementation. Integrate the helper as a token-provider only, then hand the token into the existing CSQTT 2.5.0 contract. Tokens must not be placed in process argv or ordinary logs.

## 4. Authoritative CSQTT 2.5.0 inventory

The supplied archive contains 281 files. Confirmed version markers:
- Android `app/build.gradle.kts`: `versionName = "2.5.0"`;
- `rust-client/Cargo.toml`: package `csqtt-client`, version `2.5.0`, edition 2024, Rust 1.97.1;
- `rust-server/Cargo.toml`: package `csqtt`, version `2.5.0`, edition 2024, Rust 1.97.1;
- original Deploy asset: `app/src/main/assets/deploy.sh`, 112,754 bytes in the supplied archive.

Important source groups:
- Rust client: `rust-client/main.rs`, `session.rs`, `dispatcher.rs`, `worker.rs`, `turn*.rs`, `dns.rs`, `tun.rs`, `vk_js_calls.rs`, `captcha*.rs`, `auth.rs`, `repair.rs`, `profiles.rs`, `stats.rs`, `protocol.rs`.
- Rust server: `rust-server/main.rs`, `dataplane.rs`, `tokio_io.rs`, `net_setup.rs`, `tun_device.rs`, `tproxy.rs`, `proxy_route.rs`, `web_panel.rs`, `protocol.rs`, `model.rs`.
- Android application/control plane: `MainActivity.kt`, `TunnelManager.kt`, `TunnelService.kt`, `SettingsStore.kt`, `DeployManager.kt`, `DeployTab.kt`, `DeployOperations.kt`, recovery policies, security stores, VK flows and CAPTCHA flows.

The Rust client source has Unix support and a `--tun-uds` option; the server has Linux-specific dependencies and native TUN handling. The client embeds `../domains.txt`, while the canonical file in the source tree is `app/src/main/assets/domains.txt`; the Linux build workflow must stage that exact asset at the expected path without editing upstream Rust code. The first baseline CI attempt exposed this packaging omission; the workflow now stages and hashes the canonical file before tests. This is a concrete basis for an ARM64 Linux port, but a successful Android build or an existing forked binary is not proof that the exact supplied 2.5.0 source builds and passes runtime acceptance on Debian ARM64. Build and test the exact pinned source.

## 5. Critical routing boundary and SOCKS5 gap

The CSQTT 2.5.0 server contains its own optional local SOCKS5-to-TProxy routing subsystem (`proxy_route.rs`, `tproxy.rs`, web-panel controls). This is a server-side feature that manipulates Linux TPROXY/networking and is **not** our sing-box routing engine. It must remain disabled/not integrated into the client routing path unless a separately justified server-side use is approved.

The CSQTT 2.5.0 Rust client has `--tun-uds` and stdin VK bootstrap support, but the inspected client source does not provide a ready-made local SOCKS5 inbound server for sing-box. The current project obtains that listener from the LaLune Go SOCKS5/TUN bridge. That is a prohibited dependency under the corrected scope.

A dedicated CSQTT-to-sing-box adapter is therefore a required design/build task:
- expose only a loopback SOCKS5 endpoint for sing-box to use as the CSQTT outbound;
- support TCP and UDP ASSOCIATE, and explicitly test the semantics and lifetime of UDP associations;
- bridge traffic into the CSQTT 2.5.0 client dataplane without LaLune's Go runner/SOCKS implementation;
- bind any LAN-facing listener only when explicitly required and authenticated; default local CSQTT endpoint should be loopback-only;
- do not create global routes, policy rules, DNS resolver changes or system capture as a side effect;
- keep CSQTT process, configuration, logs, status and lifecycle independent from sing-box.

Implementation options (not yet selected): a small dedicated adapter around a TUN file descriptor supplied through the existing UDS handoff, or a purpose-built SOCKS5-to-CSQTT packet bridge. Evaluate both against the 2.5.0 client API and prove TCP/UDP E2E before committing to one. Do not copy `lalune_socks5_linux.go` or the LaLune Linux TUN runner.

## 6. Current integration-to-target gap matrix

| Current repository component | Current provenance/behavior | Required target |
|---|---|---|
| `.github/workflows/build-original-linux-arm64.yml` | Clones and builds full LaLune; fetches CSQTT 2.1.9 and a separate csqtt-core fork | Build exact CSQTT 2.5.0 client/server sources; fetch/build only the isolated LaLune token helper |
| `upstream-lalune/Desktop/Linux` | LaLune Go/Wails Linux application adapted as backend | Remove as product runtime; implement a CSQTT-native Linux control/backend |
| `upstream-lalune/Frontend/Core` | LaLune Flutter Web build used as staging, then index replaced | Remove Flutter/LaLune UI build dependency; package the CSQTT-native panel directly |
| `adapt/csqtt-panel-preview.html` | CSQTT-inspired panel and routing UI | Keep as a starting point; audit every tab/API contract against CSQTT 2.5.0 |
| `adapt/headless_main.go` | LaLune Go API/app model; imports `lalune-desktop/Libs`; reports 2.1.9 | Replace with a CSQTT-specific API and process supervisor |
| `adapt/lalune_socks5_linux.go` | LaLune-derived SOCKS5/TUN bridge | Replace with independent adapter for CSQTT 2.5.0 |
| `Core/DeployManager` via LaLune | LaLune Rust DeployManager patched for CSQTT 2.1.9 | Port required behavior from CSQTT 2.5.0 DeployManager and use CSQTT 2.5.0 deploy.sh |
| `server-assets/deploy.sh` | Pinned/derived CSQTT 2.1.9 script | Use supplied CSQTT 2.5.0 deploy.sh, then reapply namespacing/security changes with regression tests |
| `Endlad2/csqtt-core@3eafb7e...` client | Forked Linux client source with local WIRE-4 patches | Compare against and move to exact CSQTT 2.5.0 `rust-client`; reimplement required recovery changes on that base |
| LaLune `LaLuneTokenFetcher` | Separate Playwright/Chromium token acquisition | Keep only this helper and minimal isolated bridge |
| `adapt/singbox_config_linux.go`, `singbox_manager_linux.go` | Independent sing-box config/process manager | Preserve and continue DNS/sniffing/outbound E2E; remove accidental LaLune path naming/ownership coupling |

## 7. sing-box work already present and to be preserved

At the audited code HEAD:
- independent external sing-box process manager with start/stop/status/logging;
- local SOCKS inbound validation (loopback or specific private address; no wildcard/public bind);
- DIRECT and CSQTT outbounds plus authenticated external SOCKS5 outbounds;
- ordered first-match route rules with domains, suffixes, keywords, regexes, IP/CIDR, advanced fields and Rule Sets;
- explicit `route.final`, so CSQTT is not an implicit fallback;
- immutable DIRECT card and movable/toggleable CSQTT card;
- persisted disabled-card/order state in a checksum-bound sidecar;
- Rule Set JSON/SRS validation, atomic activation, safe-path handling and reference-aware deletion.

The last confirmed CI run is 42595496997 (both jobs succeeded) on code commit `c086798e052b33a0367e225f79a7fb7ec602a7a6`. This validates the scenarios actually run, including ARM64 build, Debian 13 ARM64 VM, SOCKS5 TCP/UDP SO_MARK, Direct IPv4/IPv6, Deploy lifecycle/API and WIRE-4 recovery. It does not validate the current architecture against CSQTT 2.5.0, hardware NanoPi operation, or the outstanding DNS/sniffing/domain-matching matrix.

Keep sing-box as a separate module. Do not discard its tested routing work during the CSQTT core migration.

## 8. Ordered migration plan

### P0 — Establish the exact CSQTT 2.5.0 source build
1. Pin the supplied source archive's provenance/hash and upstream tag/commit.
2. Build the exact `rust-client` and `rust-server` for Linux ARM64 in CI, initially without production installation.
3. Run original source tests and inspect all platform-specific compile gates.
4. Compare wire protocol, client/server configuration and binary behavior with the current Endlad2 fork; record every patch needed and port only compatible patches.
5. Remove v2.1.9 release assets from the production artifact path only after the 2.5.0 replacements pass.

### P1 — Replace the LaLune runtime with a CSQTT-native Linux runtime
1. Define a Linux control-plane API around CSQTT 2.5.0 functions and the existing CSQTT panel contract.
2. Implement explicit CSQTT process start/stop/status/logs, safe process ownership and recovery.
3. Build the independent CSQTT SOCKS5 adapter for sing-box; TCP and UDP must be E2E-tested.
4. Keep the LaLune token helper isolated behind a small provider interface; securely persist/transfer token and feed CSQTT's existing `VK_JS_BOOTSTRAP` stdin protocol.
5. Remove the LaLune Flutter, Go/Wails, LinuxRunner, SOCKS5 bridge and DeployManager from the product build.

### P2 — Port CSQTT 2.5.0 product behavior
1. Port the 2.5.0 DeployManager behavior and use its matching `deploy.sh`.
2. Implement secret-path protection only as a complete server/router/installer/health-check feature; do not ship UI-only protection.
3. Port recovery/diagnostics semantics into Linux process lifecycle, not Android VPN-service code.
4. Preserve VK Auto API/Auto JS, hash validation, CAPTCHA bridge, profile/settings semantics and secure secret handling.
5. Reconcile the CSQTT 2.5.0 panel semantics with the Linux panel; do not silently substitute LaLune UI/API behavior.

### P3 — Finish sing-box and integration
1. Add DNS server configuration and explicit DNS routing; verify no DNS leaks or loops.
2. Test sniffing/domain matching for TLS SNI, HTTP Host, QUIC, ECH and IP-only cases.
3. E2E test external SOCKS5 authentication, UDP ASSOCIATE, route priority, failover/restore, process restarts and persisted config.
4. Prove CSQTT can be stopped/failed while sing-box remains usable via DIRECT/other SOCKS5; prove sing-box can be stopped while CSQTT remains independently manageable.
5. Ensure no global routes, TUN auto-route, auto_redirect or resolver mutation is introduced by sing-box.

### P4 — Acceptance and documentation
1. Full Go/Rust tests, race tests where applicable, panel contract/Chromium tests, ARM64 build and Debian 13 ARM64 VM.
2. Inspect packaged artifact provenance and ensure no LaLune product runtime is bundled.
3. Hardware NanoPi acceptance only after a separate explicit approval; do not alter Keenetic Direct, HydraRoute Neo, NanoPi 3x-ui/Xray or production Tailscale configuration.
4. Update README, NanoPi journal and ChatGPT Library only after implementation and readback/hash verification.

## 9. Non-goals and safety constraints

- No subscriptions or subscription updater in this migration.
- No global system routing or transparent capture by sing-box.
- No change to Keenetic Direct rules, HydraRoute Neo, QuatroVPN keys, 3x-ui/Xray or Tailscale access.
- No production NanoPi install during source/VM migration.
- Do not weaken tests to obtain green CI.
- Preserve CSQTT license notices and the supplied PolyForm Noncommercial 1.0.0 terms. The audited integration branch had no root LICENSE file; the exact upstream CSQTT LICENSE text and required notices have now been restored on this refactor branch (commit `44c963bdf24750916757d4a5f925797f5f61f5ed`). Retain them in derived distributions. This audit is not legal advice.

## 10. Audit conclusion

The current implementation has a substantial and useful sing-box module and meaningful ARM64 VM coverage, but its application/runtime foundation is still LaLune-derived and its packaged CSQTT client/server/deploy assets are 2.1.9/fork-based. That conflicts with the clarified project requirement.

The correct next engineering step is not another sing-box UI feature. It is to establish a reproducible build of the exact CSQTT 2.5.0 client/server source, then replace the LaLune runtime components one by one while preserving the sing-box module and its verified tests. LaLune remains only the isolated Playwright VK token acquisition helper.
