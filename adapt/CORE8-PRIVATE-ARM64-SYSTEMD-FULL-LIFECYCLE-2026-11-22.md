# CSQTT Core8 private ARM64 full systemd unit lifecycle acceptance

Date: November 22, 2026 (Moscow local), November 21 UTC in guest.
Result: PASS for real private ARM64 systemd start/stop/ExecStopPost.
This does NOT cover forced-crash acceptance or real VK TURN.

## Isolated baseline

- Moscow VPS private Debian 13 ARM64 QEMU guest
  csqtt-250-core8-isolated-vm-20261121, hostname csqtt-core8-isolated.
- QEMU guest net restricted (restrict=on), sole host forward TCP
  127.0.0.1:22379 to guest SSH22. Guest has no IPv4 default route.
  No host public UDP port, firewall, default route or sysctl changed.
- Original pinned ARM64 Core8 binary SHA256:
  a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706,
  protocol CSQTT-WIRE-3. NOT rebuilt with experimental Rust patches.
- Latest private durable sysctl guard SHA256 from prior guest check:
  fe1af8822a752bd5e9edeaaac2efe2c7b94fdf544b55696253d94a28631ef449,
  experiment branch patch 8e0a290. Guard was already installed inside guest.
- Source unit adapt/csqtt-core8-private-durable-guard.service.example,
  installed as a temporary guest-only service
  /run/systemd/system/csqtt-core8-private-guard-acceptance.service.
  Guest-only generated random passwords held in root-owned mode0600
  /run/csqtt-core/private-core8.env; never printed or committed.
- Service with Type=simple, KillMode=control-group, Restart=on-failure,
  StartLimitIntervalSec=60, StartLimitBurst=3, ExecStopPost invoking the
  private guarded same-boot recover command.

## Verified events

1. Guest systemd-analyze verify passed; systemctl start succeeded.
   ActiveState=active, Result=success, NRestarts=0.
   Python guard MainPID760; real Core8 child PID765.
2. During the service, Core8 opened loopback UDP127.0.0.1:46871 and
   guest TCP0.0.0.0:46872 (not forwarded to host).
   Global sysctl net/ipv4/ip_forward changed from 0 to 1.
3. 8 of 12 tracked guest global sysctls changed during Core8:
   net/core/default_qdisc
   net/ipv4/conf/all/rp_filter
   net/ipv4/ip_forward
   net/ipv4/tcp_congestion_control
   net/ipv4/tcp_fastopen
   net/ipv4/tcp_reordering
   net/ipv4/tcp_slow_start_after_idle
   net/ipv6/conf/all/forwarding
4. systemctl stop returned success; final ActiveState=inactive,
   SubState=dead, Result=success, ExecMainStatus=0, NRestarts=0.
5. Guest journal confirms REAL ExecStopPost execution (UTC):
   22:14:57.207 started service
   22:14:59.081 guard PID760 CORE8_RECOVERY=NO_PENDING_JOURNAL
   22:15:33.984 systemd stopping service
   22:15:35.079 child PID765 stopping server
   22:15:35.337 guard PID760 CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED
   22:15:36.400 DIFFERENT Python PID936
                 CORE8_RECOVERY=NO_PENDING_JOURNAL (ExecStopPost)
   22:15:36.495 systemd deactivated service successfully
   22:15:36.510 systemd stopped service.
   The separate PID936 after guard PID760, plus unit ExecStopPost
   configuration matching explicit --private-guest recover, establishes
   execution of the actual systemd post-stop handler.
6. Full guest before/after sysctl comparison:
   SYSCTL_RESTORED_ALL=True, DIFF_COUNT=0; all 8 changed sysctls
   restored and 4 unchanged fields still equal. ip_forward=0.
   No pending sysctl journal or private Core8 test socket listeners.
   Guest IPv4 default route remained absent.
7. Removed only the guest-local temporary unit and mode0600 tmpfs
   credentials after successful stop. systemctl daemon-reload and
   independent readback: LoadState=not-found, unit inactive, env file
   absent, pending journal absent.
8. Gracefully powered off VM; kernel console Powering off / reboot:
   Power down; host QEMU PID file absent, SSH 127.0.0.1:22379 closed,
   separate guest overlay disk preserved. Existing CSQTT Deploy and
   HRCore QEMU guests kept running; tunnel service active.

## Still unverified

- Forced crash of real guest wrapper/child and recovery afterwards.
  Earlier attempt was blocked; no bypass or retry in this test.
- Actual real Core8 descendant holding an inherited lock; regression
  for this case passed only offline synthetic fake-file process tests.
- Real Restart=on-failure following a crash or power-loss recovery.
  The fully functional unit was tested only with normal start/stop.
- Full authorized real VK Calls TURN->Core8->Go SOCKS5->sing-box
  joined traffic and an independently authorized public UDP endpoint.
- Newly compiled ARM64 ELF containing experimental Rust source patches;
  the previous already-validated ELF was used throughout.
- Original release candidate branch HEAD236e7b0 left unchanged;
  no production deployment or modification of NanoPi services.
