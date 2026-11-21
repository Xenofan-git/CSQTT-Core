# CSQTT Core: native ARM64 Go supervisor → original Rust client 2.5.0 (21 November 2026)

## Provenance

Separate test-only branch test/csqtt-250-go-supervisor-gated-20261121, commit ed7516a82c909e2f405a2e655c0f849c1ed08964, **not merged into the working Go WIRE-4**. GitHub Actions run 44894005565 SUCCESS, opt-in mode isolated-only.

Candidate ZIP SHA256: 513c71630655d51ebdb4ea506116b13146772e100144938ffc16f26599d34427. Four ARM64 ELF components, independently SHA256 checked both on Moscow VPS and physical NanoPi:
- Go supervisor: ebb64d1edcfcac6495dcaef4da48c2571a736fa11d977bf06015720e88d61a59.
- Official CSQTT 2.5.0 Rust client: 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f44760fa (original pinned fd936ef).
- Derived Core8 Rust server: a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706 (commit 91b1540).
- sing-box 1.14.4: d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a.

## Native physical NanoPi test: PASS for control-plane Go→Rust

The actual native ARM64 Go supervisor was executed under a pi-owned user+network namespace with loopback only and **no default route**, never installed into systemd. Management endpoint temporarily reported NanoPi offline, so the Moscow VPS's authorized Tailscale SSH to NanoPi was used; all test files stayed under a uniquely named /tmp directory.

The real HTTP API returned 200 and reported candidate v2.5.0. POST /tunnel/auth configured synthetic password; POST /configs saved a synthetic csqtt://connect link for intentionally nonexistent localhost peer 127.0.0.1:9. POST /vpn/connect returned 200 ok=true and the actual existing LinuxRunner launched the verified original Rust client ELF. API logs concealed command secrets; no test password appeared in logs. POST /vpn/disconnect returned 200 and cleaned the client process. Outer NanoPi default route remained via 192.168.4.1; M-UI, HydraRoute Core, Tailscale stayed active.

The fake unreachable peer was intentional. Rust client attempted name resolution without a default route but could NOT establish the VPN; no TUNCONF, no SOCKS5/sing-box dataplane tested. This is a valid **HTTP Go supervisor → real Rust process lifecycle** test, not end-to-end VPN transport acceptance.

## Still open

1. Full authenticated Go→original Rust 2.5 client→Core8 WIRE-3/TUN→SOCKS5→sing-box dataplane in a single isolated environment. Previously verified separate Rust pair and sing-box tests do not substitute.
2. Cohesive upgrade/forced rollback of the complete four-component bundle. Earlier run 44890007663 validated the server-only installer, not the combined package.
3. Safe integration into main Go workflow after both gates, with legacy WIRE-4 unchanged until accepted.

Amsterdam VPS journals are primary; NanoPi local journals and exact Amsterdam mirrors are secondary.
