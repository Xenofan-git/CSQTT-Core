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
