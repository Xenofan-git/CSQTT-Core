# CSQTT Core — full ARM64 QEMU guest power-cut and recovery acceptance

Date: 2026-11-23. Scope: isolated, disposable Debian 13 AArch64 virtual machine on the Moscow development VPS, NOT production NanoPi, not an authorized VK TURN session.

## Test hardware, safety, provenance

- A new private qcow2 overlay backed by an unchanged generic Debian 13 ARM64 disk, private UEFI vars and cloud-init. QEMU virt, Cortex-A72, software TCG, 1 vCPU, 512 MiB RAM, real systemd PID1, hostname csqtt-core8-isolated.
- Only host-forwarded loopback SSH 127.0.0.1:22391; QEMU user network with restrict=on, preventing external guest connections. Existing HRCore, CSQTT deploy guests, VPS routes and production NanoPi were not changed.
- Guest-only standalone controller cross-compiled from the current files singbox_config_linux.go, singbox_manager_linux.go, singbox_transaction_linux.go and csqtt_core_socks5_linux.go plus the intentionally isolated fixture adapt/arm64-lab/csqtt-singbox-powerloss-guest.go. This is not the entire production application.
- Verified real guest Sing-box 1.14.4, binary SHA256 d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a. Actual private-loopback SOCKS5 inbound 127.0.0.1:19180 CONNECT to real echo listener 127.0.0.1:19181 with TCP payload roundtrip.
- Isolated guest unit csqtt-core-singbox-powerloss-test.service runs unprivileged as csqttlab, enabled for multi-user.target, RestrictAddress scope in code (loopback only), Restart=on-failure, bounded StartLimitBurst=3, no-new-privileges, ProtectSystem=strict and private writable state directory. Unit MUST NOT be deployed on VPS/NanoPi host.

## Real whole-guest power-off procedure

1. First boot: verified unit active/enabled, TCP echo PASS; initial config hash 768d95fc3e7c7d43e301f86a774c100a45caabfdfe131ef4f1d67f8319095cba and state panel hash 568bc6b7019bdc2321b9193f241651225f08e2434b87acef070cf08b46bd23dd; boot ID b1fe5e48-ae83-46d8-8c23-2d3cfe7c91d3.
2. Applied modified routes to the running SingBoxManager, paused at durable checkpoint candidate-published after fsync: NEW config hash 6d830ad51dbb6cdce63c2b6a4598231cca7305e8b96d82a474c1f0015747678c and NEW sidecar hash 9e025de823f65d9dcb2c13774a32e1aa9dceefdab5f2ef4b19ac66d396d9836d. Private live-apply journal file existed with mode 0600.
3. Sent SIGKILL to the EXACT disposable QEMU process after verifying the full private VM name/disk path in its argv. The entire guest kernel, systemd, Go controller and Sing-box stopped without graceful shutdown or ExecStop. QEMU disk check: no errors.
4. Rebooted the SAME disk/UEFI variables. Original config and sidecar recovered byte-for-byte, incomplete live-apply journal cleared. Initial cold boot FAILED its service acceptance: sing-box config check exceeded the fixture's overly short 7-second deadline during software-emulated boot, and exit status 75 prevented retry. This is a real observed cold-start limitation, not a config rollback failure.
5. Patched ONLY the isolated guest fixture: 30-second cold-start check deadline, classify temporary timeout/listener failures as retriable with bounded systemd restarts; corrupt/untrusted state still exits 75 fail-closed. Safely remove stale Unix control socket only if its pathname is a socket and no live peer answers. Manually verified the restored config and TCP echo after the update.
6. Repeated REAL VM SIGKILL after publishing the uncommitted candidate. Disk check again showed no errors. Rebooted the exact same disk and did NOT manually start any service.
7. Final cold boot: NEW kernel boot ID 7903972a-76bd-4406-a9f9-6cf16d0752ad. systemd independently started controller and Sing-box (enabled, active, Result=success, NRestarts=0). Both TCP listeners open; real SOCKS5 CONNECT/TCP echo PASS. Old config and panel hashes matched step 1 exactly. Neither live-apply journal nor nested transaction journal remained. Guest OS system state was running. Host default IPv4 route unchanged; default IPv6 next-hop/interface/metric/route ID unchanged (excluded naturally decreasing RA expires counter).

Result: **FULL_GUEST_ARM64_QEMU_POWER_CUT_RECOVERY_AND_SYSTEMD_AUTOSTART_PASS** for a disposable real guest. This is not a simulated boot identifier. The host itself kept power; SIGKILL of the full VM is the power-loss analogue.

## Post-test preflight secrecy fix

The test also exposed private orphaned .sing-box-live-preflight-*.json candidate files (0600) after a hard cut. Separate production-source commit d65c8a4 adds fail-closed cleanup of only private same-owner regular staged files after verifying a restored checkpoint. Symlink and unsafe-mode regression cases added. This patch is tested in native ARM64 CI separately; **do not claim that it was deployed to the already tested QEMU guest**.

## Limits / continuation

No true physical NanoPi power cutoff, real original Rust client VK TURN authentication, entire seven-tab headless application startup, production systemd deployment or seamless connection continuity were verified. The test-specific Go source uses the ignore build constraint and is compiled only when explicitly supplied with the real manager Go source files. The private qcow2, NoCloud seed and SSH keys stay OUTSIDE GitHub in a dedicated Moscow development directory. The disposable guest is shut down after acceptance to free VPS resources. For a new clean run, start from a new overlay of the original generic base and new private UEFI vars.
