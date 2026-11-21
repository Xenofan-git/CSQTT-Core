# Experimental Core8 sysctl lifecycle remediation — 2026-11-21

Scope: **separate opt-in source patch and offline tests only**.
Branch: test/core8-kernel-sysctl-restore-lab-20261121.
The previously validated original/derived Core8 WIRE-3 ARM64 ELF, verified ZIP and
release/csqtt-250-wire3-candidate-20261121 are NOT changed.

## Verified root cause from original derived Core8 source

tokio_io.rs KernelNetworking::install sets twelve global sysctls and one
per-interface rp_filter. Its Drop only removed owned firewall rules and
did not restore any kernel parameters. Separately, proxy_route.rs
activate_tproxy changes IPv4 forwarding and rp_filter directly.
This is a functional system-state ownership defect, not a build failure.

## Opt-in corrective implementation

adapt/patch-core8-kernel-sysctl-restore.py must be deliberately invoked
with --source <derived>/rust-server/tokio_io.rs; use --check-only to
validate pinned original anchors without modifying the file.
It snapshots each regular non-symlink sysctl **before changing it**, records
only successful writes where value actually changed, and on the lifetime
end of KernelNetworking restores each recorded value in reverse order.
To limit clobbering another administrator's edits, restore is skipped if
the current value differs from the value set by Core8. Errors and skips are
logged. No /proc/sys is accessed by the Python patch or offline tests.

## Offline evidence

- Strict anchors and fail-closed reapplication tests.
- Six compiled Rust helper tests: restore, no-op, external mutation,
  disappeared entry, symlink substitution, absent entry.
- Two compiled Rust RAII Drop tests: normal owned restoration and
  externally modified value preservation.
- A separate Cargo syntax/type check for the pinned derived Core8
  source copy is appropriate; only on an isolated disposable directory.
- GitHub Actions job runs the self-contained offline test suite.

## Limitations / gates

- This experimental branch is **not production-ready**. The patch only
  covers KernelNetworking; separate proxy_route.rs TPROXY changes need
  their own state owner and tests before integration.
- SIGKILL, power loss and abrupt guest termination bypass Rust Drop.
  Host/systemd cleanup or a separate supervisor check remains necessary.
- Concurrent Core8 instances writing the same global sysctls are NOT
  safely handled by a single-instance RAII guard; instance ownership/
  coordination requires a separate design.
- A third party writing an identical intended value cannot be
  distinguished from Core8; this patch only prevents overwriting
  *different* externally changed values.
- The real private ARM64 VM test wrapper and real authorized VK TURN
  fullchain remain unexecuted; previous execution restrictions still apply.
- No NanoPi, Amsterdam services, SSH/Tailscale, host firewall/routes/DNS,
  running QEMU guest or user token was modified.

## Compiled derived Core8 source-copy check

- Original generated tokio_io.rs checked in dry-run strict-anchor mode; matched once.
- The opt-in patch was applied only to an isolated copy of derived Rust Core8
  2.5.0, with original shared Rust sources and bundled domains.txt.
- Offline Cargo check under cached Rust 1.97.1, target x86_64-unknown-linux-gnu:
  PASS, second run 6.16s; exit 0. This type-checks the complete derived Rust
  server crate with the changed KernelNetworking owner and Drop.
- First incomplete scratch copy lacked the static embedded domains.txt asset
  and gave file-not-found (not a source or type-check error); after including
  the unchanged asset, cargo check passed.
- This checks host x86_64 Rust types, not a rebuilt ARM64 production release,
  and it does not exercise real VM, kernel forwarding, or external VK TURN.

## 2026-11-21: eliminate overlapping direct TPROXY sysctl writes

The original derived Core8 proxy_route.rs activate_tproxy called sysctl -w on
four policy parameters already owned by KernelNetworking: IPv4 forwarding,
all/default rp_filter, and the csqtt-core0 TUN interface rp_filter. In
the original main startup flow the protocol runtime initializes the dataplane
and KernelNetworking before launching the local proxy monitor (main.rs).
Independent TPROXY writes therefore had no owner or rollback.

Strict opt-in adapt/patch-core8-tproxy-sysctl-ownership.py replaces ONLY these
four redundant writes with ensure_tproxy_kernel_parameters()?, which invokes
a READ-ONLY, fail-closed checker on the same four values. It does not create
a new conflicting TPROXY sysctl owner. A missing, nonregular or mismatched
parameter aborts TPROXY activation before installing its new interception
rules. Original legacy rule cleanup, failure handling, ACTIVE_RUNTIME
ownership, watchdog and normal route cleanup remain unchanged.

Proof:
- Python source-contract tests PASS; compiled Rust checker 4 fake-file
  scenarios PASS (correct, mismatch no mutation, missing, symlink).
- Compiled Rust cross-component lifecycle tests 4 scenarios PASS: normal
  KernelNetworking->TPROXY readiness->drop restoration, partial initialize,
  externally modified parameter, already desired/no new ownership.
- Derived Core8 source copy with BOTH opt-in patches: full offline Cargo
  check under Rust 1.97.1 x86_64 target PASS in 7.51 s.
- Same derived source copy with BOTH patches: full offline Cargo check
  for aarch64-unknown-linux-gnu target PASS in ~1m30s.
- Full source copies are disposable, distinct from release/CI archives.
  This is an ARM64 *type-check*, not an executed ARM64 VM test or
  byte-identical ARM64 release ELF acceptance.

