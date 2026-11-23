# CSQTT Core — native NanoPi R3S LTS ARM64 replacement, 2026-11-23

User-approved replacement of the complete previous CSQTT application installation on the actual live NanoPi R3S LTS running Debian 13 ARM64. No changes to unrelated networking/services.

## Installed native CSQTT Core

All five AArch64 ELF files were transferred in one verified test-only 46 MiB package (tar.gz SHA256: 355c026135d1a440618f682787162ff530d0194265469f2b8d13ac23009ef743) from the Moscow development VPS to the physical NanoPi over the authorized Tailscale SSH route. Individual SHA256 passed the archive manifest.

| Binary within /usr/local/libexec/csqtt-core/bin/ | SHA256 |
| --- | --- |
| csqtt-core-headless (actual Go headless controller/panel) | 948a3f9a24781c27f747ebe971411218d4cc9a23d409e58cd531672c0c078aa0 |
| csqtt-client-250 (original Rust client 2.5.0) | 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa |
| csqtt-core8 (selected updated Rust WIRE-3 Core8) | 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d |
| sing-box (real 1.14.4) | d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a |
| csqtt-config-seed (manager-built direct-only initial config generator) | 123aa14dbcfcc1020fb21d7f5909a49ac681ff52bfa80afea571447485213fde |

Active unit: /etc/systemd/system/csqtt-core.service, enabled. It runs the Go app and owns its one Sing-box child in a KillMode=control-group service, with on-failure restart and explicit CSQTT_SING_BOX_AUTOSTART=1. Service currently runs as root with NoNewPrivileges=true to preserve native TUN operations; root process hardening warrants a separate audit before exposing to untrusted traffic.

Data: /var/lib/csqtt-core/.la-lune/configs.db (new SQLite DB with 0 saved profiles), settings.json, private Sing-box generated config and panel sidecar in /var/lib/csqtt-core/sing-box (0600). Sing-box final outbound is DIRECT, CSQTT outbound disabled and inbound ONLY 127.0.0.1:19180. No default-route manipulation and no client policy assigned. API listens ONLY on 127.0.0.1:1062; native seven-tab panel on 1061; new Nginx reverse proxy on 18788 maintains the prior alternative panel port.

New root-only randomly generated panel authentication: admin username, password stored ONLY in /etc/csqtt-core/panel.env mode 0600. Password not printed in chat/journals/GitHub. HTTP Basic without TLS must be limited to a trusted local/private tunnel; HTTPS is advisable before use across untrusted networks.

## Privately backed-up old deployment and removal

Amsterdam VPS path: /srv/projects/CSQTT-Core/backups/nanopi-original-csqtt-20261123/ ; mode 0700, private files 0600, SHA256SUMS verified with sha256sum -c and gzip integrity. These archives may contain old private keys and credentials and MUST NOT be publicly shared.

| Archive | SHA256 |
| --- | --- |
| nanopi-original-csqtt-20261123.tar.gz | 126eee4a53192875d103fa1678d49ff99f40c2a4800049e108ef24785cef3112 |
| old-csqtt-opt-extra-20261123.tar.gz | e06a0ac99a20f3d93206295292aee0d7bffe5793efcfa63317b97f568d9973a6 |
| old-csqtt-private-config-extra.tar.gz | 5a69234005ef6f8972636ba68fada6ebeadbb10605887bd7cb23fd940d0c7d88 |
| old-csqtt-small-remnants-20261123.tar.gz | cfeb4921ada9649552c8373b7aa4f2a66eed3e227d2bf229d8db49c8567dcd05 |

The old service, database, settings, binary, server assets, static panel, old Nginx vhost and snippets were removed after remote backup integrity checks. The old /opt application/runtime/build/test trees, historical root copies and old source/test trees were also removed after their separate archive was verified on Amsterdam. This intentionally does NOT delete independent HydraRoute historical Nginx backup archives or project documentation journals, which are not active old CSQTT software.

## Real on-device acceptance, not QEMU

- Authenticated native Go 1061, API 1062 and Nginx 18788: unauthorized requests HTTP 401; private Basic Auth gives HTTP 200.
- All seven tabs (Connect, Tunnel, Deploy, Logs, Direct, Route, Sing-box) present in delivered HTML.
- Real Sing-box 1.14.4 only one PID in system process table; actual SOCKS5 handshake, CONNECT to local 127.0.0.1:1062 and authenticated HTTP /ping response 200 ok:true on this physical NanoPi.
- Native csqtt-core.service controlled restart: old Go PID 454661 -> new PID 455474, old Sing-box PID 454682 -> new PID 455493. Go SQLite, Sing-box config and sidecar remained intact, no restart loop. Go service stayed active with NRestarts=0.
- HydraRoute Core, M-UI/Mihomo, Tailscale Xray proxy remained active; existing Xray and Mihomo processes untouched. Host default routes before/after cutover/restart/purge identical.
- Actual source-defined Rust VK hostname DNS/HTTPS connectivity from NanoPi: api.vk.ru root HTTP 404; login.vk.ru root HTTP 418. This is hostname reachability, NOT authorized VK Calls success or TURN allocation.
- Native Rust Core8 CLI returned CSQTT-WIRE-3. The original Rust 2.5.0 client binary integrity was verified.

## Not yet proved

The new SQLite DB was deliberately reset to zero profiles. No authorized user-provided disposable VK/CSQTT profile is available. Consequently real VK Calls token derivation, real TURN Allocate/Permission/ChannelBind, DTLS, and end-to-end Rust client 2.5.0 -> Core8 -> Sing-box TCP/UDP via VK TURN are NOT YET TESTED on this physical NanoPi. The previous GitHub synthetic TURN test does not imply real VK success. Adding a user-authorized test profile to the new UI and configuring an approved isolated peer are the remaining prerequisites.

Main report intentionally excludes credential contents and confidential network keys.
