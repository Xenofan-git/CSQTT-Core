# CSQTT Core8 — actual ARM64 Rust sysctl RAII patch acceptance

Date: 2026-11-22 (Moscow project date).
Result: PASS — patched derived Rust 2.5.0 ELF on real Debian13 ARM64
restores all changed global kernel sysctls on NORMAL shutdown,
BEFORE the external durable guard runs recovery.

This is not a forced SIGKILL/cold-reboot recovery test, not a live TPROXY
route acceptance, and not a real VK Calls TURN end-to-end test.

## Sources and reproducible isolated build

- Original derived CSQTT Core8 2.5.0 source:
  /srv/development/isolated-checkouts/csqtt-arm64-build/core-250/rust-server
- Unmodified source SHA256:
  tokio_io.rs b18532ef518443c4abd186f0e9499c07b7b13906c6d6b9138e5fea65b7fe11d5
  proxy_route.rs a16e51263581cf6a703e0c6f30cee97d4cf1231f395d5af6587b93e433f902df
- The experimental 2 MB source copy:
  /srv/development/isolated-checkouts/.csqtt-core8-rustpatch-build-20261122/core-250/
  includes rust-server, shared/ and app/src/main/assets/domains.txt, but
  did not copy the 4.4GB original target cache.
- Applied only the existing opt-in source patches:
  adapt/patch-core8-kernel-sysctl-restore.py against scratch tokio_io.rs
  adapt/patch-core8-tproxy-sysctl-ownership.py against scratch proxy_route.rs
- Patched source SHA256:
  tokio_io.rs a7f936c7f027be55c729ccbb138903bfc68c69cb4060f7297424e461c85041fc
  proxy_route.rs e0d7efd5545cdc250d6d5f9c98fbc33f211a61913d78f479e632b17439d09ad9
- Offline build with local pinned Rust 1.97.1, Cargo --locked --offline
  --release --target aarch64-unknown-linux-gnu --jobs 1, CARGO_NET_OFFLINE=true,
  aarch64-linux-gnu-gcc and original cached Cargo registry. Completed
  release compilation in 8m13s with exit code 0.
- Resulting ELF: 64-bit AArch64 PIE, dynamically linked, 9,589,352 bytes,
  SHA256 8b6d7b948881e2446218198253b3f24908629b9831b6bf0c119d1ff483c9da1c.
  Saved ONLY as a separate experimental file:
  /srv/development/isolated-checkouts/.csqtt-core8-rustpatch-build-20261122/csqtt-core8-aarch64-rustpatch-experimental
- Prior cached, non-production ARM64 build ELF was SHA256
  475b142327cbfc311e46e56ce036c44c9280fcea527c310b506dac7b4bf554c9.
  Snapshot saved before compilation; that cached output was restored
  by atomic rename after extracting the patched ELF, verified same SHA256.
  This preserves the prior cached builder binary, although cache metadata
  may now contain newly generated dependencies.
- Main verified WIRE3 release candidate HEAD236e7b0 remained untouched.
  Its original guest Core8 ELF SHA256 remains
  a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706.

## Real independent private ARM64 VM acceptance

- Existing private QEMU Debian13 guest csqtt-250-core8-isolated-vm-20261121
  (hostname csqtt-core8-isolated; uname aarch64), network -netdev user,restrict=on.
  ONLY host loopback 127.0.0.1:22379 forwarded to guest SSH22, NO public UDP.
  Guest had no IPv4 default route. Working Deploy and HRCore VMs not changed.
- Staged patched ELF into guest /home/csqttlab/core8-rustpatch-experimental-aarch64.
  SHA256 matched host value exactly. CLI --protocol-revision printed CSQTT-WIRE-3.
  Guest dependencies available, original Core8 guest binary unchanged.
- Also copied guest-only manual test
  adapt/test-core8-private-arm64-rustpatch-raii-acceptance.py.
  Guest durable guard SHA256
  fe1af8822a752bd5e9edeaaac2efe2c7b94fdf544b55696253d94a28631ef449
  (previously tested latest wrapper revision).
- Test was run as root inside guest with explicit guest hostname/arch checks,
  exact binary SHA, NO IPv4 default route, and no pending recovery journal.
  Outer durable guard snapshots original 12 global sysctls and starts the
  inner Python test. Inner test starts the PATCHED binary directly with
  randomly generated ephemeral in-memory passwords on UDP127.0.0.1:46877
  and guest-local web TCP46878 (neither forwarded). Inner script verifies
  actual guest sysctl values, cleanly sends SIGTERM, waits for Core8 exit,
  and reads ALL sysctl values BEFORE returning to the outer guard.
- Actual observed runtime:
  PATCHED_REAL_CORE8_UDP_WEB_BOUND=True
  PATCHED_REAL_CORE8_SYSCTL_CHANGED_COUNT=8
  PATCHED_REAL_CORE8_IP_FORWARD_DURING=1
  PATCHED_CORE8_CLEAN_EXIT=0
  RUST_RAII_BEFORE_GUARD_RESTORED_ALL=True
  RUST_RAII_BEFORE_GUARD_MISMATCH_COUNT=0
- Eight changed global parameters:
  net/ipv4/ip_forward
  net/ipv6/conf/all/forwarding
  net/ipv4/conf/all/rp_filter
  net/core/default_qdisc
  net/ipv4/tcp_congestion_control
  net/ipv4/tcp_reordering
  net/ipv4/tcp_fastopen
  net/ipv4/tcp_slow_start_after_idle
- External guard only ran after the independently verified Rust post-exit
  readback. It then reported:
  CORE8_RECOVERY=NO_PENDING_JOURNAL
  CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED
  PATCHED_ARM64_GUARD_EXIT=0
  PATCHED_ARM64_AFTER_GUARD_RESTORED_ALL=True
- Separate read-only guest inspection after whole test:
  /proc/sys/net/ipv4/ip_forward=0,
  csqtt-core0 TUN interface absent,
  guest ports46877/46878 closed,
  original guest test ports46871/46872 also closed,
  pending durable journal absent,
  no guest IPv4 default route,
  original pinned Core8 ELF unchanged,
  Rust patched Core8 log showed normal Stopping server with no errors.

## Cleanup and preservation

- Removed only guest-owned test output directory, including generated
  credentials/config, and copied patched experiment/test script from
  guest home. Test ELF remains separately preserved on Moscow VPS for
  additional lab work; it is not installed in production or shipped.
- Private guest shut down gracefully from inside. QEMU console Powering off/
  Power down, QEMU PID absent, local SSH 127.0.0.1:22379 closed,
  private qcow2 disk preserved. Other CSQTT Deploy and HydraRoute Core QEMU
  guests still run, tunnel active, Moscow VPS default route unchanged.
- Original Rust source files, cached preexisting build output, original
  verified release candidate, and NanoPi production runtime unchanged.

## What this DOES NOT establish

- The TPROXY ownership patch was compiled into this ELF but no live
  TPROXY configuration/path was activated; source-level readiness and
  offline fake-file TPROXY tests already passed separately.
- No real guest forced wrapper/core8 SIGKILL recovery test; previously
  blocked scenario not reattempted. Power-loss recovery not tested.
- No real non-cooperative parallel Core8 owners/descendant lock during a
  true Core8 ARM64 runtime; synthetic tests only.
- No real authorized publicly reachable VK TURN -> WIRE3 -> Go SOCKS5 ->
  sing-box fullchain; no external UDP test stand was provisioned.
- No promotion of experimental Rust ELF into main release candidate.
