# CSQTT Core Linux ARM64 — durable supervisor uptime correction

Date: 2026-11-22. Isolated branch, NOT installed.

## Discovered healthy server termination after 140 seconds

The experimental Python sysctl guard previously called child.wait(timeout=140)
for the NORMAL ongoing Core8 server. A healthy server running beyond
140 seconds was interpreted as a timeout, sent SIGTERM, and possibly SIGKILL
after 5 seconds, then the guard reported exit 124.
The guest-only Restart=on-failure unit could repeatedly restart a
healthy long-running Core8 at intervals of approximately 140 seconds.

This is different from the already confirmed unsupervised SIGKILL
sysctl leak.

## Narrow guard fix

- The server may run indefinitely: one-second subprocess wait polls,
  but NO total server uptime deadline.
- An 8-second termination budget begins ONLY after a supervisor
  SIGINT or SIGTERM event. That signal forwards to the child group.
- If it exits promptly, the true child exit status is preserved.
  If it ignores shutdown, only the child's own group is escalated to
  SIGKILL and reaped, and the guard returns 124.
- Existing run/recover disk journal, inherited flock, boot-scoped
  ownership and fail-closed sysctl restoration path stay unchanged.
- No CSQTT Rust wire protocol, Go client, production installer,
  active systemd unit, route, firewall or NanoPi configuration changed.

## Offline tests completed

- Original 21 journal, ownership, symlink, old-boot, SIGKILL, external
  modification and SIGTERM tests: PASS.
- New simulated 181 healthy one-second polling intervals with a
  mocked process in milliseconds: NO stop signal, returns actual
  success, lock fd inherited: PASS.
- New actual synthetic Python child ignores SIGTERM, supervisor
  receives it, escalates only child group after test-short grace
  0.35 sec and reaps the child; exit status 124: PASS.
- Total durable guard tests: **23/23 PASS**.
- Separate guarded unit contract tests: **8/8 PASS**.
- Source and YAML syntax checks PASS.

## Remaining real ARM64 gates

The previous exact native AArch64 Core8 child SIGKILL and
durable guard restore CI45040689299 was SUCCESS with the earlier
guard. Updated source must pass the new native CI independently.
Real systemd automatic restart after SIGKILL, failure exit code 75,
power outage and reboot on a regular Linux host remain unverified.

Guard continues to refuse real /proc/sys except isolated root
AArch64 guest named csqtt-core8-isolated without default route.
Do not bypass this gate or install guest-only template on NanoPi.

## Revalidated in native Linux ARM64 GitHub Actions

- Commit 9abbe3fc2de4a1af2959ca40cbb74537a90043e3.
- CI 45046139385 SUCCESS (native ARM64):
  23/23 offline guard unit/security tests PASS, real Core8
  UDP/HTTPS/TLS bound in an isolated network+hostname namespace.
  The real child received SIGKILL; the external journal supervisor
  restored the prior IPv6 forwarding value, cleared ownership
  state, then allowed a second Core8 start and normal SIGTERM.
  Read-only log markers include
  CSQTT_CORE253_GUARD_REAL_SIGKILL_IPV6_FORWARDING_RESTORED_PASS
  and CSQTT_CORE253_GUARD_REAL_ARM64_SIGKILL_RECOVER_RESTART_ALL_PASS.
- CI 45046139361 SUCCESS (offline guarded systemd contract):
  8/8 unit invariants, systemd-analyze verify and 23/23 guard tests.
- The 181 synthetic wait polls prove absence of the previous 140
  poll timeout condition in the updated source, not actual 181
  seconds of production traffic. The real new Core8 restoration
  test confirms the new guard did not regress crash recovery.
- No systemd unit was started in CI 45046139361.
  Actual systemd auto restart, controlled power failure and
  production host default-route safety remain unverified.
