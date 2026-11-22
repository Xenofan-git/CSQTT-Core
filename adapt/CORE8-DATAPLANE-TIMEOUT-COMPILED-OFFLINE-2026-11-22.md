# CSQTT Core8 — compiled Rust dataplane shutdown Result contract

Date: 2026-11-22 (Moscow project date).
Result: **PASS, five of five compiled isolated Rust worker tests**.
This verifies an exact source-extracted Rust function against mock worker
types. It does NOT execute the full Core8 binary, Linux TUN, sysctl, external
durable guard, systemd restart, VK TURN, or a real network stack.

## Why this new test was necessary

The opt-in experimental patch in commit
e9b6ef7a149df20002b1b2a3ec2341b46e37fa25 changes ONLY the
six-second bounded DataplaneRuntime::shutdown() timeout arm from
Ok(()) to Err(anyhow!(... external recovery required)).
Previous coverage included:
- 8/8 strict source-anchor offline tests;
- 7/7 static Result propagation contract tests of derived Rust source;
- successful offline ARM64 release ELF build SHA256
  a067b470fad52465e107eb0c807e57b4fdde19232f5da1c0a21d68991115cb39;
- GitHub Actions run 45005164025 SUCCESS.
However no executable unit-level Rust proof that the actual shutdown
method's branches returned the expected results with real threads
had been executed.

## New reproducible test

Added adapt/test-core8-dataplane-timeout-compiled-offline.py.
Uses standard-library-only std::thread, Instant, JoinHandle,
PhantomData, and an error-returning stub for the real handler.
Runs entirely in a temporary directory; it does not require root,
network access, a guest VM, privileged sysctls, or actual Core8 daemon.

Two supported modes:
1. With --source-file, extracts the **exact complete**
   pub fn shutdown(mut self) -> Result<()> method from the
   separately patched Rust dataplane.rs source, then embeds that
   method into minimal mock types with compatible Result and anyhow!
   interface, compiles it with native Rust, and executes the fixture.
2. Without --source-file, reconstructs the same bounded wait and
   timeout branch from the exact repository-controlled anchor strings
   and compiles/runs it. This allows GitHub Actions to test semantics
   without publishing the derived Rust sources or the ARM64 ELF.

The real-source trial used:
/srv/development/isolated-checkouts/.csqtt-core8-timeout-nonzero-build-20261122/core-250/rust-server/dataplane.rs
SHA256 f35eab6e89943c449d65d29d61b0d17d0adc7b9eedddf150852fba3a2fae334d
with locally pinned native rustc 1.97.1.

## Results

| Actual Rust worker fixture | Expected outcome | Observed |
|---|---|---|
| success | Return Ok(()) | PASS (10 ms) |
| worker-error | Propagate worker error | PASS (10 ms) |
| worker-panic | Report joined thread panic | PASS (10 ms) |
| signal-error | Propagate handler shutdown error | PASS (10 ms) |
| timeout | After bounded six seconds, return Err("external recovery required") | PASS (6009 ms) |

Real-source mode: CORE8_OFFLINE_COMPILED_SHUTDOWN_5_OF_5_PASS,
COMPILED_SOURCE=exact-actual-rust-method.

Self-contained GitHub CI mode:
CORE8_OFFLINE_COMPILED_SHUTDOWN_5_OF_5_PASS,
COMPILED_SOURCE=repository-anchor-fixture.
Bounded timeout result was approximately 6008 ms.

The compilation and both native test runs exited successfully.
Original 8/8 error-patch tests, 7/7 static error-chain tests and
6/6 bounded shutdown budget-patch tests were rerun and all passed.

## Production impact and remaining gates

- Only the experimental
  test/core8-kernel-sysctl-restore-lab-20261121 branch gains
  a new offline test and a CI step. No production release,
  Rust core implementation, NanoPi service or network route is changed.
- Original validated WIRE3 candidate remains
  236e7b01446d0f01a58b9ad2d86bee093f5e1fe0.
- Original ARM64 build-cache ELF remains SHA256
  475b142327cbfc311e46e56ce036c44c9280fcea527c310b506dac7b4bf554c9.
- The disposable QEMU guest csqtt-core8-isolated-vm-20261121
  remained OFF, disk preserved; no attempt made to rerun any previously
  blocked real guest or forced-crash tests.
- **Not proven:** real Core8 process nonzero exit when the actual dataplane
  remains hung beyond six seconds, systemd restart after that event,
  forced crash/power loss recovery, and authorized public VK Calls TURN
  through WIRE3 → Go SOCKS5 → sing-box. Do not promote experimental
  ELF until these independent acceptance gates are addressed.
