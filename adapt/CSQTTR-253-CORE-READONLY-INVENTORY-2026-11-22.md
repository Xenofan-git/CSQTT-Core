# CSQTTR 2.5.3 selective read-only inventory for CSQTT Core 2.5.0

Date: 2026-11-22. Experiment branch only. No production changes.

## Verified difference from upstream 2.5.3

Afteroid/csqttr tag v2.5.3 (84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806)
adds a read-only deploy.sh inventory command. Our dedicated Core 2.5.0
installer offered only human-readable status. Ported only the read-only
inspection contract, not the upstream swg-panel/multi-instance installer.

## New command and limitations

- Invoked as: bash deploy-csqtt-core-250.sh inventory, as root to prevent
  misleading PID-absence from ss -p.
- Does not open logs, mkdir, install packages, stop services, modify /proc,
  load TUN, change firewall, alter default route or read secrets.
- CSQTT_INV machine-readable lines report the dedicated Core systemd
  unit state, optional named Docker instance, Core TUN interface presence,
  SQLite database EXISTS (never contents), UDP peer and TCP web ports.
- Occupancy classifications: core_candidate, foreign, mixed, unknown,
  none. Core process matches are CANDIDATES, not proof of service ownership;
  TUN and Docker ownership explicitly unverified.
- Missing required tooling or socket inquiry error returns error and
  nonzero status with NO CSQTT_INV end marker. A failed ss query is
  never misreported as an unoccupied port.
- Does not enumerate unrelated CSQTT installations, perform server
  adoption or integrate into Desktop/Android deploy planning.
  These remain separate possible follow-on features requiring review.

## Reproducible generation, prior rollback protection

- Original CSQTT 2.5.0 upstream deploy.sh pinned SHA256:
  9a82d4ab5f12b0677b4e11be14ca892fb16c0a771eddb7ed5485d8957e9b9895.
- Strict source patches apply existing 2.5.3-derived Core port
  ownership protection and new read-only inventory.
- Audit discovered original generator was missing about 264 lines
  of ALREADY VERIFIED Core rollback and restored-panel checking
  code present in tracked installer. It must not silently overwrite
  those improvements.
- Stored exact delta in csqtt-core-existing-rollback-preservation.patch.
  Generator replays this in a private temporary directory with zero
  fuzz and expected SHA-256 before AND after. It writes final installer
  only after matching target digest. Full re-generation in disposable
  /tmp from the pinned original source was byte-for-byte IDENTICAL
  to currently tracked experimental installer.
- SHA256 before preexisting rollback overlay:
  8a3b1878dc65549937f90190ee459d22ce2c2296c52b8db7dfac8f42b2804605.
- SHA256 finished experimental installer:
  18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68.
- Existing Core source/rollback and release candidate HEAD
  236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 unchanged.

## Verifications

- 21/21 isolated Bash mock inventory cases PASS including
  owned/foreign/mixed/unknown/free UDP/TCP ports, invalid query
  errors, systemd lifecycle, TUN, Docker, DB presence, early dispatch.
- Existing Core port-safety scenarios 16/16 PASS.
- Core install isolation/static test PASS; release ELF hash guard PASS.
- bash -n PASS; actual generator source replay cmp PASS.
- No actual Deploy, SSH change, host network mutation, full-server
  systemd lifecycle, guest TUN or VK Calls TURN test in this step.
- This is NOT approval for a production rollout.
