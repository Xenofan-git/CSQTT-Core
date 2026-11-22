# Core8 — isolated ARM64 systemd restart-after-failure acceptance

Date: 2026-11-22 (Moscow local).
Status: **PASS for normal, nonzero child process exit, bounded systemd
Restart=on-failure, ExecStopPost recovery and durable guard state clearing**.
Explicitly NOT a forced SIGKILL/crash of real Core8, and NOT a real VK TURN
data-plane test.

## Why this test was needed

- Previous complete real guest Core8 start->stop->ExecStopPost acceptance
  was already PASS and documented in
  CORE8-PRIVATE-ARM64-SYSTEMD-FULL-LIFECYCLE-2026-11-22.md.
- The restart-on-failure action had not been exercised within real
  ARM64 guest systemd. Existing 18 fake-file guard regression tests were
  not equivalent to systemd supervised restart behavior.
- An actual real guest wrapper+Core8 forced-kill test was blocked by
  execution restrictions previously; NOT retried or circumvented.
- No separately authorized public UDP VK TURN endpoint/credential path was
  available, so no live VK Calls joined TCP/UDP session was attempted.

## Isolated scope

- Moscow VPS pre-existing disposable guest
  csqtt-250-core8-isolated-vm-20261121, Debian 13 aarch64, QEMU
  -netdev user,restrict=on; the ONLY host-forwarded port was SSH
  127.0.0.1:22379. Guest hostname csqtt-core8-isolated and no
  IPv4 default route.
- Existing verified private durable Python guard SHA256
  fe1af8822a752bd5e9edeaaac2efe2c7b94fdf544b55696253d94a28631ef449,
  and original Core8 ARM64 ELF SHA256
  a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706.
- New explicit opt-in fixture script
  adapt/test-core8-private-arm64-systemd-restart-failure-fixture.sh.
  It runs only in this hostname/architecture with no guest IPv4 default
  route and checks the pinned guard SHA256.
- ALL modified values are under private guest
  /tmp/csqtt-core8-restart-fixture-20261122/proc/sys, a synthetic
  filesystem tree, NEVER the real /proc/sys. The child is a short Python
  subprocess writing the allowlisted 12 FAKE values to expected Core8
  values, then returning exit status 42. No real Core8 process is
  started by this particular fixture.
- Guest ephemeral systemd unit
  /run/systemd/system/csqtt-core8-private-restart-fixture-20261122.service
  launches the real guard around the fake child, with
  Restart=on-failure, RestartSec=1, StartLimitIntervalSec=60,
  StartLimitBurst=3, KillMode=control-group, and independent ExecStopPost
  recover on each failed invocation.
- No real sysctl writes, firewall, routes, DNS, host ports, VPN servers,
  or NanoPi services changed; no secret credentials used.

## Evidence

- Guest systemd-analyze verify of temporary unit PASS.
- Three test child invocations each logged FIXTURE_CHILD_EXIT_42.
- Each invocation ran the guard and logged:
  CORE8_RECOVERY=NO_PENDING_JOURNAL,
  CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED,
  independent ExecStopPost CORE8_RECOVERY=NO_PENDING_JOURNAL.
- Three retries recorded with restart counters 1/2/3. Guest journal
  explicitly stated Start request repeated too quickly.
- Final systemd readback:
  Result=exit-code, ActiveState=failed, ExecMainStatus=42, NRestarts=3.
  The stop condition is proven by the actual journal and the 3 attempts;
  Result=exit-code is the correct observed status on this systemd build,
  not start-limit-hit.
- The fixture independently counted exactly three attempts, compared
  all twelve FAKE parameter values with the original snapshot,
  RESTORED_ALL=True, PENDING_JOURNAL_EXISTS=False, and ended with
  SYSTEMD_FAILURE_RESTART_LIMIT_AND_RESTORE_PASS and shell exit 0.
- Initial harness run returned exit 1 only because it expected
  Result=start-limit-hit; a second run returned 141 because grep -q
  prematurely closed a journalctl pipe under pipefail. Both were
  TEST ASSERTION / PIPELINE issues, not guard/recovery failures.
  The final version accepts the actual exit-code result, checks the
  rate-limit journal message from a captured file and verifies
  exactly three restoration records. Final end-to-end run exit 0.
- Post-test guest readback: temporary systemd unit LoadState=not-found,
  temporary tree removed, no pending real-guard journal, real guest
  /proc/sys/net/ipv4/ip_forward remained 0, no Core8 port 46871/46872
  listener, guest IPv4 default route absent. No production side effects.

## Remaining gates

- Real Core8 or wrapper forced SIGKILL under actual systemd and supervised
  recovery: NOT tested, previously blocked.
- Real Core8 failure due to application error with a dynamic changed
  kernel configuration, and automatic reactivation after restart:
  NOT tested (this fixture used fake sysctls and a synthetic child).
- Actual inherited flock holding descendant on real Core8 ARM64:
  synthetic regression only.
- Actual authorized VK TURN -> WIRE3 -> Go SOCKS5 -> sing-box
  end-to-end and publicly reachable user-authorized UDP lab: NOT tested.
- No new experimental Rust-patched Core8 ELF was compiled or deployed.
- Validated release branch release/csqtt-250-wire3-candidate-20261121,
  HEAD236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 unchanged.

## Cleanup

- The test script automatically removes ONLY its temporary unit and
  private fake sysctl tree, resets failed state of test-only unit,
  and reloads guest systemd.
- The separate private guest is shut down gracefully after verification;
  its image is preserved. Working Deploy ARM64 and unrelated HRCore
  VM and all production host network settings are preserved.
