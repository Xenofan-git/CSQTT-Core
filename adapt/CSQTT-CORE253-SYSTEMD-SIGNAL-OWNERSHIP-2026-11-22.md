# CSQTT Core Linux ARM64 — supervised systemd stop ownership

2026-11-22. Experimental guest-only unit, not production.

## A service-manager race in the existing guest-only template

A Linux Core8 process under the Python durable sysctl supervisor
needs the guardian process to coordinate shutdown and restore the
original kernel values. The prior sample unit used:

    KillMode=control-group

With this setting systemd delivers its initial SIGTERM to both
the supervising main process and the child Rust Core8 process.
The guardian also forwards SIGTERM to the Core8 child, resulting
in overlapping termination paths instead of a single coordinator.

## Minimal adjustment

The guest-only unit now declares:

    KillMode=mixed

According to systemd service semantics, it initially sends the
termination signal to the service's main PID, the Python guardian.
The guardian forwards it to the separate Core8 process group, waits
for Core8, then restores saved sysctls. If shutdown hangs and
TimeoutStopSec=20 is reached, systemd's final SIGKILL escalation
covers remaining service members.

No changes to Rust, original 2.5.0 client, Go, route, actual systemd,
on-host daemon, installer or default VPN policy.

Retained in guest-only template:
- Restart=on-failure, RestartSec=5;
- StartLimitIntervalSec=60, StartLimitBurst=3;
- RestartPreventExitStatus=75 for journal/recovery/ownership failure;
- ExecStopPost invokes guarded recovery with the same boot ID, state
  directory and sysctl root;
- mandatory isolated guest ConditionHost, loopback-only Core8 binds,
  private credentials and permissions;
- NoNewPrivileges=true and bounded TimeoutStopSec=20.

## Verification

- adapt/test-core8-guarded-systemd-253-contract-offline.py:
  10/10 tests PASS, including guardian-first signal contract and
  negative regression for the wrong KillMode=control-group.
- adapt/test-core8-private-durable-sysctl-guard-offline.py:
  25/25 kernel journal, signal forwarding, lock, crash recovery tests
  PASS (fake filesystem and test subprocesses only).
- systemd-analyze verify of a temporary copy of unit PASS;
  no systemctl start, daemon-reload, installation or sysctl change.
- Branch-only GitHub Actions offline unit workflow independently
  repeats checks; verify actual run result separately.

These checks establish systemd configuration contract, not real
systemd crash-restart acceptance. No privileged service test or
change to active NanoPi services was performed.
Power-loss/reboot and real VK TURN remain pending.
