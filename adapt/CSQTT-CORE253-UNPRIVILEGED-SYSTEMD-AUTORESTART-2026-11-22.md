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

## Final verified native ARM64 user-systemd result

- Test commits:
  - 0cbcf4a04c11a0fcc81d83beb66a82103bf6c3dd: test-only actual
    non-root AArch64 user-systemd service with temporary fake /tmp sysctls.
  - 8c979e0b6e17511d7c18e60be1ba7c04275aab0d: create a missing
    runner-owned XDG_RUNTIME_DIR/systemd/user directory safely.
  - 8c3f721f47d6427729172630d2098eeb6e5bce3c: add real repeated
    SIGKILL negative test for bounded systemd StartLimitBurst.
  - 14fce394f640b81c8d0bfa4b7f6bf1b21cbd5fa5: capture systemd
    actual rate-limit counters and state.
  - 76bb19c450842170a7b51e614ddaca2419897f34: enforce actual
    bounded number of live starts and restored journal regardless
    of variant display value of Result.
- Real GitHub native ARM64 workflow **45048993025 SUCCESS**.
  Logs from job137289721979 prove:
  CSQTT_USER_SYSTEMD_REAL_AUTORESTART_AFTER_SIGKILL_PASS,
  CSQTT_USER_SYSTEMD_REAL_STOP_MIXED_AND_EXECSTOPPOST_PASS,
  CSQTT_USER_SYSTEMD_FAILCLOSED_CORRUPT_JOURNAL_PASS,
  CSQTT_USER_SYSTEMD_REAL_STARTLIMIT_BURST_3_PASS,
  CSQTT_USER_SYSTEMD_UNPRIVILEGED_FAKEPROC_LIFECYCLE_PASS.
- Native systemd property values in the preceding diagnostic run:
  Restart=on-failure, RestartUSec=1s,
  StartLimitIntervalUSec=25s, StartLimitBurst=3.
  Three consecutive synthetic child SIGKILL events were recorded.
  After the final failure: exactly 3 starts, no fourth,
  no remaining child, restored fake kernel parameters and no journal.
  systemd showed ActiveState=failed, NRestarts=3,
  Result=exit-code; checking for exact Result=start-limit-hit was
  incorrect and is NOT used to weaken the real pass criteria.
- The earlier CI45048454375 failed when an unprivileged runner-owned
  runtime unit directory did not exist; CI45048656233 and
  CI45048812019 failed due to an overly specific expected Result
  label, while observations showed bounded launches. Corrected
  test and CI45048993025 passed all steps.
- This proves actual **user-level systemd** semantics with the
  real Python guardian and a disposable synthetic Python child,
  fake regular-file sysctls and fake boot ID. It does NOT prove
  a privileged system service with actual ARM64 Rust Core8 on
  host /proc, nor a full reboot after power loss. Existing
  separate actual ARM64 Core8 supervised crash/recovery CI
  45046942359 and WIRE3 fullchain CI45039689081 remain green.
- No root privilege escalation, real sysctl writes, host routes,
  NanoPi services, Tailscale or VPN policies were performed.
  Original release candidate is unchanged.
