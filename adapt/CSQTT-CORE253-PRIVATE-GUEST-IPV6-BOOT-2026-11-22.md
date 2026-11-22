# CSQTT Core Linux ARM64: private guest IPv6 and boot identity safety

Date: 2026-11-22. Isolated test branch only, not production.

## Gap: an IPv6 default route was not rejected

The existing durable sysctl supervisor rejects host /proc and any
default IPv4 route in its isolated guest. It previously did not reject
an IPv6 default route, which could allow guarded Core8 to change kernel
parameters in an unexpectedly externally routed test namespace.
Added an explicit ip -6 route show default command, and a fail-closed
error if any IPv6 default route is present.

## Gap: caller-chosen boot identifier under real /proc

The durable sysctl journal identifies the kernel boot that owns its
record. Arbitrary --boot-id-file under real /proc/sys could confuse the
stale boot recovery check. The supervisor now requires exactly
/proc/sys/kernel/random/boot_id for real kernel sysctl operations.
Synthetic fake /tmp sysctl tests retain their own boot files.

## Tests and unchanged scope

- All previous 23 fake-sysctl, durability, SIGKILL, no-uptime-limit,
  signal deadline, symlink, lock and journal tests still pass.
- New IPv6-route negative test uses mocked root AArch64 isolated guest
  with empty IPv4 route and a synthetic IPv6 default; launch refused.
- New alternate boot ID negative test verifies refusal BEFORE reading
  the boot ID or modifying kernel state.
- Total: 25/25 offline guard tests PASS.
- No real /proc writes in these tests, no production service changes,
  no external routes, no NanoPi installs. The guard remains strictly
  restricted to an isolated root AArch64 guest with no default routes.

The native ARM64 CI must independently retest this change. Real
systemd service integration and live VK/TURN remain separate gates.

## Native CI results after guest gate tightening

- GitHub Actions **45046942340 SUCCESS**, new private guest code:
  actual Core8 ARM64 guarded SIGKILL, correct sysctl recovery,
  second normal startup, no default route.
- GitHub Actions **45046942359 SUCCESS**, actual uninterrupted
  165-second Core8 live PID2391, 17 TLS/UDP and journal checkpoints,
  real SIGKILL, restored IPv6 forwarding, journal cleared,
  successful restart and normal SIGTERM.
- GitHub Actions **45046942661 SUCCESS**, eight offline systemd
  contract checks, systemd-analyze unit syntax and 25 guard tests.
- No production service installation or systemd privileged
  restart experiment performed; guest-only isolation still enforced.
