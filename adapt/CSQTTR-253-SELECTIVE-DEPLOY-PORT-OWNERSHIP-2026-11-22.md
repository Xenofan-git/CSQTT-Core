# CSQTT Core: selective CSQTTR 2.5.3 Deploy port-ownership protection

Date: 2026-11-22; project: CSQTT Core based on original 2.5.0.

## Selection rationale and original evidence

Upstream Afteroid/csqttr v2.5.3 tag
84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806
contains ownership-aware install preflight. Comparing actual
app/src/main/assets/deploy.sh against our tracked standalone
server-assets/deploy-csqtt-core-250.sh found a strictly narrower
but serious missing protection: our current script could reach
force_release_peer_port() on any PID returned by ss on the configured
UDP port, regardless of ownership; TCP web-port conflict checks were
inadequate. This is a meaningful safety regression compared with 2.5.3.

The original derived 2.5.0 installer file was SHA256
efebd2eb6e9374004136eab5f356515d3f762fb829ef0ebf88a8e5c62b23cb44.

## Minimum safe transplant

- New explicit, READ-ONLY foreign/unknown-owner check on UDP peer and
  TCP web listeners, executed before detect_os, apt, sysctl preflight,
  network mutation and service cutover. Absent ss fails closed.
- Existing Core owner on the same port is allowed during preflight.
  Foreign PID, unknown PID and mixed-owner listeners are refused.
- Revalidate owner during UDP cleanup and again inside force_release_peer_port
  before its pre-existing process-stop loop. The original mechanism
  for stopping *owned* Core runtime is unchanged.
- Refuse continuing after cutover if TCP web port remains occupied.
- No changes to ports 47000/47002, 10.67.69.0/24 TUN, NAT, DNS,
  global default route, systemd, Docker logic or saved user settings.
- Existing Core immutable-ELF SHA256 guard and rollback mechanics kept.
- The originally pinned 2.5.0 source generator
  adapt/derive-csqtt-core-deploy-250.py applies the same strict
  selective patch at the end, preventing accidental removal on regeneration.

Files changed in experiment-only branch:
server-assets/deploy-csqtt-core-250.sh,
adapt/derive-csqtt-core-deploy-250.py,
adapt/patch-csqttr-253-selective-deploy-ports.py,
adapt/test-csqttr-253-selective-deploy-ports-offline.py,
workflow and this report.

## Actual tests

- Exact multi-anchor patch applied only to derived Core installer.
  An attempt to patch again is explicitly rejected.
- 16 / 16 standalone Bash-executed fake-listener scenarios PASS:
  empty, owned, unrelated and unknown UDP/TCP, mixed owners,
  cleanup and post-cleanup checks, including direct stop-loop
  rejection before process-mutation calls.
- bash -n on real patched installer: PASS.
- Existing Core isolation static suite: PASS.
- Existing release-ELF hash validation static suite: PASS.
- Generated a disposable installer in a temporary directory from
  original v2.5.0 pinned deploy.sh SHA256
  9a82d4ab5f12b0677b4e11be14ca892fb16c0a771eddb7ed5485d8957e9b9895:
  generator PASS, reproduced ownership guard and correct ordering;
  temporary files discarded.
- Experimentally patched installer SHA256
  9e4e17174d6ba21c715613be82d014f39e5321352a88b7f58aa391e99d1e9e3b.

## Explicit non-claims and limitations

No actual Deploy install/update/uninstall invoked; no process, firewall,
server unit, live port, QEMU guest, production NanoPi, VPS service or
global sysctl was changed. Port preflight and later cleanup cannot
be fully atomic: races can still occur, and no full TUN-interface
ownership inventory is implemented. The existing process identity
heuristic was not rewritten. A foreign TUN owner or other legacy
instance must remain an independent release gate.

Separate experiment branch
test/csqttr-253-selective-deploy-20261122
starts at release-candidate 236e7b01446d0f01a58b9ad2d86bee093f5e1fe0.
Original release candidate and completed Core8 backport branch
test/csqttr-253-selective-core8-20261122 remain unchanged.
No new installer is approved for production.
