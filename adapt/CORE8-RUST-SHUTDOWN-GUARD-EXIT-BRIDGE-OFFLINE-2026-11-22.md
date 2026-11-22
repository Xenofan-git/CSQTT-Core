# CSQTT Core8 — compiled Rust shutdown to process exit to durable guard

Date: 2026-11-22 (Moscow project date)
Result: **3/3 PASS in actual subprocesses**, using a compiled Rust
fixture containing Core8's exact DataplaneRuntime::shutdown() method,
a real child process exit code, and the unmodified Python durable guard
with disposable fake sysctl files.

This is NOT a real Core8 daemon test, host kernel test, TUN,
systemd-start/restart test, forced crash or public VK TURN test.

## Why this closes a new gap

Earlier independent checks proved:
- Source patch returns Err(anyhow!(external recovery required)) instead
  of Ok(()) on a 6-second join timeout (strict source tests 8/8).
- Derived DataplaneRuntime -> ProtocolRuntime -> main error propagation
  chain is present (static chain tests 7/7).
- The compiled exact Rust shutdown method behaves correctly on five
  inert std::thread worker cases (compiled-method test 5/5).
- The original durable guard restores fake sysctls after synthetic
  Python child nonzero exit; a separate isolated systemd restart fixture
  previously proved that nonzero child exit triggers Restart=on-failure.

However the **compiled Rust method's actual nonzero process status
entering the same real durable Python guard** had not been checked in
one continuous integration test. This new fixture checks that bridge.

## Reproducible offline implementation

File: adapt/test-core8-rust-guard-exit-bridge-offline.py.

- Accepts optional --source-file and --rustc.
- Real-source mode extracts the exact full Rust shutdown method from
  the separate experimental derived Rust source; self-contained CI
  mode uses the repo-controlled checked source-anchor method.
- Rust std::thread workers exercise the same bounded 6s timeout and
  Err(external recovery required). A small stub replaces the network
  dataplane handle. No live Core8 process is used.
- Imports the original durable guard PARAMETERS allowlist, creates
  twelve fake sysctl files with deliberately different initial values
  beneath a private TemporaryDirectory, and generates a random
  fixture-only boot-id.
- Executes the original adapt/core8-private-durable-sysctl-guard.py
  with --sysctl-root set exclusively to that disposable directory.
- The Rust child exits 0 after successful shutdown() and 42 on Err.
  The real Python guard must return precisely 0 or 42 respectively,
  independently restore all twelve fake sysctls, log
  CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED and clear its journal.
- Rust compilation and process execution have bounded timeouts;
  no root, sockets, network, QEMU, systemd, process signals or actual
  host kernel files are touched.

## Moscow VPS results

Locally pinned compiler Rust 1.97.1. Exact derived Rust method from:
/srv/development/isolated-checkouts/.csqtt-core8-timeout-nonzero-build-20261122/core-250/rust-server/dataplane.rs
SHA256 f35eab6e89943c449d65d29d61b0d17d0adc7b9eedddf150852fba3a2fae334d.

Compiled executable with exact derived Rust method:
- CASE_SUCCESS=PASS CHILD_EXIT=0 GUARD_RESTORED_ALL=True JOURNAL_CLEARED=True
- CASE_WORKER_ERROR=PASS CHILD_EXIT=42 GUARD_RESTORED_ALL=True JOURNAL_CLEARED=True
- CASE_TIMEOUT=PASS CHILD_EXIT=42 GUARD_RESTORED_ALL=True JOURNAL_CLEARED=True
- CORE8_COMPILED_RUST_TO_GUARD_EXIT_BRIDGE_3_OF_3_PASS
- SOURCE_MODE=exact-derived-Rust-method

Self-contained CI-mode source anchors: same 3/3 result with
SOURCE_MODE=verified-repository-anchors.
Existing strict source 8/8 and actual-source error chain 7/7 rerun PASS.

## Production and remaining gates

- Experiment branch test/core8-kernel-sysctl-restore-lab-20261121
  gains only this offline test/report/CI entry. No derived Rust patch
  changed and no active host kernel/network/production service modified.
- Original validated WIRE3 release candidate remains HEAD
  236e7b01446d0f01a58b9ad2d86bee093f5e1fe0.
  Isolated ARM64 QEMU remains powered down with private overlay preserved.
- Still NOT proven: full live Core8 process's status when its dataplane
  truly hangs longer than 6s, systemd restart with the real Core8,
  crash/power-loss recovery, or externally authorized VK Calls TURN
  WIRE3 -> Go SOCKS5 -> sing-box full end to end.
- External durable guard remains mandatory. Do NOT promote the
  experimental ELF to production on the strength of this fixture.
