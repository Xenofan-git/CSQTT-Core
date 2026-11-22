# CSQTT Core 2.5.0 + selective 2.5.3: actual ARM64 SIGKILL sysctl leak

2026-11-22. Linux-only integration branch
test/csqtt-core-linux-unified-253-20261122.

## Verified safety defect, not a production incident

Native Linux ARM64 GitHub runner test in a disposable isolated network
namespace without a default route: GH run 45039991322 FAILED as desired.
Actual Core8 server bound UDP 47000 and HTTPS 47002, then its process
received SIGKILL. Kernel listeners were released and a new Core8
instance could start; after the following clean stop,
net.ipv6.conf.all.forwarding stayed at 1 instead of baseline 0.
No running NanoPi or production host was touched.

Source cause: Core8 Rust
rust-server/tokio_io.rs KernelNetworking::install() modifies 12
sysctls and keeps saved values *only in process memory*.
impl Drop restores those on orderly shutdown, but SIGKILL prevents
Drop. A new process sees the retained target value 1 as the old value
and does not assume ownership. This is a real crash-safety gap and a
separate durable supervisor is REQUIRED before live deployment.

## Existing external durable supervisor

Previously authored
adapt/core8-private-durable-sysctl-guard.py: 0600 fsynced
boot-scoped journal, cooperative nonblocking flock ownership,
root AArch64 isolated-host checks, restoration of owned values only,
stale-boot protection, outside modification refusal.
Runs Core8 as a supervised child and verifies all recorded sysctls
after child exit, including abrupt SIGKILL.

First attempt of actual supervised Core8 in GitHub run 45040370672
FAILED CLOSED because Linux guest kernel did not expose the optional
/proc/sys/net/core/default_qdisc. Original Core8 itself skips missing
kernel knobs; guard originally required all 12 and refused to start.
It never ran Core8 or modified kernel parameters in this failed case.

## Narrow change to optional kernel values

The durable guard now allows absent *optional* kernel sysctl files on
real /proc, matching the existing Rust Core8's own setter behavior.
A new constant REQUIRED_PARAMETERS always requires IPv4 forwarding,
IPv6 forwarding, and both all/default rp_filter sysctls.
Journal validation requires at least this mandatory set and rejects
unexpected keys, unsafe metadata, symlinks, invalid values, unresolved
ownership and external edits. Synthetic fake-proc mode continues to
require every knob unless a regression case opts in explicitly.

Existing offline fake-proc guard regression suite + 3 new negative
tests PASS (21 total):
- optional absent knob retained as missing and no attempt to write it;
- missing required IPv6 forwarding is refused;
- journal tampered to omit required IPv6 forwarding is refused.
The previous orphan-child flock, SIGTERM/SIGKILL, symlink and
world-readable permission tests remain green.

New CI for actual Core8 ARM64 child SIGKILL + durable supervisor
must be completed and independently verified; this report is not
a production or power-loss acceptance. Real machine reboot and
production systemd service wrapping remain separate gates.
No changes to the Rust 2.5.0 wire protocol, routing, host default
route, NanoPi, Moscow VPS or Amsterdam VPS runtime configuration.
