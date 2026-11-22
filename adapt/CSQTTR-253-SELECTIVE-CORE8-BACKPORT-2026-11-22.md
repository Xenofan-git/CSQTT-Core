# Selective CSQTTR v2.5.3 backport into CSQTT Core 2.5.0

Date: November 22, 2026. Scope: **server Rust scheduler and IP safety
only**, in an **isolated experiment**. Do not promote binary to a
production release without additional integration testing.

## Source provenance and baseline

- Source: Afteroid/csqttr, tag v2.5.3, Git SHA
  84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806 (GitHub tag).
- Compared against its actual v2.5.0 tag
  fd936ef087c34cc46f26111dc2361f43b8b03c07,
  AND our existing Core8-derived Rust2.5.0 source, not just release notes.
- Existing experimental Core8 source was previously patched for its own
  sysctl RAII, TPROXY, and bounded shutdown errors, located on Moscow VPS:
  /srv/development/isolated-checkouts/.csqtt-core8-timeout-nonzero-build-20261122/core-250.
- New four-file source scratch with no cloned build target:
  /srv/development/isolated-checkouts/.csqttr-253-selective-core8-src-20261122/core-250/rust-server.
  Original prior experimental source and validated release candidate
  HEAD236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 remain unchanged.
- Branch: test/csqttr-253-selective-core8-20261122, created from the
  current experiment source-test HEAD1f8d75c9e2e71f5d3c189fc4d36a8efdf597f783.
- Source transformation is opt-in, strict source-anchor, two-phase
  all-file validation. Repeated application and unknown inputs fail
  closed. It affects ONLY tun_device.rs, model.rs, web_panel.rs,
  protocol.rs. No paths, keys, deploy scripts, services, base subnets,
  interface creation, NAT, global default route or panel HTML changed.

## Changes actually missing or worse in CSQTT Core

