# CSQTT Core Linux ARM64: real user-systemd synthetic systemd restart

Date: 2026-11-22. Isolated experimental Linux integration branch.

## Why this test is separate from privileged real Core8

Prior Linux ARM64 GH run45039689081 validated the actual complete WIRE3
synthetic TURN four-component tunnel. GH run45046942359 validated
real Core8 uptime 165s, actual SIGKILL with the durable external
guardian, restored IPv6 forwarding, journal clear and normal second run.

Still missing was evidence that systemd itself, not the guardian, would
automatically relaunch the supervised process, including ExecStopPost
and fail-closed status75. Running an actual privileged system service
was restricted earlier, and this test does NOT attempt that route.

## This test

The separate branch-only native GitHub ARM64 workflow runs
adapt/test-core8-user-systemd-real-restart-fakeproc.py as a NORMAL
non-root runner user. It refuses any invocation outside GitHub-hosted
aarch64 with normal user systemd access, and does not use sudo.

It creates a unique temporary *user* systemd service under that
runner's XDG_RUNTIME_DIR. Its ExecStart invokes the real project
Python durable guardian in normal synthetic-sysctl mode, which
operates only on a private /tmp folder with regular files.
The child is an intentionally synthetic Python Core8 substitute.

Checks:
- user systemd actually launches child and guardian;
- SIGKILL child, guardian restores original fake sysctl values and
  reports failed child exit; user systemd automatically restarts
  the guardian under Restart=on-failure;
- second child has a different live PID and systemd NRestarts>=1;
- systemd stop with KillMode=mixed and guardian ExecStopPost restores
  baseline, removes journal and leaves no child;
- a deliberately invalid 0600 journal causes guardian exit75,
  no new child, and no repeated unsafe start loops;
- all files, units and processes are disposable runner-only.
  Cleanup removes transient user unit and reloads only the ephemeral
  user manager.
- no real kernel sysctl, system network, guest NIC, Tailscale, active
  NanoPi, live CSQTT or real TURN account is accessed.

If GitHub ARM64 does not expose an unprivileged user manager, the
test reports that limitation and fails without trying sudo or another
privileged service manager. This is an actual user systemd test with
a **fake Core8 and fake sysctls**, not real privileged systemd +
actual Rust Core8 integration. A separate real Core8 root systemd
guest test still remains a release gate.

## Verified native ARM64 user-manager result

- Initial CI45048454375 stopped before unit creation: the
  runner-owned /run/user/1001/systemd/user directory did not exist.
  It had not started any service or modified sysctls.
- Narrow correction 8c979e0b6e17511d7c18e60be1ba7c04275aab0d
  creates the directory only when its owner is the unprivileged
  GitHub runner user, refusing symlinks or foreign-owned paths.
- Native Ubuntu ARM64 CI45048555703 SUCCESS, real non-root user
  systemd manager:
  CSQTT_USER_SYSTEMD_REAL_AUTORESTART_AFTER_SIGKILL_PASS,
  CSQTT_USER_SYSTEMD_REAL_STOP_MIXED_AND_EXECSTOPPOST_PASS,
  CSQTT_USER_SYSTEMD_FAILCLOSED_CORRUPT_JOURNAL_PASS,
  CSQTT_USER_SYSTEMD_UNPRIVILEGED_FAKEPROC_LIFECYCLE_PASS.
  User systemd really restarted the killed fake Core8 child and
  recovered only the private fake /tmp sysctl files.
  No sudo, actual /proc write, production Core8 or network changes.

Additional negative coverage now exercises real systemd
StartLimitBurst=3 per 25sec by SIGKILL-ing three consecutive fake
Core8 children after resetting the *test-only* unit failure counter.
It requires systemd to stop restarting after three launches, no fourth launch,
full restoration of the fake kernel values and no orphan.
The follow-up CI result must be verified before claiming PASS.

## Real systemd StartLimit diagnostic — important observed difference

GitHub ARM64 run 45048812019 was intentionally left red because
the test demanded Result=start-limit-hit after three crashes. Actual
systemd user manager reported StartLimitBurst=3, interval25s,
NRestarts=3 and ActiveState=failed, but Result=exit-code, with
exactly three synthetic process PIDs and no fourth process.
This is not evidence of an unsafe restart loop. The updated
assertion requires the **actual bounded process count**, a failed
inactive unit, expected configured rate limits, and restoration
of every fake sysctl value, an empty durable journal, and no orphans.
It does NOT accept unlimited relaunches or skip journal checks.
A new GitHub run must confirm this outcome independently.
