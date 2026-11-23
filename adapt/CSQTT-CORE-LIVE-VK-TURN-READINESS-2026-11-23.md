# CSQTT Core — live VK TURN infrastructure / authorization preflight

Date 2026-11-23 UTC, experimental branch only. Result: LIVE_VK_TURN_NOT_VERIFIED_MISSING_AUTHORIZED_PROFILE_AND_CORE8_UDP_PEER.

This is an actual read-only live VK API connectivity probe, NOT an authorized VK TURN tunnel test. No private credential was read, printed, transferred between hosts or copied into GitHub. No public UDP server was started.

## Verified observations

- Original Rust CSQTT 2.5.0 auth.rs uses api.vk.ru/method/calls.getCallPreview, api.vk.ru/method/calls.getAnonymousToken and login.vk.ru/?act=get_anonym_token. A first unauthenticated diagnostic targeted a DIFFERENT generic domain api.vk.com and guessed method name calls.getAnonymToken; it returned HTTP 200 with JSON error_code=3. That response does NOT verify an actual Rust VK Calls API method and should not be treated as successful VK Calls authorization.
- Corrected, credential-free **network reachability** preflight using the actual source-defined hostnames: api.vk.ru DNS OK / HTTPS HEAD / responded HTTP 404 at root; login.vk.ru DNS OK / HTTPS HEAD / responded HTTP 418 at root. Both hosts are reachable over TLS, but neither response establishes VK Calls method success, TURN credential acquisition, allocation or data transfer. No VK Calls issuance endpoint was called without an approved test profile.
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

Decision: source-defined VK hostnames respond over HTTPS, but **VK Calls authorization NOT VERIFIED**; authenticated VK TURN and end-to-end WIRE-3/TCP/UDP NOT VERIFIED, because a configured authorized profile and test Core8 UDP endpoint are absent. No functional defect in the Rust client is concluded from missing prerequisites.
