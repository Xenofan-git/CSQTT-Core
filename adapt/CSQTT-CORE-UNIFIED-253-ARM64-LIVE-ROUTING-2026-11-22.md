# CSQTT Core: real ARM64 Sing-box/SOCKS5 loopback tests and private Core8 CI

Date: 2026-11-22. Integration branch: test/csqtt-core-linux-unified-253-20261122.

## Actual Moscow results

- Used verified preexisting local CSQTT Core Linux 2.5.0 candidate Sing-box
  1.14.4 ARM64 binary (SHA256
  d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a).
  It is mode 0644 in the old archive and was *copied* to a mode-0700 lab
  directory and made executable there; original bytes and hash unchanged.
- Executed real ARM64 Sing-box under qemu-aarch64 user-mode, never TUN,
  systemd, global route or firewall changes. Go tests created and removed
  only ephemeral TCP/UDP loopback listeners and an HTTP/DNS echo fixture.
- All 10 selected real tests PASS, 14.863 sec:
  * TCP CSQTT SOCKS5 forwarding
  * first rule wins and explicit final outbound
  * real UDP ASSOCIATE through CSQTT SOCKS5
  * DNS hijack and reverse mapping
  * pinned sing-box config check
  * Rule Set syntax and reference integrity
  * authenticated external SOCKS5 without CSQTT
  * HTTP Host sniff routing from IP
  * TLS SNI sniff routing from IP
  * dead matched SOCKS5 never falls through to DIRECT
- Full Sing-box config/manager, SOCKS5, rollback and crash-journal Go
  combined suite under go test -race PASS with the actual ARM64 binary
  enabled (28.078s); no data races reported. All temporary Sing-box
  processes exited, VPS IPv4 default route unchanged.
- Guest Debian 13 ARM64 QEMU already exists and SSH is read-only
  reachable, but it has an IPv4 default route. No high-privilege
  Core8/TUN crash test was run there. A previous guest executable
  transfer was blocked by policy; no alternate unauthorized transfer
  path attempted.

## Added controlled native GitHub CI

New branch-only workflow csqtt-core-unified-253-arm64-real-runtime-e2e.yml.
Downloads exact GitHub 4-component Linux ARM64 artifact from CI45038262207,
verifies manifest and all four SHA256 plus new Core8 SHA, checks native
Sing-box v1.14.4, runs entire real Go suite with race detector on a
disposable native ARM64 GitHub runner (not the Moscow system).

Separate disposable runner job tests the new ARM64 Core8 *real process*
under unshare --net --fork --mount-proc, with loopback ONLY and no IPv4
or IPv6 default route; Python helper checks runner identity, its
network namespace differs from the runner host, no unexpected
interfaces, correct SHA and protocol. It attempts UDP listener,
HTTPS handshake, SIGTERM cleanup, restart and netns sysctl restoration.
Failure must be reported as failed CI, not a passed acceptance test.
No host route, Tailscale, NanoPi, permanent services or credentials
are touched.

## Remaining release gates

The Go/Linux client -> original CSQTT 2.5.0 Rust client -> new Core8
encrypted UDP tunnel + Sing-box end-to-end data path has NOT YET
been demonstrated in a single isolated system. The real Go SOCKS5 tests
use an intentionally mocked CSQTT SOCKS5 upstream, not the Rust tunnel.
Power-cut storage durability and automatic recovery after SIGKILL
must also be tested inside an isolated no-default-route guest.
This experiment remains UNAPPROVED for NanoPi deployment.
