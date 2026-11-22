# CSQTT Core 2.5.0 selective 2.5.3: real 165-second uptime verification

Date: 2026-11-22. Linux-only experimental integration branch.

## Why this experiment is necessary

The durable Core8 external supervisor formerly used an unconditional
child.wait(timeout=140), forcibly stopping a healthy server after 140
seconds of real running time. Commit 9abbe3fc2de4a1af2959ca40cbb74537a90043e3
fixed it with indefinite repeated wait polling, and the 181-timeout-poll
simulation and real SIGKILL recovery CI45046139385 PASSED.

The mock does not prove actual 140+ seconds of uptime. This new experiment
tests **165 seconds of real wall-clock running time of the SAME ARM64 Core8
process** before killing it on purpose.

## Execution and safety boundaries

Reuses existing, previously successful native GitHub ARM64 test code
adapt/test-csqtt-core253-durable-arm64-guard-crash.py, the exact approved
four-component Core8 binary with SHA256
362a27927764e4e728b6327ce3b62a229c376ae3512fdce3359c4d3bd9b8017e,
and existing fsynced boot-scoped durable supervisor.

The checker refuses any invocation outside a disposable GitHub-hosted
root ARM64 NET+UTS namespace, requires the hostname
csqtt-core8-isolated, no IPv4/IPv6 default route, isolated network
namespace distinct from host, and working real /dev/net/tun.
It does not run a production service or change host default route.

- Start Core8 with durable guard, USB/TUN-free loopback-only UDP 47000
  and HTTPS 47002.
- Record the actual Core8 PID and monotonically measured start time.
- Repeatedly every ten real seconds ensure SAME PID still exists,
  supervisor is alive, both listeners still bind, TLS handshake works,
  durable ownership journal persists and no default route appears.
- Require continuous elapsed >=165 real seconds.
- Send SIGKILL only to the Core8 child group, leave guard alive.
- Require restored sysctls (particularly IPv6 forwarding), cleared
  journal, released listeners, and repeat normal start + SIGTERM.
- Both native build/test inputs are pinned to previous GH artifact run
  45038262207. Timeout budget 330 seconds (no default route).

This is NOT a power-cut or true systemd Restart=on-failure test.
No working NanoPi, active VPN, host sysctl or VPS systemd touched.

## Status

Prepared source and CI; outcome must be read from its own GitHub
Actions run before describing true 165-second uptime as passing.

## Native AArch64 GitHub result: PASS, twice

- Branch commit d869ab4c56fb09a3436d83a5dea4299f4765b27d:
  GitHub Actions **45046748969 SUCCESS**, actual live Core8 PID2352
  survived checkpoints 0,10,...,160 sec, actual monotonic elapsed165.0s,
  TLS/UDP port checks + journal remained intact. Then real SIGKILL
  of only the Core8 child, guardian restored IPv6 forwarding, cleared
  journal, started fresh Core8 and gracefully stopped. Success marker
  CSQTT_CORE253_REAL_ARM64_165S_UPTIME_SIGKILL_RECOVERY_JOB_PASS.
- After subsequent private guest hardening commit
  6154e6b5a309a53c80911573b3582ab77d831206:
  GitHub Actions **45046942359 SUCCESS**, actual live Core8 PID2391
  survived 17 checks over real 165.0s, then guardian handled child
  SIGKILL, sysctl restore and next graceful run/stop correctly.
  Native ARM64 pipeline simultaneously ran 25/25 offline guard tests.
  Short native control run 45046942340 SUCCESS and guarded systemd
  offline contract 45046942661 SUCCESS.
- These are real uptime tests with a real ARM64 Linux Core8 ELF and
  actual TLS/UDP readiness, not simulated waits. They are *not*
  traffic-load or unattended month-long soak tests.

## Remaining boundaries

Service was intentionally invoked under the private NET+UTS supervisor,
NOT as a real systemd unit. Real systemd auto-restart after SIGKILL
and power-loss recovery still require an authorized disposable
fully isolated guest and review of host service ownership. In
particular no active NanoPi, existing VPN, host route, /proc or
service manager configuration was changed.
