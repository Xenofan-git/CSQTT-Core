# CSQTT Core 2.5.0 — isolated native ARM64 WIRE-3 full-chain TCP/UDP acceptance

Date: 2026-11-21. Status: PASS for one disposable native ARM64 network experiment; NOT yet release approval for unchanged binaries.

## Evidence

GitHub Actions: https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44907818083 — COMPLETED SUCCESS.
Experimental branch: test/csqtt-250-fullchain-arm64-20261121 at commit 352b73939820b676f08c3385210a3a584a153b9f.

One ARM64 runner with separate veth-linked client and server network namespaces and no main default route. Real Go headless HTTP /vpn/connect started a Rust v2.5 client compiled from original source with a test-only local synthetic TURN credential provider. The unchanged SHA256-pinned derived Core8 WIRE-3 server negotiated real DTLS/SRTP; client received GETCONF/TUNCONF. Go then configured Linux csqtt0 TUN and its localhost SOCKS5 listener, while pinned sing-box 1.14.4 selected CSQTT SOCKS outbound. Direct non-VPN HTTP to the remote test fixture failed before tunnel establishment, making bypass false positives visible.

TCP remote HTTP endpoint 10.67.69.1:47093: curl over sing-box -> Go SOCKS5 -> client TUN -> Rust client -> TURN -> Core8 WIRE-3 -> server TUN returned exact marker, HTTP 200, curl exit 0. Evidence marker FULLCHAIN_END_TO_END_GO_RUST250_CORE8_TUN_SOCKS_SINGBOX_TCP_PASS.

UDP endpoint 10.67.69.1:47094: actual UDP echo validated via RFC 1928 SOCKS UDP ASSOCIATE twice. First: Go CSQTT SOCKS5 :12500, exact echo returned, FULLCHAIN_GO_SOCKS5_UDP_WIRE3_RESULT 0. Second: sing-box SOCKS5 :19180 -> Go CSQTT SOCKS5 -> WIRE-3, exact echo returned, FULLCHAIN_SINGBOX_GO_SOCKS5_UDP_WIRE3_RESULT 0. Combined proof marker FULLCHAIN_END_TO_END_GO_RUST250_CORE8_TUN_SOCKS_SINGBOX_UDP_PASS.

Stop/disconnect and cleanup passed. Original ARM64 runner host default route remained unchanged. M-UI, HRCore, Tailscale, NanoPi and Keenetic production network were never modified.

## Confirmed technical findings

1. Previously patched Go LinuxTun file.Read on /dev/net/tun returned not pollable and exited immediately on ARM64. Only experimental Go lab build substituted unix.Read on a nonblocking FD with unix.Poll; this restored real bidirectional TUN-UDP packet flow. No production Go source fix merged yet.
2. Disposable client namespace intentionally had no main default route; Linux loose rp_filter=2 silently rejected the returning SYN+ACK, despite actual WIRE-3 transport. A/B control restricted to that namespace set all.rp_filter and csqtt0.rp_filter to zero and immediately allowed full TCP+UDP responses. Do not apply global rp_filter changes on production NanoPi; production FIB differs.
3. An intermediate queue diagnostic temporarily set txqueuelen=0 and created packet drops. It was removed before the successful TCP+UDP run.

## Release boundary and remaining work

The client in this joined network test is original-source v2.5 with synthetic local TURN test credentials, not byte-identical to the unchanged official ARM64 ELF used in separate realpair and Go supervisor launch acceptances. Core8 and sing-box are verified existing binaries; Go was rebuilt with a test-only TUN correction. Previously passed actual server Core8+original ELF test, Go launch, Direct sing-box, legacy CI and atomic four-file rollback remain valid for their own scope.

Outstanding: implement the narrow production-grade Linux TUN read correction in isolated candidate, validate Go lifecycle/race and combined WIRE-3 with supported routing, build a unified release candidate with exact original 2.5 ELF, test safe package upgrade and rollback again if Go changes, then decide production promotion. Avoid repeating unrelated green tests or modifying production routes.
