# CSQTT Core Linux ARM64 — recovering a stale boot journal under user systemd

Date: 2026-11-22; experimental Linux integration branch only.

## Existing evidence and question

- Real original CSQTT 2.5.0 Rust client + selective 2.5.3 Core8
  full synthetic TURN tunnel, TCP+UDP: GH45039689081 SUCCESS.
- Real standalone new Core8 AArch64 guarded SIGKILL, journal restore
  and 165-second uninterrupted process uptime: GH45046942359 SUCCESS.
- Real nonroot systemd user service with fake sysctls, supervisor,
  auto-restart, graceful mixed stop/ExecStopPost, refusal on corrupt
  journal and 3-start limit: GH45048993025 SUCCESS.

Remaining question: when old-boot journal remains after a power
interruption, does the real service *start* without restoring the
old boot's values? A fake-proc guard unit test already covered the
journal algorithm; a systemd lifecycle test was still missing.

## New experiment

New branch-only native ARM64 GitHub workflow:
.github/workflows/csqtt-core253-arm64-user-systemd-stale-boot.yml
with test script
adapt/test-core8-user-systemd-stale-boot-arm64.py.

Runs only on a GitHub-hosted disposable aarch64 NONROOT runner with
a user systemd manager. Unit and files are under runner-owned
/run/user/<uid>/systemd/user and a mode0700 /tmp directory.
No sudo and no real /proc/sys, kernel routes or network.

1. Write a valid private, fsynced previous-boot journal with a random
   old boot UUID, old fake sysctl baseline "9" and matching owner lock.
2. Simulate a new boot: a DIFFERENT UUID boot ID and reset all fake
   sysctl regular files to distinct "7" values.
3. Start actual user systemd service supervising a synthetic Python
   Core8 via the real durable guard. It must archive old journal,
   preserve exact contents, snapshot the new "7" baseline, and
   launch the fake child which applies target fake sysctl values.
4. Verify old journal preserved in stale-boot UUID archive (private),
   new journal bound to new UUID and new values, then systemctl --user
   stop restores the "7" values, clears only new journal, preserves
   old archive and reaps child.
5. Negative test: deliberately recreate the previous-boot pending
   journal while the old audit archive already exists. Guard MUST
   refuse with code75, never overwrite the archive, never launch a
   second child, never change new fake sysctl values or loop restarts.

This is a synthetic boot-ID change through real nonroot systemd,
NOT a real machine reboot, an electrical power-cut, or a system
service controlling actual ARM64 Core8 /proc/sys. Those remain
release gates. New workflow result must be checked independently
before marking PASS.

Production installer, working NanoPi, routes, subscriptions and
original 2.5.0 Linux release candidate stay unchanged.

## Verified native ARM64 CI result: SUCCESS

- Source commit: 03642855da6097b0b9735d15804aa2843cb73124.
- Native GitHub Actions run **45050551596 SUCCESS**,
  job 137294485249. The workflow executed under genuine nonroot
  systemd user manager on a disposable ARM64 GitHub runner.
- Log markers:
  - CSQTT_USER_SYSTEMD_REAL_STALE_BOOT_ARCHIVED_PASS
  - CSQTT_USER_SYSTEMD_NEW_BOOT_JOURNAL_BASELINE_PASS
  - CSQTT_USER_SYSTEMD_REAL_NEW_BOOT_STOP_RESTORED_PASS
  - CSQTT_USER_SYSTEMD_STALE_ARCHIVE_COLLISION_FAILCLOSED_PASS
  - CSQTT_USER_SYSTEMD_SYNTHETIC_BOOT_TRANSITION_ALL_PASS
  - CSQTT_CORE253_ARM64_NONROOT_USER_SYSTEMD_STALE_BOOT_PASS
- Verified old-boot journal bytes unchanged in a private archive;
  new journal used new boot UUID and distinct new baseline values;
  actual user systemd stop restored the new baseline and cleared
  the new journal. Reintroducing an old journal with a pre-existing
  audit archive failed closed without launching a second child,
  overwriting evidence, modifying fake sysctls, or retrying in
  an unsafe loop.
- All kernel parameters in this scenario are ORDINARY FILES under
  /tmp. It does NOT emulate a real hardware power loss, a kernel
  reboot, actual /proc/sys, or a privileged systemd-managed Rust
  Core8 process. Those integration gates remain open.
- Original release candidate, active NanoPi services and all
  real host/network/systemd state remain untouched.
