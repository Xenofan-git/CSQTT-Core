# CSQTT Core8 — ARM64 normal-stop acceptance for lockfix 8e0a290

Date: 2026-11-21. Scope: disposable private Debian 13 ARM64 VM only.
Outcome: **PASS — real original Core8 binary + lockfix durable guard, normal
start/stop and restore of all changed sysctls**.

## Precise versions and isolation

- Experimental source branch: test/core8-kernel-sysctl-restore-lab-20261121,
  revision 8e0a2908a6cb2100c4150bf748621d8995d60233 before this report.
- Durable guard script SHA256 at host source and in guest:
  fe1af8822a752bd5e9edeaaac2efe2c7b94fdf544b55696253d94a28631ef449.
  This is the *latest* guarded lock-reacquire-after-child-exit version.
- Core8 original, already-validated ARM64 ELF:
  a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706.
  Not a newly rebuilt binary with experimental Rust source patches.
- Test guest: csqtt-250-core8-isolated-vm-20261121 QEMU user networking
  restrict=on, only host loopback 127.0.0.1:22379 forwarded to guest SSH,
  no public UDP forwarding or host system route/firewall/sysctl changes.
  Guest hostname csqtt-core8-isolated, aarch64, no IPv4 default route.
- Original manual test script reused:
  adapt/test-core8-private-actual-arm64-smoke.sh.
  Dynamically generated guest-local credentials not logged.
- Core8 listener only 127.0.0.1:46871 UDP in guest; temporary guest web
  TCP port 46872 not forwarded to the host.

## Evidence from actual disposable ARM64 execution

- Updated guard copied into guest /usr/local/libexec/ with exact SHA256 readback.
- Before Core8 run, guard preflight:
  CORE8_RECOVERY=NO_PENDING_JOURNAL; no occupied test ports or pending sysctl
  journal; guest IPv4 default route empty.
- Guest transient normal-stop unit
  csqtt-core8-private-normal-lockfix-20261121.service started via systemd-run.
  Live Core8 observation: TEST_UDP_LOOPBACK_BIND=1,
  TEST_WEB_PORT_BIND=1, /proc/sys/net/ipv4/ip_forward changed 0 -> 1.
- Service final readback: ActiveState=inactive, SubState=dead,
  Result=success, ExecMainStatus=0.
- 12 global sysctls snapshotted before/during/after actual Core8. Eight were
  changed on startup:
  net/core/default_qdisc
  net/ipv4/conf/all/rp_filter
  net/ipv4/ip_forward
  net/ipv4/tcp_congestion_control
  net/ipv4/tcp_fastopen
  net/ipv4/tcp_reordering
  net/ipv4/tcp_slow_start_after_idle
  net/ipv6/conf/all/forwarding
- Exact final status:
  TEST_GUARD_EXIT=0
  TEST_CHANGED_COUNT=8
  TEST_RESTORED_ALL=True
  TEST_SYSTEMD_PRIVATE_SMOKE_COMPLETE
  CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED
  CORE8_RECOVERY=NO_PENDING_JOURNAL
- Independent readback confirmed ip_forward=0, no 46871/46872 guest sockets,
  no pending journal. All 8 changed sysctls restored, other 4 unchanged.

## What this DOES NOT prove

- The live test covers *normal stop* of direct Core8 without a surviving
  descendant. The unique descendant-lock bug found in 8e0a290 was proven
  by offline synthetic subprocess/fake-file test (18/18 guard tests PASS,
  GitHub Actions 44936103922 SUCCESS). The descendant case itself has
  NOT been run with real Core8 on ARM64.
- A separate attempt to install and test the full sample systemd service
  under a disposable /run path was BLOCKED by execution restrictions before
  the command ran. Read-only guest verification showed no unit file, no
  credential file, and LoadState=not-found for its proposed test unit.
  Do not retry by splitting commands or using another access route.
- Prior real wrapper SIGKILL test also blocked, not tested and not passed.
- Real authorized publicly reachable VK TURN / actual VK Calls joined
  Core8->Go SOCKS5->sing-box remains unverified. No new host UDP bind.
- No production migration, no main release branch change, and no live
  execution of experimental Rust KernelNetworking/TPROXY patched ELF.
- A cooperative flock does not coordinate independent nonparticipating
  Core8 processes or unrelated sysctl writers.

## Cleanup and preserved equipment

- Guest powered off cleanly from inside using sudo systemctl poweroff.
  Host readback showed reboot: Power down; QEMU process/pidfile absent,
  127.0.0.1:22379 closed, private overlay disk preserved.
- Separate running csqtt-deploy-arm64-vm and unrelated hrcore-arm64-vm
  preserved, csqtt-lab-panel-tunnel.service active, host default route
  unchanged. No NanoPi runtime or network routing modifications.
