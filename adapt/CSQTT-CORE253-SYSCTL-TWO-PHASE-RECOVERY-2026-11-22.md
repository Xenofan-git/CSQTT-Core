# CSQTT Core Linux ARM64 — safe preflight before sysctl restoration

Date: 2026-11-22. Experimental Linux-only selective v2.5.3
integration branch: test/csqtt-core-linux-unified-253-20261122.

## Root cause identified in predeployment guardian

In adapt/core8-private-durable-sysctl-guard.py, the previous
recover() mixed external-ownership validation and writes in ONE
loop, walking through the journal in reverse sorted-key order.
When a preexisting foreign sysctl change was near the end of
the scan, the guardian had already restored unrelated kernel
parameters before finally raising RECOVERY_UNRESOLVED and leaving
a pending journal. This violated the expected fail-closed behavior:
a known preexisting conflict still caused partial network changes.

Demonstrated directly: a new offline regression with deliberately
conflicting last journal key FAILED against the unmodified
one-pass implementation, showing that unrelated sysctls changed.
No actual host sysctls were modified during this reproduction.

## Narrow, non-privileged source correction

- Phase 1: read and validate ALL journal-listed parameter values.
  Valid values are either exactly the journal's saved original
  or the known Core8 target. Any missing, unreadable, foreign
  or unexpected value causes failure WITHOUT A SINGLE WRITE.
- Phase 2: only after the complete preflight succeeds, restore
  values in deterministic reverse journal-key order. Every
  parameter is read again immediately before its write so a
  late change is never silently treated as owned.
- On failure leave the original durable journal for review.
  Do not clear it, hide the conflict, or start the Core8 child.
- Original boot-scoped owner lock, fsync journal, systemd service
  example, WIRE3 Rust client and kernel route policy are unchanged.

Important limitation: Linux sysctl files are not a multi-key
atomic transaction. An I/O error or external mutation that occurs
*during phase 2 after earlier successful writes* can still leave
an incomplete restoration. The guardian retains the journal and
reports failure in this case. It does NOT claim atomicity against
concurrent uncoordinated writers; release requires ownership control.

## Tests

Fake kernel (normal files under a private /tmp test fixture):
1. preexisting external conflict in the LAST reverse journal key:
   ZERO unrelated writes, durable journal retained, and complete
   recovery once the conflict was explicitly fixed;
2. missing later parameter: ZERO writes to other parameters,
   durable journal retained, complete recovery after repair;
3. parameter changed after phase 1 and before its first phase-2
   write: refuses to overwrite, zero unrelated changes,
   journal retained, then recovers after repair.

Total **28/28** offline guardian tests PASS, guest-only systemd
contract **10/10** PASS, systemd-analyze verify PASS.
The newly added regression (1) was red against old source
and passed after the change.

Additional native GitHub ARM64 checks are triggered by the guard
source change: actual Core8 SIGKILL and durable guardian cleanup
within disposable no-default-route NET+UTS namespace, plus real
unprivileged user systemd with fake /tmp sysctls. The latter test
also checks a valid current-boot journal with an external conflict:
no partial change, no child launch or retries; after correction a
second user-systemd start must restore the baseline and launch
normally. CI outcomes must be checked before claiming PASS.

No systemd service, root network, /proc/sys, current NanoPi,
Moscow VPS network, Amsterdam VPS network or production VPN was
modified by these local tests. Existing release candidate 236e7b0
remains immutable.

## Independently confirmed GitHub ARM64 results — SUCCESS

Source commit 032a00930f78971a88b8565771f33401b2f70680.
Five isolated native/CI jobs all finished SUCCESS.

- 45051481270: genuine pinned ARM64 Rust Core8 started in a
  private no-route NET+UTS namespace with the updated Python guardian,
  real child SIGKILL, restored IPv6 forwarding, cleared journal,
  second real Core8 run and clean SIGTERM.
  Main marker:
  CSQTT_CORE253_GUARD_REAL_ARM64_SIGKILL_RECOVER_RESTART_ALL_PASS.
  All 28 fake-sysctl guardian offline tests also passed.
- 45051481303: genuine nonroot ARM64 user systemd managing a
  synthetic Python child and /tmp regular-file fake sysctls.
  Valid (well-formed) current-boot owner journal with an externally
  changed later key failed without a single partial restoration,
  without a new child or repeated unsafe restarts. After fixing the
  conflict, systemd successfully launched the child and restored the
  correct baseline after stop. Markers:
  CSQTT_USER_SYSTEMD_VALID_CONFLICT_ZERO_PARTIAL_RESTORE_PASS
  and CSQTT_USER_SYSTEMD_CONFLICT_RESOLVED_RESTART_RESTORES_PASS.
  Previous synthetic stale-boot archival checks also passed.
- 45051481273: native nonroot user systemd fake-proc guardian
  auto-restart, KillMode=mixed, ExecStopPost, corrupted journal
  fail-closed, and three-start rate-limit all PASS.
- 45051481297: guest-only systemd contract, systemd-analyze
  verify, full offline guardian tests PASS.
- 45051481226: genuine ARM64 Core8 PID 2337 remained
  uninterrupted for 165.0 wall-clock seconds with 17
  TLS/UDP/journal checkpoints. Real SIGKILL then restored IPv6
  forwarding, cleared durable journal and allowed second clean
  Core8 start/stop. Markers:
  CSQTT_CORE253_REAL_CORE8_165S_CONTINUOUS_UPTIME_PASS and
  CSQTT_CORE253_GUARD_REAL_ARM64_SIGKILL_RECOVER_RESTART_ALL_PASS.
  Note: the long-soak workflow's printed historical summary marker
  still says OFFLINE_23_TESTS_PASS, but its invoked current
  Python test file actually contains all 28 tests. The separate
  short native job 45051481270 reports the updated 28-test marker.

Limitations unchanged: nonroot user-systemd fake-proc is not an
actual root system service with the Rust server; a noncooperative
external mutation during the second phase may still result in
partial state changes and a retained journal (not global atomicity).
No real electrical reboot, privileged production service install
or live VK TURN auth has been performed.

Main 2.5.0 WIRE-3 candidate:
236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 (unchanged).
This source remains experimental; working NanoPi routing, existing
services and Moscow/Amsterdam default routes remain untouched.
