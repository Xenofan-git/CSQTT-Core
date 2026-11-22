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
