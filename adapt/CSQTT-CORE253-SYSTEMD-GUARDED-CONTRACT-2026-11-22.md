# CSQTT Core Linux 2.5.0 + selective 2.5.3 — guarded systemd preparation

Date: 2026-11-22. Experimental Linux-only integration branch.

## Existing successful runtime evidence

Native ARM64 fullchain (synthetic TURN) GH45039689081 SUCCESS.
Core8 actual SIGKILL under external durable guard with same-boot
sysctl restore and second start GH45040689299 SUCCESS.
Actual unsupervised Core8 SIGKILL leak GH45039991322 FAILURE confirmed:
net.ipv6.conf.all.forwarding was left at 1, with initial value 0.

## Safe service hardening prepared now

Existing guest-only systemd example:
adapt/csqtt-core8-private-durable-guard.service.example.

- Preserves guardian Python MainPID and child Rust Core8 process.
- Uses ExecStart = durable run and ExecStopPost = durable recover
  with identical state dir, kernel sysctl path and boot ID.
- Restart=on-failure; StartLimitBurst=3 per 60 sec; RestartSec=5.
- **NEW** RestartPreventExitStatus=75. The guard emits 75 on
  untrusted journal, lock conflict, unresolved sysctl ownership,
  missing required kernel fields or permission errors. Repeated
  automatic starts on such recovery failures are unsafe.
- KillMode=control-group, 20 sec stop timeout; private secret
  EnvironmentFile, umask 0077 and NoNewPrivileges=true.
- Test-only ConditionHost=csqtt-core8-isolated and loopback-only binds.
  Actual guard private_guest_gate additionally checks root, ARM64
  and no IPv4 default route before any real /proc sysctl changes.
- The original deploy-csqtt-core-250.sh live installer is NOT
  switched to this guest-only guard. It cannot currently safely
  run on the host with a default route. Production migration
  demands separate approval, ownership design, rollback and
  real systemd integration testing.

## New offline contract checks

File: adapt/test-core8-guarded-systemd-253-contract-offline.py.
Eight Python tests verify: matching run/recover owner paths and boot-id,
guard before Core8, private host+loopback, bounded restarts and
exit75 automatic restart exclusion, credentials and restrictive units,
host route private_guest_gate protection, no silent installer change,
and no implicit unit enable/start.

Existing 21-test durable journal suite also stays intact.
No root privileges, no systemd service start, no file writes
outside the isolated source worktree.
A remote attempt to run a new privileged systemd scenario was
blocked by the available execution policy; it was not retried
by an alternate privilege route. The new CI validates static and
offline contracts ONLY. A *real* new-Core8 systemd automatic restart
after SIGKILL remains unverified in this change.

## Remaining integration gates

1. Decide explicit fail-closed production service guard mode with
   persistent owner journal, single process leader and a safe restore
   mechanism on a normal Debian/Ubuntu ARM64 host with default route.
2. Validate actual systemd Restart=on-failure after SIGKILL,
   ExecStopPost, StartLimit and failure code 75 in a disposable guest.
3. Validate interrupted update/rollback, power loss and subsequent
   boot against foreign services and host-owned sysctls.
4. Test live VK TURN credentials with unchanged original Rust client
   only in a separately authorized end-to-end environment.

No change to NanoPi, foreign VPS network or production CSQTT services.
