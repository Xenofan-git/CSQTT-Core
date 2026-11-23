# CSQTT Core — ARM64 Go headless app and Sing-box user-systemd SIGKILL acceptance

Date: 2026-11-23 UTC. Experimental branch only: test/csqtt-core-linux-unified-253-20261122. No production deployment.

## Isolated guest and real components

Disposable QEMU Debian 13 ARM64 machine csqtt-core8-isolated, virtual Cortex-A72, 1 CPU, systemd, accessible solely via host-loopback SSH at 127.0.0.1:22391 (QEMU user network restrict=on).

- Real Go headless panel/runner built for ARM64 from this branch commit b1d1fe0 and the existing reviewed original Linux Go libraries. SHA256 948a3f9a24781c27f747ebe971411218d4cc9a23d409e58cd531672c0c078aa0.
- Original Rust CSQTT client 2.5.0 binary SHA256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa.
- Selected Core8 binary SHA256 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d; actual Sing-box 1.14.4 binary SHA256 d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a.
- Rust binaries verified on disk. No credentials or authorized public VK TURN session were provided, so the Rust client was deliberately left idle: this test proves the Go/Sing-box control plane and real local SOCKS5 TCP only, NOT end-to-end authenticated WIRE-3.

User session unit csqtt-core-user-fullapp-autostart-test.service runs unprivileged as csqttlab. It is guarded by exact VM hostname, limited to the local test directories, enables explicit CSQTT_SING_BOX_AUTOSTART=1, Restarts on failure, and groups Go and child Sing-box in one service cgroup. At end of testing, the unit remained disabled and inactive. No new system-level services, routes, firewall, sysctl, credentials or external VPNs were installed.

## Actual guest process crash

1. The original Sing-box config SHA256 was 768d95fc3e7c7d43e301f86a774c100a45caabfdfe131ef4f1d67f8319095cba; sidecar SHA256 568bc6b7019bdc2321b9193f241651225f08e2434b87acef070cf08b46bd23dd; SQLite DB SHA256 b40644008aae469b58c3330c614614fd66acbdf40eecf66ffe3a560755b46a48.
2. Started real Go app through unprivileged user systemd as PID 1556, and its real child Sing-box. Go initialized SQLite and both headless API and panel. Test required the unauthenticated API to refuse requests with 503 (authentication intentionally not configured). Sing-box successfully performed a real SOCKS5 CONNECT to the guest's loopback SSH port 22 and returned an SSH-2.0 banner.
3. Checked that PID 1556 actually belonged to the expected disposable Go ELF before sending SIGKILL with systemctl --user kill --kill-who=main. Native systemd log confirmed main exit status 9/KILL, child Sing-box PID 1568 terminated with the cgroup, then restart counter incremented to 1.
4. New Go controller PID 1586 was started automatically by systemd, together with a new Sing-box. Rechecked real SOCKS5 TCP application traffic, closed unauthorized API, and verified main PID had changed. Old main process had gone.
5. Both Sing-box config/sidecar and SQLite database SHA256 hashes remained unchanged. Neither nested transaction journal nor live-apply recovery marker remained; guest IPv4 and IPv6 default route identities and boot_id stayed unchanged.
6. The guest user service was stopped following success. Verified inactive status, MainPID=0, success result, no Go API or Sing-box test SOCKS5 listener. Systemd journal retained both successful pre- and post-crash health markers.

Observed definitive result:
CSQTT_FULLAPP_REAL_ARM64_USER_SYSTEMD_GO_SIGKILL_AUTO_RESTART_SOCKS5_TCP_SQLITE_PASS.

## Scope and remaining real release gate

This is **real restart of the complete Go headless control plane plus real Sing-box**, including actual TCP data, not just a test manager. However, the unit is a user-session unit and loginctl reports Linger=no. Its successful recovery after SIGKILL is NOT a proof of pre-login auto-start after whole Debian VM power loss. The separate earlier full-QEMU power-cut trial used a narrower test-only manager fixture; do not conflate these tests. System-wide guest service deployment or creation of panel credentials was blocked by remote execution safety controls and was not claimed.

A live authorized CSQTT 2.5.0 Rust client to Core8/VK TURN, full authenticated seven-tab UI, and physical NanoPi boot/recovery remain independent untested acceptance steps. Existing production hardware and global routes were never changed. The separate disposable QEMU process still exists unless safely shut down by an authorized control method.

Source fixtures and an offline ARM64 safety-contract workflow are retained in the repository to make this acceptance reviewable without storing guest SSH keys, test disk or credentials in Git.

## Subsequent full-VM pre-login/power-cut result (same day)

The pending pre-login full Go boot and subsequent abrupt full-QEMU power-cut gate were later verified separately. See CSQTT-CORE-ARM64-FULLAPP-PRELOGIN-POWERCUT-AND-VK-GATE-2026-11-23.md. The service boot trigger preceded first SSH login; after SIGKILL of the whole VM, full Go app and Sing-box restored real TCP SOCKS5 and preserved SQLite. Genuine authorized VK TURN is still untested because the disposable guest had no test credentials or profiles and external egress remained restricted. The guest was gracefully shut down afterward and linger restored to no.
