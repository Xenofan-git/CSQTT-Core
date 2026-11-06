# CSQTT authenticated liveness protocol — design gate

Status (2026-11-06): negotiated WIRE-4 liveness is now applied by the release workflow to the pinned production-source ARM64 client/server build and is included in the packaged ARM64 runtime. The production client marker is audited, and same-process recovery is exercised in the Debian 13 ARM64 system VM using the production server plus a CI-only instrumented WIRE-4 client. **Not yet hardware-validated or deployed to NanoPi.**

## Verified isolated CI evidence (2026-10-15; commit `bac67d662877b49a5e4a8b7aa007eb38e7575cde`)

The disposable pinned Rust client/server clones receive a test-only negotiated-liveness patch from `scripts/ci/patch-csqtt-negotiated-liveness.py`. This is evidence about the fixture, not an upstream-reviewed patch or the shipped runtime client.

- [Real Rust-client recovery E2E, run #235](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38801699589): both `process-recovery (disabled)` and `process-recovery (enabled)` matrix jobs passed. In the no-watchdog leg, the same Rust client PID survived the isolated server restart, parsed fresh TUNCONF, re-registered all nine workers, and restored bidirectional IPv4 through the real client UDP dispatcher and Linux TUN. Both three-packet ping probes reported 0% packet loss.
- The no-watchdog leg exercises the test-only negotiated authenticated PING/PONG exchange; it does **not** mean the untouched stock receive loop independently recovers. The disposable pinned sources are patched for this experiment.
- The separate experimental AArch64 client/server artifacts were checked for architecture, provenance and the `CSQTT_LIVENESS_V1` marker, then run in a full Debian ARM64 system VM. The quiet tunnel survived multiple PING/PONG periods; after server restart the same client process restored TUN traffic. The pre-restart probe received 3/3 packets and the post-restart probe received 5/5 (0% loss). See [run #235, ARM64 VM job](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38801699589).
- [Pinned original LaLune ARM64 build and deployment E2E, run #700](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38801700145) passed separately, including the real packaged artifact's ARM64 VM lifecycle and Deploy API install/uninstall checks.
- The upstream unmodified baseline does not contain `CSQTT_LIVENESS_V1`; the release pipeline now builds a separately provenance-tracked patched production artifact. The CI-only test client is separate and uses disposable TURN credentials. Real VK authentication and end-to-end recovery for Manual, Auto API and Auto VK hash-acquisition modes have not been demonstrated by the recovery E2E.

**Historical interpretation (2026-10-15):** same-process recovery was initially demonstrated only in disposable patched sources. The release workflow has since promoted the negotiated WIRE-4 patch into its production-source build pipeline, as documented in the current-status section below. Hardware validation and real VK-auth mode coverage remain open.

## Current release-build status (2026-11-06)

The current workflow `.github/workflows/build-original-linux-arm64.yml` no longer treats negotiated liveness as a disposable fixture only:

- It pins `Endlad2/csqtt-core` commit `3eafb7ee868f5f2690edf3b0326153813f88eb5f` and CSQTT server tag `v2.1.9`.
- It applies `scripts/ci/patch-csqtt-negotiated-liveness.py` to the matching client/server source trees before building the production ARM64 client and server.
- It verifies `CSQTT_LIVENESS_V1` and `CSQTT-WIRE-4` markers and checks that CI-only TURN credentials/counters are absent from the production client artifact.
- The separate test-only ARM64 client is instrumented after the production client has been built. The system-VM test uses that disposable client with the production server binary to test same-process recovery; it does not inject test credentials into the production artifact.
- The actual packaged ARM64 runtime is separately accepted in a clean Debian 13 ARM64 guest. CI run #1297 passed both jobs, including `Exercise same-process recovery with the ARM64 WIRE-4 client and production server`.

**Limits:** the system-VM recovery E2E is strong integration evidence, but it is not a real NanoPi hardware run and does not prove live VK authentication for Manual, Auto API, and Auto VK modes. Do not deploy automatically or change production network configuration based on CI alone.

## What the pinned sources establish

- The client receive loop awaits `TurnReceiver::recv()` without a session-level inactivity deadline.
- Server `SESSION_LEASE` is one-way today: it refreshes inbound activity and is then ignored. The pinned client has no matching lease sender/ack handler.
- Legacy all-`0xff` keepalives are ignored by the server without a response.
- `STREAM_ALIVE` belongs to the server's stream-repair round; it is not a periodic general session heartbeat.
- A blind receive-idle timeout can disconnect a healthy tunnel whose inbound traffic is quiet.

## Proposed wire behavior (requires review before implementation)

Use a new, versioned control message family; do not overload legacy `SESSION_LEASE` or `STREAM_ALIVE`.

1. **Capability negotiation.** The client advertises a liveness capability in an explicitly versioned registration extension. The server confirms the exact version in its authenticated configuration response. Absence of the extension/confirmation means legacy mode: do not start heartbeat deadlines and do not infer peer failure from idle receive.
2. **Challenge/response.** Once negotiated, the client sends an authenticated `PING` with a cryptographically random nonce and monotonic sequence. The server validates the negotiated session identity and returns an authenticated `PONG` echoing the nonce and sequence. Use the existing authenticated packet envelope; never accept an unwrapped control message as proof of life.
3. **Bounded state.** Permit at most one outstanding challenge per worker/session. Accept only the exact outstanding nonce/sequence; ignore duplicates, stale replies and unsolicited replies. Rotate nonce for each challenge. Do not let an old response satisfy a newer challenge.
4. **Timeout policy.** A missed response marks the session suspect, not immediately dead. Use a bounded number of consecutive misses and a grace period informed by the selected TURN transport. On confirmed failure, return a typed liveness error through the existing reader/session teardown path so the existing worker supervisor owns retry and cleanup. Do not spawn replacement workers from the heartbeat task.
5. **Idle correctness.** Schedule probes independently of TUN payload traffic. A healthy quiet tunnel must remain connected when each valid PONG arrives. Normal user traffic may count as liveness only if the protocol explicitly defines it and safely correlates it to the current session; otherwise keep the control exchange explicit.
6. **Compatibility.** New client + old server: negotiation fails closed to legacy behavior (no heartbeat timeout). Old client + new server: server ignores unknown versioned controls safely. Only after negotiation succeeds may either side rely on liveness messages. Never infer support solely from a version string not covered by the wire-protocol compatibility rules.

## Security and implementation questions to settle

- Define exact binary encoding and maximum lengths; reject malformed lengths, unknown mandatory flags and integer overflow.
- Ensure PING/PONG are protected by the same inbound authentication and replay window as other packets. Add a per-session sequence/nonce check to prevent replay even inside the accepted replay window.
- Decide whether probe state belongs to each TURN allocation, each worker, or the shared device session. The current recovery evidence is per worker, so the initial implementation should be per worker unless code review proves another scope correct.
- Ensure pending timers and sends are cancellation-aware; session teardown must cancel the liveness task and not leak a worker or TURN allocation.
- Consider UDP loss, reordering, delayed packets, server overload, TURN refresh behavior and clock jumps. Use monotonic time only for deadlines.
- Keep `vk_hash_mode` and `vk_auth_mode` separate. This E2E uses a disposable TURN fixture and does not prove VK auth compatibility.

## Required test matrix before enabling

1. Negotiated client/server, quiet tunnel for several probe periods: remains connected.
2. Valid PONG with exact nonce/sequence: resets miss count.
3. Missing PONG: only fails after the documented bounded miss policy.
4. Wrong nonce, stale sequence, duplicate PONG, malformed PONG and unauthenticated PONG: none can satisfy a challenge.
5. Delayed/reordered PONG and packet loss: no premature reconnect; eventual recovery after real server restart.
6. Server restart while traffic is active and while tunnel is idle: same client process re-registers all nine workers and restores bidirectional TUN traffic.
7. New client with old server and old client with new server: no heartbeat-induced reconnect loop.
8. Cancellation during pending probe: all tasks/allocations are cleaned up; no duplicate worker registrations.
9. Repeat E2E with stock (unmodified) receive loop and with liveness feature explicitly negotiated. The current test-only idle watchdog must not count as proof of this protocol.
10. Verify packaged artifact provenance before claiming the deployed ARM64 binary includes the fix.

## Current gate

Keep the production/runtime artifact on the NanoPi, deployment workflow, production services, and device network configuration unchanged until a separate hardware acceptance is authorized. The WIRE-4 patch is now part of the CI release build, but still requires hardware validation and review of the patch source. Before considering device deployment:

1. Port the protocol to the actual maintained client/server source trees (not a scripted disposable-clone patch) and obtain code review for the wire encoding and negotiation.
2. Add endpoint-level unit tests for malformed frames, nonce/sequence matching, replay, overflow, timeout/cancellation and WIRE-3 fallback; keep the model tests as supplemental coverage.
3. The experimental ARM64 system-VM restart/TUN dataplane scenario has passed in CI run #235. Preserve it as regression coverage; it does not replace protocol review or tests against the final packaged runtime artifact.
4. Validate the real VK authentication path and packaged-artifact provenance independently of the CI-only credential hook.
5. Produce a new explicitly versioned artifact only after all gates pass. Do not overwrite the pinned runtime asset or install/deploy automatically.

Until then, the CI fixture and its experimental artifact are diagnostics only. Do not deploy them or infer that the pinned production runtime has been updated.
