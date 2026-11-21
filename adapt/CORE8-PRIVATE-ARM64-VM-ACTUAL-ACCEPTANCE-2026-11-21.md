# Core8 private ARM64 VM: real binary sysctl lifecycle acceptance (2026-11-21)

Result: PASS for real Core8 normal termination and external durable guard
restoration in a disposable ARM64 VM. This is NOT proof of an abrupt
supervisor crash and NOT proof of live VK TURN.

## Scope

- Moscow development VPS guest csqtt-250-core8-isolated-vm-20261121, Debian 13
  ARM64 QEMU. Started using staged start-loopback-only.sh, with QEMU
  user networking restrict=on and ONLY host-local SSH 127.0.0.1:22379 forwarded.
- Guest hostname csqtt-core8-isolated, uname aarch64, /dev/net/tun present,
  no IPv4 default route. Guest had IPv6 RA default route but external egress
  restricted by QEMU restrict=on.
- Original already-validated Core8 ARM64 ELF SHA256
  a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706,
  CLI --protocol-revision exactly CSQTT-WIRE-3.
- Experimental durable guard staged INSIDE ONLY the private guest at
  /usr/local/libexec/core8-private-durable-sysctl-guard.py and private
  state /var/lib/csqtt-core-private/sysctl-guard. Dry recover before run
  yielded CORE8_RECOVERY=NO_PENDING_JOURNAL.
- Manual test script adapt/test-core8-private-actual-arm64-smoke.sh used
  generated ephemeral guest-local passwords (not logged), launched Core8
  UDP only at 127.0.0.1:46871 and guest-local web port 46872.
  Executed under disposable systemd-run transient service
  csqtt-core8-private-actual-smoke-20261121, KillMode=control-group.
  This does NOT test the full inert service example ExecStopPost/restart.
- Independent systemctl readback: Result=success, ExecMainStatus=0,
  ActiveState=inactive, SubState=dead. Logs reported
  TEST_UDP_LOOPBACK_BIND=1, TEST_WEB_PORT_BIND=1, TEST_GUARD_EXIT=0.
- Actual Core8 live /proc sysctl observation: net.ipv4.ip_forward 0 -> 1.
  Twelve global sysctl fields captured before/during/after. Eight changed:
  net/core/default_qdisc
  net/ipv4/conf/all/rp_filter
  net/ipv4/ip_forward
  net/ipv4/tcp_congestion_control
  net/ipv4/tcp_fastopen
  net/ipv4/tcp_reordering
  net/ipv4/tcp_slow_start_after_idle
  net/ipv6/conf/all/forwarding
- Durable guard emitted CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED.
  Independent second recovery emitted CORE8_RECOVERY=NO_PENDING_JOURNAL.
  Snapshot comparison TEST_CHANGED_COUNT=8 and TEST_RESTORED_ALL=True:
  all 8 changes reverted and other 4 values remained unchanged.
  TEST_SYSTEMD_PRIVATE_SMOKE_COMPLETE. Guest ip_forward returned to 0.
  No pending journal, Core8 local UDP/TCP listeners closed, no IPv4
  default route added.
- Host route and other working Deploy and HRCore guests were not changed.
  No public UDP port opened and no NanoPi runtime modified.

## Limits and still-open acceptance

- Proposed second test, intentionally terminating the ACTUAL wrapper and
  actual Core8 child, was BLOCKED BY EXECUTION RESTRICTIONS BEFORE RUN.
  Do not bypass it with another access path or rephrase into chunks.
  Prior 17 offline process/fake-file tests remain only synthetic proof.
- The original verified Core8 ELF was used, NOT a newly built executable
  from the experiment's Rust KernelNetworking/TPROXY patches. This PASS
  validates the external durable Python guard on actual ARM64 VM only.
- No full systemd sample-unit deployment and ExecStopPost test. No actual
  external VK TURN / real VK Calls joined Core8+Go+sing-box chain.
- Never infer production readiness, power-failure safety, or universal
  multi-process sysctl ownership from this bounded guest test.
