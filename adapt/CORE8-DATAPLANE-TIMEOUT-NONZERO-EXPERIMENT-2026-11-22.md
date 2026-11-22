# CSQTT Core8 — fail closed on a dataplane shutdown timeout

Date: November 22, 2026 (Moscow project date).
Result: experimental source patch PASS (15/15 source and propagation
contracts), real derived AArch64 release compilation PASS, but
**dynamic forced timeout / closed-loop systemd restart NOT RUN**.
This is NOT a release promotion or real VK TURN acceptance.

## New finding and rationale

The previous real isolated ARM64 guest adverse test proved that when
an injected synthetic TCP client never sends FIN, the old experimental
Rust Core8's dataplane join can exceed its one-second wait. The old
source prints [DATAPLANE] shutdown timed out, but then returns Ok(()),
which can produce a misleading Core8 exit code 0 while eight modified
global sysctls were not restored by Rust. A separate durable Python
sysctl guard did restore all eight and the kernel policy/journal.

The previous opt-in six-second bounded join window allowed Rust to
restore sysctls in ONE real ARM64 open-TCP run. The attempted second
dynamic run was blocked by safety controls, and not rerouted or
retried. That existing first-pass experiment remains separate and
does not establish fail-closed handling for persistent timeouts.

A source audit established that on a timeout:
- dataplane.rs previously returned Ok(()) after 6 seconds;
- protocol.rs has runtime.shutdown()? and propagates errors;
- main.rs records protocol_runtime.shutdown() errors as terminal_error,
  and ultimately returns Err(error) rather than Ok(()) if present;
- the opt-in private systemd sample has Restart=on-failure,
  StartLimitBurst=3 and StartLimitIntervalSec=60, but is NOT installed.
- the external durable guard returns the child's exit code after
  same-boot sysctl restoration when safe, with lock ownership checks.
Thus the newly reported timeout error can reach the service manager,
unlike silent Ok(()). No actual systemd recovery behavior was tested.

## Exact opt-in patch

New adapt/patch-core8-dataplane-timeout-nonzero.py applies ONLY to a
separate derived Rust source copy after the existing six-second
join-grace source patch. It replaces the unique source timeout arm:

  log "shutdown timed out (6s bounded grace)";
  Ok(())

with:

  log "shutdown timed out (6s bounded grace)";
  Err(anyhow!("dataplane worker did not stop within ... external recovery required"))

The match (signal_result,join_result) is unchanged, and passes Err to
the caller. No global routing, DNS, sysctl writes, firewall edits,
TUN/TPROXY code, service restart settings, or real recovery code
were changed. This is not a cure for an unjoinable worker; the
external durable guard is still required.

- Source before change: separate earlier 6s lab
  /srv/development/isolated-checkouts/.csqtt-core8-stalled-shutdown-build-20261122/core-250/rust-server/dataplane.rs
  SHA256 6d8bb67d11ce8fda223a6a819d21a0505df960a84702166894c1e5677367d1db.
- Separate new scratch source:
  /srv/development/isolated-checkouts/.csqtt-core8-timeout-nonzero-build-20261122/core-250/rust-server/dataplane.rs
  SHA256 f35eab6e89943c449d65d29d61b0d17d0adc7b9eedddf150852fba3a2fae334d.
- All previously proven AArch64 Rust sysctl RAII and TPROXY
  patches preserved byte-for-byte; no original Rust 2.5.0 source
  or validated release-candidate branch modified.

## Offline evidence

- adapt/test-core8-dataplane-timeout-nonzero-offline.py:
  8/8 PASS: unique timeout -> Err transform, idempotence, missing
  or duplicated owners/timeout arms, missing or duplicated result
  propagation. No actual /proc or network operations.
- adapt/test-core8-dataplane-timeout-error-chain-offline.py:
  7/7 PASS: existing source error propagation chain asserted;
  negative tests for fake success, missing/duplicate branch,
  missing result propagation and swallowed main/protocol errors.
  Additionally against the ACTUAL new scratch Rust
  dataplane.rs/protocol.rs/main.rs, the real source chain check PASS.
- Earlier offline guard suite 18/18, prior kernel sysctl 3/3,
  TPROXY tests 4 tests with 1 source-path-only skip, integrated
  test 2/2, mock guest 8/8, bounded-grace patch 6/6.
  After adding pinned rustc 1.97.1 directory to PATH on Moscow,
  previously skipped compiled fake-file Rust tests PASS.
  The explicitly opted-in source-file-only test was not run.
- Offline noninstalled private systemd service template
  systemd-analyze verify PASS; no new systemd unit started.
- The CI branch includes both new source-contract suites; GitHub
  Actions checks the offline code and static template, not the
  actual private ARM64 guest data plane.

## New real AArch64 ELF compilation (not run)

- Built from the separately copied source with pinned Rust 1.97.1,
  cargo --offline --locked --release,
  aarch64-unknown-linux-gnu, GNU cross linker, one build job,
  using preexisting dependency cache on Moscow VPS.
  Successful build exit0, 2m30s.
- New experimental 9,589,352-byte AArch64 ELF SHA256
  a067b470fad52465e107eb0c807e57b4fdde19232f5da1c0a21d68991115cb39.
  Saved only at
  /srv/development/isolated-checkouts/.csqtt-core8-timeout-nonzero-build-20261122/csqtt-core8-timeout-nonzero-arm64-experimental.
- Shared development Cargo target executable restored byte-exact to
  original SHA256
  475b142327cbfc311e46e56ce036c44c9280fcea527c310b506dac7b4bf554c9.
- Private dedicated ARM64 QEMU acceptance guest remains OFF,
  guest host loopback SSH127.0.0.1:22379 CLOSED, qcow2 overlay preserved;
  previous guest test-only config/credentials may still exist and
  the disk must not be shared without separately permitted cleanup.
- Other Moscow CSQTT Deploy and unrelated HydraRoute Core VMs,
  panel tunnel and host default route unchanged. No NanoPi/Amsterdam
  runtime, production policies, public UDP, DNS or Tailscale changed.

## Explicit unverified gates

1. No real open-TCP six-second timeout-to-error process run for the
   latest nonzero-exit ELF; prior repeat was blocked. Treat exit-on-
   persistent-timeout as SOURCE-VERIFIED + COMPILED, not RUNTIME PASS.
2. No true abnormal Core8/wrapper SIGKILL, recovery under power loss,
   or tested systemd restart behavior. Earlier forced test was
   blocked; no workaround attempted.
3. No separately authorized live VK Calls TURN -> WIRE3 -> Go SOCKS5
   -> sing-box fullchain on public UDP. Previous real VK Calls hash
   preflight and synthetic TURN E2E PASS are narrower results.
4. No promotion into the validated WIRE3 release candidate
   236e7b01446d0f01a58b9ad2d86bee093f5e1fe0.