1. **Token bucket elapsed-time bug (CSQTTR PR #4).**
   Previous Core8 route scheduler always advanced last_update even when
   a gap shorter than 0.0001 seconds added zero tokens. Multiple small
   gaps never accumulated. Only advance the timestamp when credit is
   applied; preserve original units, rate, capacity and behavior.
2. **Small UDP bulk classification (CSQTTR PR #1).**
   Our existing Core8 already had flow stickiness for packets <=300 B,
   but a high-rate QUIC-like UDP transfer with occasional small frames
   could remain pinned. Import the 2.5.3 lightweight accounting of
   bytes per 250 ms, 75% of one stream's token rate, and a 2-second
   bulk hold. Preserve existing burst scheduler, 100-worker scales
   and low-rate game UDP stickiness. Use saturating_add when counting
   window bytes so extreme artificial lengths cannot wrap usize.
3. **Participant IP import safety (CSQTTR PR #4).**
   Existing import endpoint accepted out-of-subnet and already-taken
   addresses. Add ip_in_pool and import_ip, check collisions against
   existing clients, choose next unused address where needed. Validate
   stale stored addresses during repair and deny invalid session IPs.
   Also ensure GETCONF reacquires a valid participant IP if the stored
   value became invalid.
4. **Separate discovered Core8 bug: inconsistent subnet constant.**
   Our own custom Core8 TUN_SUBNET and owner IP are 10.67.69.0/24 and
   10.67.69.2, but the downlink RouteTable SUBNET_PREFIX still pointed
   to 10.66.67.0/24. Align RouteTable to 10.67.69 and adjust its unit
   tests. This is a Core8 adapter bug, not an upstream 2.5.3 new feature.

## What was NOT copied and why

- **swg-panel / --instance / --tun-addr / --no-nat / --no-web /
  --desired:** not copied. The 2.5.3 implementation is a broad
  multi-instance host-network redesign; our gateway has distinct
  TUN 10.67.69.0/24, policies and independent Sing-box. Adopting
  it wholesale would risk collisions and global route changes.
  Requires independent narrow design/acceptance if ever needed.
- **Deploy scripts:** no copying. Original Core8 Deploy already
  carries project-specific validation of foreign UDP/TCP port
  ownership. The 2.5.3 deploy script modifies additional services,
  sysctl, interface cleanup and instance boundaries. Its read-only
  inventory and atomic upgrade behavior merit a separate comparison,
  but were not proven safer to transplant unmodified.
- **V2 UI, Android energy/SSID UX, Gooby TCP/TLS and participant
  hash UI:** not silently ported. Our confirmed six/seven-tab
  interface and Sing-box/Go routing must not be replaced.
  Those client/UI changes need separate side-by-side baseline and
  compatibility tests. No protocol WIRE3 or token acquisition change.
- Original compilation root, production NanoPi and Amsterdam live
  routes, and validated release candidate unchanged.

## Tests actually performed

- Strict source guard check against exact pinned source hashes: PASS,
  four files; derived post-patch source hashes independently checked;
  second patch application on each derived source refused.
- Actual Rust tun_device.rs isolated native unit tests: **15/15 PASS**,
  includes 100 stream scales, mixed game UDP stickiness, 4,000-packet
  bulk distribution, 50us refill microsteps and 10.67.69 subnet.
- Actual Rust model.rs imported functions extracted byte-for-byte and
  compiled with actual tun_device.rs plus inert database stand-ins:
  **6/6 new IP tests PASS**, 21/21 combined tests PASS. Covers
  free address preservation, foreign/out-of-range reject, sequential
  duplicate import, owner IP, invalid sessions, exhausted pool.
  This isolated native harness is NOT the complete Core8 process or
  a live database or VK TURN.
- Full derived Core8 source cross-compiled with pinned Rust1.97.1
  Cargo --offline --locked --release for aarch64-unknown-linux-gnu:
  PASS in 2m34s, ELF64 AArch64, SHA256
  839c3124ef94b82447a66b8c5c82f2c3d60a89c1fc8aa7bcba77abfd78b3b58d.
  Preserved in private scratch as
  csqtt-core8-253-selective-aarch64-EXPERIMENTAL, NOT DEPLOYED.
- Shared compiler cache binary restored and readback at SHA256
  475b142327cbfc311e46e56ce036c44c9280fcea527c310b506dac7b4bf554c9.
- There is NO new verified real guest TUN, live VK TURN, systemd
  or production traffic acceptance for this new binary. Previous
  blocked force/restart test was not retried.

## Original and derived source hashes

| File | Prior experimental derived | New selective derived |
|---|---|---|
| tun_device.rs | 10e99e2a8316f82afbbc6cd352e998bdf03e4f59a22fe70ba17752c8f5e8da4a | 2e68c3b0b85a868b96179b1c5a443c2763f5ed54e4c8f2a47d6d2aed068b90d2 |
| model.rs | b4afe4f8b19af1063d06d61eb7900af15475ff0ae2ada744672329ac5d12059b | 9e9b59b474f9bc94c7e90ba69c7e70a980ce8980d99c33c5d75d0b6f39f14ffa |
| web_panel.rs | 55777e0864de9f927010cd2fe7312e0d9f89b8af3c1d293ba727cd075c44ff96 | 71b0bc5fa3208d8dd21b5d0f998e973ac5f96bbb94f50f8caf4ecb7b982970ae |
| protocol.rs | a9aeee7ea83271eb619e71623cfbde9201f398e1371d36caef69d45f5d61fd1a | 351947c3f8ecc77ea77448ca95409fcdc89b45a952a2a877897d672abceff9be |

## Commands for private offline repeat

From the isolated experiment worktree, after copying a fresh pristine
source into an explicitly separate scratch destination:

1. python3 -B adapt/patch-csqttr-253-selective-core8.py --source-root PATH
2. python3 -B adapt/test-csqttr-253-selective-tun-native.py --source-root PATH --rustc /path/to/pinned/rustc
3. python3 -B adapt/test-csqttr-253-selective-import-native.py --source-root PATH --rustc /path/to/pinned/rustc
4. Cross-build pinned Cargo1.97.1 with ARM64 target in the existing
   developer cache after preserving/restoring the old cache binary.
   Never install this experimental ELF on real devices from these tests.

The separate GitHub offline workflow checks Python source/anchor
contract only. Actual Rust compilation and tests were run on Moscow
VPS against exact pinned source. Do not equate CI green with
end-to-end acceptance.
