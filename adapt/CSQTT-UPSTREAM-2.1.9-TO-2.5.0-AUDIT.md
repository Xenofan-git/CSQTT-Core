# CSQTT upstream 2.1.9 → 2.5.0 compatibility audit

**Date:** 2026-11-06  
**Target branch:** `fix/direct-dnsmasq-dualstack-production`  
**Purpose:** identify upstream 2.5.0 improvements worth adapting without replacing the proven Linux ARM64 dataplane or the approved sing-box architecture.

## Pinned source references

- CSQTT v2.1.9: `amurcanov/csqtt` tag `v2.1.9`, commit `446293aa2e873ac5323ef6fd2316d9b81d966c11`.
- CSQTT v2.5.0: `amurcanov/csqtt` tag `v2.5.0`, commit `fd936ef087c34cc46f26111dc2361f43b8b03c07`.
- The GitHub compare endpoint reports no common ancestor for these two tag commits. Therefore this audit compares the two immutable source trees, not a linear commit range.
- Tree inventory: v2.1.9 has 263 tracked files; v2.5.0 has 281. The v2.5.0 tree adds 46 paths, removes 28 paths, and changes 178 existing paths.
- The upstream `CHANCHELOG.md` at v2.5.0 still identifies itself as v2.1.9. Do not use that file's heading as proof of version-specific changes; validate source and tests directly.

## Confirmed 2.5.0 changes relevant to this adaptation

### 1. Deploy security and usability

- `DeployTab.kt` adds optional web-panel secret-path protection, UUID regeneration, and explicit main-password validation (`^[a-zA-Z0-9]{16}$`).
- `SettingsStore.kt` persists the secret-path enabled state and UUID per profile.
- `SshHostKeyStore.kt` adds SSH host-key TOFU pinning (SHA-256 fingerprint) and rejects a changed key after initial trust.
- The current Linux adaptation derives its installer from pinned v2.1.9 and invokes OpenSSH with `StrictHostKeyChecking=accept-new`. This already has first-use trust-on-first-use semantics, but does not expose a UI for reviewing or rotating the pin. Do not replace this with unconditional host-key acceptance.
- Secret-path support is a candidate for a separate, narrowly scoped security feature. Before porting, trace every web-panel route, reverse-proxy rule, certificate/renewal path, health check, and uninstall path. The current deploy UI/API contract and namespaced installer must remain compatible.

### 2. Tunnel recovery and diagnostics

- `VpnRecoveryPolicy.kt` distinguishes recoverable service/TUN-FD failures from terminal TUN-establish and permission-revocation failures.
- `TunnelManager.kt` adds structured tunnel diagnostics, server-routing state, and a bounded escalation window for repeated TUN loss.
- The Linux adaptation already has its own production-patched CSQTT liveness/recovery protocol and CI coverage. Do not transplant Android VPN-service recovery code. Compare failure classification and escalation semantics against the Linux process lifecycle first; only add missing platform-neutral behavior with tests.

### 3. Routing and profile features

- `SettingsStore.kt` and `ExceptionsTab.kt` add per-profile domain/IP routing modes, blacklist/whitelist handling, Russian-zone exclusions, template domains, application whitelist, and persisted profile names.
- `TurnSubnetRegistry.kt` and `TurnSubnetsDialog.kt` add explicit TURN subnet/IP selection and latency probing.
- These are Android-client/server-routing features, not direct equivalents of the Linux sing-box route editor. Do not merge their data model into sing-box route rules or the existing DNSmasq Direct path. In particular, preserve the current separation between the user's Keenetic Direct rules, CSQTT policy routing, and sing-box first-match routing.

### 4. Import/export and QR support

- v2.5.0 adds `ImportExportDialog.kt`, `QrScannerDialog.kt`, and import/export methods in `SettingsStore.kt`.
- This may improve portability of Android profiles, but it is outside the currently approved scope for the Linux panel. Do not add it during sing-box stabilization.

### 5. Dataplane and installer changes

- v2.5.0 changes many Rust client/server files and adds new transport/dataplane components. The tree also removes some earlier dispatcher, queue, and performance-test files.
- `deploy.sh` grows from 91,157 to 105,956 characters. New areas include CPU mitigation tuning and Let's Encrypt IP-certificate adoption/fallback/renewal helpers; Docker and installer lifecycle code also changed.
- Do not wholesale-replace the currently tested v2.1.9-derived namespaced installer or Rust dataplane with v2.5.0. Any future cherry-pick must preserve CSQTT namespacing, preflight-before-stop behavior, ownership checks, data preservation, and the existing disposable-VM install/uninstall matrix.

## Initial transfer decisions

| Candidate | Decision | Reason |
|---|---|---|
| SSH host-key TOFU | Keep current `accept-new` behavior; later add explicit pin inspection/rotation only if needed | Same security principle already exists in the Linux SSH invocation |
| Deploy secret path | Investigate as a separate security improvement | Potentially useful, but requires complete route/proxy/certificate/health-check audit |
| Recovery classification | Compare semantics; do not transplant Android service code | Linux adaptation already has its own tested recovery protocol |
| Domain/IP and profile routing | Do not port into sing-box | Different control plane; risks mixing Direct DNSmasq, CSQTT and sing-box routing |
| TURN subnet registry | Do not port | Android-specific and not part of approved Linux scope |
| Import/export and QR | Defer | Useful but not required to finish sing-box |
| Rust dataplane and deploy.sh wholesale update | Reject for this stage | Too broad; incompatible risk to the proven v2.1.9-derived Linux deployment and protocol |

## Verified Linux integration baseline after the sing-box logical-rule fix

- Fix commit: `d2779751ce644aff549ac0189ebb52b3185b8ef7` — logical route rules no longer inherit the ordinary-rule `inbound` selector.
- Workflow run #1297: [GitHub Actions](https://github.com/Xenofan-git/CSQTT-sing-box-for-Debian-Ubuntu-ARM64/actions/runs/42114527446).
- Re-run attempt: both jobs SUCCESS:
  - pinned original Go Linux ARM64 build and pinned sing-box 1.14.4 config validation;
  - real CSQTT artifact in Debian ARM64 system VM, including SOCKS5 TCP/UDP, Direct IPv4/IPv6 policy, deploy lifecycle, actual Deploy API, recovery and clean-runtime acceptance.
- This is a CI validation baseline, not authorization to deploy to the production NanoPi.

## Next actions

1. Continue auditing the v2.5.0 Deploy secret-path feature against the Linux panel/API/installer before deciding whether it can be safely adapted.
2. Compare v2.5.0 tunnel recovery semantics with the existing Linux liveness/recovery implementation; add only demonstrably missing, platform-neutral safeguards.
3. Keep the existing v2.1.9-derived installer and dataplane unchanged unless a narrow, tested improvement is justified.
4. After the audit decisions are implemented or explicitly deferred, update the project README and NanoPi journal to the verified state.
