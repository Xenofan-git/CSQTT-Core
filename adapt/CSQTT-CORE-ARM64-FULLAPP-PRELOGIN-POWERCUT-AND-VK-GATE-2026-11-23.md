# CSQTT Core — full ARM64 pre-login boot, full QEMU power-cut and VK TURN readiness

Date: 2026-11-23 UTC. Scope: isolated, disposable QEMU Debian 13 AArch64 guest on the Moscow development VPS, NOT production.

## Real tested software

- Full experimental Go headless Linux runner, SQLite DB and seven-tab panel, with the current branch's SingBoxManager and explicit CSQTT_SING_BOX_AUTOSTART=1. Linux ARM64 Go ELF SHA256: 948a3f9a24781c27f747ebe971411218d4cc9a23d409e58cd531672c0c078aa0.
- Real Sing-box 1.14.4, SHA256 d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a. Guest SOCKS5 port 127.0.0.1:19180 routed a real TCP CONNECT to its loopback SSH on 127.0.0.1:22 and received an SSH-2.0 banner.
- Original Rust CSQTT 2.5.0 client SHA256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa and selected 2.5.3 Core8 SHA256 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d; Rust files were checked by SHA, Core8 CLI returned CSQTT-WIRE-3. Rust was not connected to real VK TURN in this experiment.
- QEMU virt Cortex-A72 software TCG, one ARM64 CPU, 512MB RAM, dedicated qcow2 overlay, private UEFI vars. Guest hostname csqtt-core8-isolated; QEMU external guest network disabled with restrict=on, only host-loopback SSH 127.0.0.1:22391 forwarded.
- Only user-level systemd unit csqtt-core-user-fullapp-autostart-test.service was allowed to start the Go app, with Restart=on-failure, KillMode=control-group and NoNewPrivileges. Old competing system-level Sing-box power-loss test unit disabled.

## Actual pre-login cold boot acceptance

Before this stage the user unit had been disabled and Linger=no; success in a logged-in user session was insufficient. On this disposable guest enabled user lingering with loginctl and enabled the test user unit in default.target. First boot revealed a real port conflict with an old, still-enabled system-level Sing-box fixture, so that boot was rejected as invalid. Disabled/stopped ONLY that old fixture in the guest before repeating.

Clean cold reboot of the same VM disk: kernel boot ID c1c6a0f7-7f96-4a50-91ae-9215b8e0798e, different from prior 14b8c5dc-1dc8-47c5-a33e-9dc9be3d179e. User manager started at uptime 102.349s. User service startup at 108.835s **preceded the first SSH login at 117.220s**. Go SQLite and settings initialized, Sing-box opened real SOCKS5, user unit reported ActiveState=active, Result=success, NRestarts=0, TCP SSH banner PASS at 125.495s. NOTE: proof is that the app started before login; full health confirmation occurred *after* the first SSH login, not before it.

## Whole QEMU guest power-cut with full Go and Sing-box running

Captured real pre-cut direct-only Go/Sing-box TCP pass, Go PID 611 and the SHA256 state below. Checked the exact disposable QEMU PID argv, including named VM and guest overlay, then SIGKILLed the entire QEMU process (no guest ACPI/systemd shutdown). qemu-img check reported No errors were found on the image. Rebooted the SAME qcow2 and UEFI state, with the SAME restricted network, under new QEMU PID 792816.

Real new boot ID 96c06201-4b8b-4fb7-a2d6-824a5b7f5254. Guest user systemd started full Go app again automatically at uptime 107.649s, with no manual systemctl start. Go reopened SQLite/settings, spawned real Sing-box, opened headless HTTP and panel, and passed real SOCKS5 TCP SSH-banner at uptime 125.532s. Unit reached ActiveState=active, Result=success, NRestarts=0. Old fixture inactive, no nested transaction/live apply markers, no altered VPS IPv4/IPv6 default route. The VM host's disk stayed powered: this was an abrupt whole *guest VM* power loss analogue, not a physical NAND/SSD power cut.

Hashes preserved byte-for-byte across clean boot and whole guest forced off/on:

| File | SHA256 |
|---|---|
| Sing-box config.json | 768d95fc3e7c7d43e301f86a774c100a45caabfdfe131ef4f1d67f8319095cba |
| Sing-box panel sidecar | 568bc6b7019bdc2321b9193f241651225f08e2434b87acef070cf08b46bd23dd |
| Go SQLite configs.db | b40644008aae469b58c3330c614614fd66acbdf40eecf66ffe3a560755b46a48 |

Accepted marker: CSQTT_FULLAPP_ARM64_QEMU_PRELOGIN_USER_SYSTEMD_AND_WHOLE_POWER_CUT_REAL_SOCKS5_TCP_PASS.

## Real authorized VK TURN readiness — NOT ACCEPTED

An independent read-only audit of the disposable guest found **zero configured CSQTT profiles** in SQLite, no panel authorization file and no credentials present in the tested environment variables CSQTT_PASSWORD, VK_TOKEN, VK_ACCESS_TOKEN or CSQTT_WEB_PASS. Guest outbound traffic was deliberately restricted. No user-authorized VK TURN account/session was available and no real VK TURN connection was attempted or proven. This is a missing test prerequisite, not evidence of a Rust implementation fault. No secrets were printed, copied into Git or added to journals.

Previous complete native ARM64 synthetic TURN full-chain test (GitHub Actions run 45039689081, SUCCESS) truly exercised Go -> original Rust 2.5.0 client -> selective Core8 CSQTT-WIRE-3 -> Go TUN/SOCKS -> real Sing-box, TCP and UDP with local synthetic TURN credentials, Direct bypass and unchanged default route:
https://github.com/Xenofan-git/CSQTT-Core/actions/runs/45039689081
It does **not** prove external VK login or authorized VK TURN. Completion requires a user-authorized disposable VK test identity/session, a CSQTT profile and a separately approved isolated test egress route. Never place token values in GitHub, journal or user-facing logs; never silently use a production credential.

## Guest teardown and scope limitations

Following successful acceptance the guest user test unit was disabled/stopped, lingering restored to **Linger=no**, both test listeners closed. Graceful guest systemctl poweroff completed; console ended with systemd-shutdown Powering off and reboot Power down; QEMU removed its PID file and localhost SSH forwarding was gone. Host available memory increased from ~0.8 GiB to ~1.7 GiB; host routes were unchanged. Private guest disk retained for future explicitly authorized tests; no existing other VM or production services changed.

This proves full Go headless + local Sing-box pre-login start, real TCP traffic, durable SQLite/config and recovery from a full guest QEMU process kill. It does not prove physical NanoPi boot, the fully authenticated seven-tab panel, physical flash power-cut or a real Rust/VK TURN session.
