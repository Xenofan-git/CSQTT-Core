# CSQTT Core — live VK TURN infrastructure / authorization preflight

Date 2026-11-23 UTC, experimental branch only. Result: LIVE_VK_TURN_NOT_VERIFIED_MISSING_AUTHORIZED_PROFILE_AND_CORE8_UDP_PEER.

This is an actual read-only live VK API connectivity probe, NOT an authorized VK TURN tunnel test. No private credential was read, printed, transferred between hosts or copied into GitHub. No public UDP server was started.

## Verified observations

- From Moscow development VPS, live VK Calls HTTPS API resolved in DNS, replied HTTP 200 and returned a JSON top-level error with VK error_code 3 to a no-credential call. This proves HTTPS API reachability, not valid authorization or TURN allocation.
- The actual original Rust 2.5.0 client VK hash validation succeeded in GitHub Actions run 44914600867 on 2026-11-21. This historical validation does not establish current hash validity or a live TURN/WIRE-3 session.
- On the NanoPi, the installed read-only metadata view of /root/.la-lune/configs.db found the configs table but 0 rows. No existing configured CSQTT test profile.
- The previous disposable QEMU guest also had zero CSQTT profiles and no VK test credential. That VM was powered off after the earlier tests.
- Moscow VPS currently has no UDP listener on previous isolated lab ports 48779/47000; previously used temporary public Core8 lab is inactive. The Amsterdam servers are an earlier Core8 build with a different ELF hash and cannot be counted as proof of the target new Core8.
- Independently completed native ARM64 full-chain tests (GitHub Actions 45039689081) passed TCP and UDP with synthetic local TURN credentials; they do not prove real VK authentication.

## Exact remaining requirements

1. User-authorized disposable VK Calls hash or token available to a separately approved isolated test runner. Keep any secret strictly private and do not publish to chat, journal, Git or CI artifacts.
2. Separately approved, publicly reachable UDP Core8 WIRE-3 test endpoint with target ELF and return path, isolated from old servers and production routing; verify ownership, ELF checksum and cleanup.
3. Run pinned original Rust 2.5.0 with real VK Calls auth and record only allowlisted stage markers: VK Calls steps 1–4, TURN credentials present, Allocate, Permission, ChannelBind and DTLS/SRTP.
4. Verify actual end-to-end TCP and UDP over original Rust client -> Core8 -> Linux Go SOCKS5 -> Sing-box plus Direct bypass negative control, unchanged default routes and full cleanup.

Earlier remote tools blocked public UDP test listener setup and live credential acquisition. This preflight did not circumvent the restrictions through any other machine, proxy, port or runner.

Decision: VK Calls HTTPS API connectivity PASS; authenticated VK TURN and end-to-end WIRE-3/TCP/UDP NOT VERIFIED, because a configured authorized profile and test Core8 UDP endpoint are absent. No functional defect in the Rust client is concluded from missing prerequisites.
