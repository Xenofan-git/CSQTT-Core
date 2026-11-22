# CSQTT Core8 — bounded dataplane shutdown with an unclosed TCP client

Date: November 22, 2026 (Moscow project date).
Result: **first real ARM64 A/B test PASS**, NOT multi-run/production acceptance.
Scope: experimental derived Rust Core8 with existing RAII/sysctl and TPROXY
ownership patches; new opt-in 1-second to 6-second bounded thread join grace.
Live VK TURN, forced crash and power loss are NOT tested.

## Reproduced old behavior

- Already proven bidirectional local TCP/UDP TUN to Linux TPROXY to SOCKS5
  was successful when the synthetic client sent TCP FIN/ACK before shutdown.
  Without FIN an earlier diagnostic showed DATAPLANE shutdown timed out,
  and Rust failed to restore sysctls before the outer durable guard.
- Disposable Debian13 AArch64 QEMU guest csqtt-core8-isolated,
  QEMU user networking restrict=on, ONLY host 127.0.0.1:22379 SSH
  forwarded; no public UDP and no guest IPv4 default route.
- Baseline Rust-patched Core8 ARM64 ELF SHA256
  8b6d7b948881e2446218198253b3f24908629b9831b6bf0c119d1ff483c9da1c.
- Opt-in open-tcp case added to already-green real packet test.
  It injects actual IPv4 UDP into Core8-owned TUN, receives reverse UDP,
  establishes TCP with guest-local SOCKS5 CONNECT, receives echoed payload,
  and deliberately leaves the test client without FIN before API
  deactivation and Core8 normal SIGTERM. Entirely synthetic and guest-local.
- Baseline real guest run: actual UDP/TCP transfer PASS, Core8 exit0,
  log DATAPLANE shutdown timed out, but before guard all eight modified
  global sysctls remained changed: net/core/default_qdisc,
  net/ipv4/conf/all/rp_filter, net/ipv4/ip_forward,
  net/ipv4/tcp_congestion_control, net/ipv4/tcp_fastopen,
  net/ipv4/tcp_reordering, net/ipv4/tcp_slow_start_after_idle,
  net/ipv6/conf/all/forwarding.
  TUN_DATAPLANE_RUST_SYSCTL_RESTORED=False,
  OPEN_TCP_RUST_ONLY_RESTORE_STATUS=NEEDS_EXTERNAL_GUARD.
  Outer durable guard returned CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED,
  exit0; all kernel sysctls and policy back to baseline, no pending journal.
- Baseline raw stdout private Moscow scratch file:
  /srv/development/isolated-checkouts/.csqtt-core8-rustpatch-build-20261122/tun-stalled-tcp-baseline-raw.log
  SHA256 dd04e524b46f26863de643b4dc3d9d9d2f16e9bfec56557bb64e0b6c48def2d7.

## Source patch, exact isolation and binary

- Original Rust patched experimental source separately preserved under
  /srv/development/isolated-checkouts/.csqtt-core8-rustpatch-build-20261122/core-250.
  Original dataplane.rs SHA256 c1f97c31908e073f56dde10279fab83aa3f78690cbc24728c67e7e3b74784bb2.
- New source-only, strict-anchor, opt-in patch:
  adapt/patch-core8-stalled-tcp-dataplane-stop-budget.py.
  In DataplaneRuntime::shutdown only, increases the bounded join deadline
  from one second to six seconds, preserving 10ms polling, logging and
  existing design. No Rust v2.5.0 main branch or global network changes.
  The external durable guard remains mandatory.
- Six offline patch-negative tests PASS: unique replacement, idempotence
  reject, missing owner context, missing anchor, duplicate anchor, duplicate
  owner context. Test file:
  adapt/test-core8-stalled-tcp-dataplane-stop-budget-offline.py.
- New isolated two-megabyte source copy:
  /srv/development/isolated-checkouts/.csqtt-core8-stalled-shutdown-build-20261122/core-250,
  patched dataplane.rs SHA256
  6d8bb67d11ce8fda223a6a819d21a0505df960a84702166894c1e5677367d1db.
  Other RAII and TPROXY source patches unchanged.
- Offline --locked --release ARM64 GNU cross-compile with Rust1.97.1,
  jobs1 succeeded in 2m19s, exit0. New separate AArch64 PIE ELF size
  9,589,352 bytes SHA256
  b3afb2faa4340d6a47202b6fbb5947ec21c1b677c22f812d56e0a38c24179084.
  Preserved only in the experiment scratch build directory, not deployed.
  Previous cached ARM64 output restored to its prebuild SHA256
  475b142327cbfc311e46e56ce036c44c9280fcea527c310b506dac7b4bf554c9.

## First real new-ELF ARM64 result: PASS with important limits

- The same protected guest verified new ELF SHA256 and CSQTT-WIRE-3;
  no existing sysctl journal, TPROXY rules or IPv4 default route.
- Actual open-tcp case with the new compiled binary again exercised
  real UDP through TUN and SOCKS5, reverse UDP, TCP SYN/SYNACK, SOCKS5
  CONNECT, echoed TCP payload and deliberately no FIN.
- After authenticated API deactivation and normal Core8 exit0,
  BEFORE outer guard:
  TUN_DATAPLANE_RUST_SYSCTL_RESTORED=True
  TUN_DATAPLANE_RUST_SYSCTL_DIFF={}
  TUN_DATAPLANE_OPEN_TCP_RUST_ONLY_RESTORE_STATUS=RESTORED
  and policy rules back to baseline.
- Outer guard returned RESTORED_AND_VERIFIED with exit0, clean sysctl
  baseline, policy and pending journal. Core8 app log ended Stopping
  server with NO DATAPLANE shutdown timed out.
- First new-ELF run stdout privately saved on Moscow VPS:
  /srv/development/isolated-checkouts/.csqtt-core8-stalled-shutdown-build-20261122/stalled-tcp-6sec-first-pass.log
  SHA256 b2ea525777741f4302c0246d48f4878b846ae936b9a77a65575c4cd679d7f01c.
- **IMPORTANT**: an attempt to start a second independent ARM64 run
  was BLOCKED by OpenAI tool safety controls before execution.
  No alternate port, split command or other channel was attempted.
  Therefore this is ONE successful real ARM64 experimental run only,
  not stress-tested / repeat-verified. This is a mitigation with a
  finite wait, not proof of restoration after hangs, crashes or power loss.

## State and remaining gates

- After safe readback, guest was gracefully powered off:
  QEMU console Power down, qemu.pid absent, SSH localhost22379 closed,
  guest qcow2 overlay preserved. Other working Moscow CSQTT Deploy and
  HydraRoute Core ARM64 VMs, panel tunnel and host routes unchanged.
- Because guest-only cleanup was part of the blocked second-test call,
  test config and ephemeral credentials may remain on the preserved
  private guest overlay. Do NOT claim those guest test files were deleted;
  the disk is private, must not be shared, and requires separately
  permitted exact-path cleanup before further reuse.
- Original validated WIRE3 release candidate HEAD
  236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 unchanged.
  No experimental ELF promoted or installed on NanoPi.
- Remaining: repeat actual ARM64 open/normal-TCP cases if permitted;
  real forced Core8 or guard SIGKILL and power-loss recovery not tested
  (earlier attempt blocked); real authorized public VK Calls TURN to
  WIRE3 to Go SOCKS5 to sing-box remains NOT VERIFIED.