Remaining limitations: KernelNetworking Drop does not run after SIGKILL
or power failure; concurrent independent Core8 processes writing the
same machine-wide sysctls lack a cross-process coordination lock;
external edits matching expected values cannot be distinguished;
real VM sysctl restore and actual VK TURN remain unverified. No public
test UDP socket was opened, and no production service was altered.

## Offline child-crash guard regression — 2026-11-21

Extended the existing *unchanged production-script* offline guest wrapper mock
suite from six to eight tests **on this experiment branch only**.
The two new cases prove the parent EXIT trap restores fake sysctls after
a synthetic child process SIGKILL and after a partial startup command failure.
This simulates loss of the Core8 child but NOT loss of the supervisor itself.
A supervisor SIGKILL/power loss needs separate persistent recovery orchestration
and is still unverified. No real process was launched except a temporary
Python child that only changed test files and killed itself.

## 2026-11-21: durable private guest supervisor — ONLY OFFLINE TESTED

Added adapt/core8-private-durable-sysctl-guard.py, an opt-in Python3
supervisor with RUN and RECOVER modes, and an example disabled systemd unit
adapt/csqtt-core8-private-durable-guard.service.example. No service is
installed, started or enabled. The original bash VM smoke wrapper and
validated ARM64 Core8 release binary are unchanged.

Safety and lifecycle details:
1. Guest real /proc mode is refused unless explicitly --private-guest,
   root, AArch64, hostname csqtt-core8-isolated and no IPv4 default route.
   Without these, offline tests must point to a disposable /tmp fixture.
2. Strict 0700 state directory, 0600 journal and flock lock, reject symlink
   and nonregular sysctl paths. Only the original Core8 global twelve
   kernel sysctls are allowlisted (the TUN-specific path is created later
   and owned/removed by KernelNetworking). No TUN, routes or firewall changes
   are made by the supervisor.
3. On fresh run the original values and exact kernel boot-id are written to
   a JSON journal using temporary-file+fsync+atomic replace+directory fsync
   BEFORE spawning child Core8.
4. Only ONE *cooperative* supervisor at a time can hold the nonblocking
   advisory exclusive lock. Child inherits the same lock FD: if Python
   wrapper is SIGKILLed while Core8 remains alive, a second cooperating
   instance fails closed instead of restoring against a still-running child.
5. On ordinary exit / child crash / same-boot recover: only values that
   still equal the expected Core8-written value are restored; changes from
   third parties are NOT overwritten, and a partially unresolved journal
   remains for diagnosis. Any changed/missing/bad journal fails closed.
6. On kernel reboot (boot-id mismatch) the old snapshot is archived but
   NEVER applied: kernel sysctl values are boot-scoped. A new run may
   then snapshot the freshly booted kernel.
7. A separately provisioned systemd service could manage the private guest
   as a cgroup, KillMode=control-group and ExecStopPost RECOVER. The unit
   included here is an inert example, not deployed or tested in systemd.
   Credentials are neither saved nor printed by the guard.

Offline evidence: 17 Python unittest cases using OWNED temp files and
synthetic child processes, covering fresh/run recovery, child SIGKILL,
parent SIGKILL with inherited lock, simultaneous run rejection, partial
launch snapshot, external change fail closed, boot mismatch, corrupt or
unsafe journal, symlink lock/journal/sysctl, directory permissions, missing
sysctl, parent SIGTERM, and real host /proc denial. No real /proc write.

Limitations: lock only coordinates participants that use this guard;
unwrapped original Core8 processes or other services can still change
sysctls. Child libraries may explicitly close inherited FDs in future.
A power outage stops the machine: no writes are possible during outage,
and stale settings must not be replayed across a boot boundary. Automatic
recovery on restart requires a separately approved, provisioned systemd
service (the unit has NOT been enabled or exercised). Fail-closed if an
external component changed a parameter. A different component writing the
same expected value cannot be distinguished. Real ARM64 VM and live VK TURN
acceptance remain UNTESTED and may require separate authorization.

## 2026-11-21: inherited-lock descendant race found and fixed (offline)

A new local fake-file regression spawned a child which passed the inherited
flock descriptor to a surviving descendant, then exited. Against the old
durable supervisor, the test **failed**: parent restored sysctls and reported
success while the descendant was still live, because it still owned the same
open-file-description lock and did not first test whether other holders
remained. This is a genuine logical race affecting long-lived descendants.

Changed the wrapper finalization order: after the direct Core8 child exits,
close the parent's original lock fd, then acquire a NEW independent lock
open-file-description before restoring. If a child/grandchild still holds the
original descriptor, the reacquire fails with CORE8_OWNER_BUSY, the durable
journal remains intact, and no sysctl is restored prematurely. After the
synthetic descendant exits, explicit same-boot RECOVER succeeds. All 18/18
durable guard unittest cases now PASS, including original scenarios.

Also strengthened inert systemd unit example with StartLimitIntervalSec=60 and
StartLimitBurst=3 to prevent an unlimited privileged restart loop. The
sample .service copy passed host systemd-analyze verify with exit code 0,
without installing or starting the unit. No active VM, current Core8 ELF,
host routes/sysctls/firewall, or release candidate were modified. The
PRIVATE guest real normal-stop PASS on the older guard commit remains valid
for that exact tested version; this new guard delta has **only offline**
tests until a later authorized guest acceptance. Forced live SIGKILL test
and real VK TURN fullchain remain outside current verified scope.
