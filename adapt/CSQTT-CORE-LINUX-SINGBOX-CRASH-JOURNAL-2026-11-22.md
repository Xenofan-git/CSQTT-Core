# CSQTT Core Debian/Ubuntu ARM64: crash-recoverable Sing-box configuration

Date: 2026-11-22. Isolated Linux branch:
test/csqtt-core-singbox-crash-recovery-20261122
Base commit: 1390794ce1fe60546ef8110a97ebbcce3932037d.
Original release candidate: 236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 (unchanged).

## Verified original problem

The existing SaveConfig already handles a synchronous rename failure,
but config.json and config.json.panel.json are two independent durable
filesystem operations. Process death or a sudden power loss after publishing
the first and before the second can leave a new config with an old panel
sidecar. The original SHA256 integrity check then refuses to load the
saved configuration, including disabled routes/SOCKS5 pool settings.

## Recovery protocol (new Linux-only code)

- Added adapt/singbox_transaction_linux.go. A mode-0600 temporary
  transaction journal records the previous raw config and previous panel
  state, whether each existed, their SHA256 hashes, and both expected new
  SHA256 hashes. It can contain passwords and must NEVER be logged.
- Journal file is fsynced, renamed to the deterministic local
  config.json.transaction.json path, and the directory fsynced BEFORE
  publishing either new config or new panel state.
- Once the new config and matching panel state are both published, the
  directory is fsynced before removing the journal and syncing again.
- On LoadConfig or at the beginning of a new SaveConfig:
  - If a complete new pair has both exact expected hashes, accept it and
    remove the interrupted journal.
  - If both old files are still intact, keep them and remove the journal.
  - If only part of the new pair was published, restore the verified old
    pair with fsynced 0600 temporary files and atomic renames, or remove
    initial new files when neither existed before.
  - If interrupted during recovery, replay idempotently while keeping the
    durable journal until recovery finishes.
- Corrupt journal, symlink, unsafe journal permissions, unexpected third-party
  file content, nonregular file, oversized file, or mismatched original
  sidecar fail CLOSED without overwriting foreign changes.
- After failed ordinary panel-state commit, previous synchronous rollback
  remains in place and the journal is cleaned after restoring the old pair.
  If rollback itself fails, the private old-config snapshot and the new
  recovery journal remain available for a later attempt.
- Original client UI, configuration format, routing tables, CSQTT Core
  protocol, TUN, no-global-default-route policy and unrelated services
  are unchanged.
- Internal Rule Set upload/delete code paths separately recover pending
  transaction before their direct config reads, without recursive m.mu
  locking. This was found and fixed during full Linux regression tests.
- Source copied into original Linux build/reconstruction workflows,
  not merely checked as an isolated test helper.

## Confirmed executions on Moscow development VPS

- Previous 6 Go tests for synchronous config/sidecar failures PASS.
- New Go crash-test functions (including subtests and a subprocess worker):
  old pair before publish, partial publish, fully committed new pair,
  first save, partial recovery, invalid journal, unsafe journal path/mode,
  external edits, SaveConfig after interruption, UploadRuleSet and
  DeleteRuleSet after interruption: PASS.
- Separate actual subprocess process-exit injection at 3 rename boundaries
  uses os.Exit to bypass cleanup. A fresh process incarnation loads the
  correct old/new configuration, preserving disabled rules and SOCKS5:
  PASS in all 3 phases.
- Full original Linux Sing-box Manager, routing, Rule Set and SOCKS5
  test suite combined with new tests PASS on Go 1.26.10 linux/amd64.
  Full combined go test -race PASS; no detected race.
- Cross-compiled a static ELF AArch64 Linux Go test and executed the
  config/crash recovery tests with qemu-aarch64 user-mode: PASS.
- Verified existing isolated Debian 13 ARM64 QEMU guest is accessible
  (aarch64; unprivileged user), but test ELF transfer/execution into
  the guest was blocked by the tool safety gate. NO guest execution or
  guest crash/power-failure test is claimed.
- Branch-scoped GitHub Actions workflow runs full Go and race tests and
  cross-compiles the ARM64 binary. Its CI result must be verified separately.

## Remaining risks / gates

- This implements a durable journal with fsync sequencing and replay for
  interrupted split writes; it does NOT prove physical power-loss safety
  for every filesystem/hardware caching behavior. VM power-off experiments
  and real disk durability validation remain to be done.
- Concurrent independent processes writing the SAME config path are not
  supported (only same-process manager synchronization is present).
- Legacy random 0600 rollback snapshots from a prior incomplete save
  may require manual inspection/cleanup; recovery does not delete files
  merely matching a filename glob.
- No production NanoPi deployment, installer, systemd restart, global
  routing alteration or release promotion happened.
