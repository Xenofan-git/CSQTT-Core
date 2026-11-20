# LaLune CSQTT isolation gate

**Status (2026-10-15): deployment remains BLOCKED / NOT AUTHORIZED.** The DeployManager is being switched to the original CSQTT v2.1.9 `deploy.sh` flow, derived into `deploy-csqtt-lalune.sh` with namespaced resources. This is not permission to deploy to NanoPi.

## Source-derived deployment design

The chosen source is the pinned [CSQTT v2.1.9 deploy.sh](https://github.com/amurcanov/csqtt/blob/v2.1.9/app/src/main/assets/deploy.sh). DeployManager must stage the architecture-matched patched server binary, protected web credentials, and one-shot deployment overrides to a unique remote directory, then invoke the derived script's normal `install` or `uninstall` action. It must never execute the raw upstream script on the target host.

The original sequence is retained: validate the uploaded binary and configuration before cutover; preflight the platform; prepare networking/firewall; activate the namespaced service; check runtime and web-panel health; and use the original cleanup/uninstall flow. The standalone `install-csqtt-lalune.sh` and `uninstall-csqtt-lalune.sh` scripts remain regression fixtures only and are not called by the Deploy button.

## Resources that must be namespaced

- Binary and helpers: `/usr/local/bin/csqtt-lalune`, `/usr/local/lib/csqtt-lalune`
- Unit: `csqtt-lalune.service`; Let's Encrypt and Docker helper units/containers use unique LaLune names.
- Configuration/database: `/etc/csqtt-lalune`; runtime/log paths: `/run/csqtt-lalune`, `/var/log/csqtt-lalune-install.log`
- UDP peer port `47000`; HTTPS web port `47002`
- TUN `csqtt-lalune0`; subnet `10.67.68.0/24`
- Policy tables/priorities `47001` and `47066`; packet marks `0x6741` and `0x6742`; firewall comment `CSQTT_LALUNE_MANAGED`
- The patched server receives `CSQTT_SYSTEMD_UNIT=csqtt-lalune.service`, so panel-triggered restart actions cannot restart `csqtt.service`.

The script adaptation must also preserve these safety rules:

1. Preflight both UDP/47000 and TCP/47002 before changing the current runtime. An unrelated listener, or a listener whose owner cannot be identified, causes a fail-closed refusal; it is never killed.
2. Only the LaLune-owned service, files, TUN, firewall markers, sysctl filenames, certificates, and helper processes may be stopped or removed. Generic legacy proxy interfaces and the original CSQTT resources are out of scope.
3. Use a unique remote staging directory and mode-0600 one-shot credential files; remove the staging directory on success and failure.
4. Do not flush the main route table or global route cache. The server may add its dedicated TUN subnet route, but the main/default routes and pre-existing policy rules must be identical after removing that expected route from the comparison.
5. Preserve the persistent database/configuration during uninstall, following the original CSQTT behavior.

## Required end-to-end proof

The gate opens only after CI passes all of these against a disposable Debian ARM64 system VM:

- Static tests prove the deploy script is derived from the pinned v2.1.9 source and retains upstream validation, activation, health checks, and uninstall.
- The original namespaced script installs and starts the actual patched ARM64 server on UDP/47000 and TCP/47002, authenticates a real CSQTT protocol exchange, decodes TUNCONF, and passes bidirectional IPv4/TUN packet delivery.
- A same-process client protocol recovery test passes after restarting only `csqtt-lalune.service`.
- The actual Deploy button API stages assets over SSH, installs using the same original script, returns `CSQTT_LALUNE_DEPLOY_OK` and the panel URL, then uninstalls via the same script and returns `CSQTT_LALUNE_UNINSTALL_OK`.
- Pre-existing `csqtt.service`, `csqtt-47000.service`, and `csqtt-47000-netns.service` remain active with unchanged unit files; foreign routes, policy rules, and firewall entries survive install and uninstall; main routes return exactly to baseline.
- A separate listener on TCP/80 survives both actions, proving the optional Let's Encrypt stage defers instead of killing it.

No production NanoPi, routing, firewall, DNS, Tailscale, HydraRoute, or 3x-ui settings are changed by this work. Passing CI would validate only the disposable-VM lifecycle, not authorize a production deployment.

## Protocol trace for the next test

The pinned v2.1.9 source trace identifies why the current UDP fixture cannot establish tunnel readiness:

- The existing pinned wire fixture is a legacy `CSQTT-WIRE-2` first-stage setup request. It proves credential decryption/session creation, but does not complete the client's current configuration exchange.
- The client builds the follow-up request as `GETCONF:<local_port>|<device_id>|<password>|<generation_id>|<salt>|<worker_id>|<desired_stream_count>|CSQTT-WIRE-3`.
- The server's configuration handler validates that request, allocates or resolves the device IP, then returns `TUNCONF:<device_ip>:<dns>:<client_port>:stream-v2` (or a denial/no-config response). The server's TUNCONF response must be decoded with the established session's actual packet keys; checking raw UDP bytes for the string would be invalid.
- The client implementation is in upstream `rust-client/protocol.rs`, `rust-client/session.rs`, and `rust-client/wrap.rs`. The next harness should reuse those real protocol functions or equivalent server test helpers, not invent encryption or hard-code an encrypted response.
- Only after authenticated configuration is decoded should a second test inject a valid IPv4 packet through the isolated TUN and assert it arrives at the authenticated opposite endpoint. The test must also assert that unrelated main routes and the three production-like services remain unchanged.

This trace is analysis of pinned source, not a passing TUNCONF/data-plane test. No deployment authorization is implied.

## Current CI status and next end-to-end gate

- CI run #352 (commit `cf201e2`) passed in a disposable ARM64 system VM. It proved that the packaged ARM64 server answers a pinned, encrypted upstream `GETCONF` fixture with a protocol response and that install/uninstall preserves the simulated pre-existing services and main routes.
- This is **not** proof of client-side `TUNCONF` handling or real IP packet delivery through the TUN dataplane. The test fixture currently validates only the encrypted setup request/response at UDP level.
- Before enabling deployment, extend the VM test to seed a disposable device/password configuration, assert a decoded `TUNCONF` containing the expected test IP/DNS, and then run a two-ended data-plane test that sends a known IP packet through an authenticated CSQTT session and verifies delivery on the opposite TUN/UDP endpoint. The test must fail if it merely observes an open port or a non-empty encrypted response.
- NanoPi installation remains unauthorized and must not be attempted as part of these CI steps.

## Comparative source audit (2026-10-10)

Pinned references used for comparison:

- LaLune desktop Linux implementation: `Endlad2/LaLune@e4a6d08b63aef6025bf8ad8f77da660ea552e04b`, file `Desktop/Linux/app_linux.go`.
- CSQTT server: `amurcanov/csqtt@v2.1.9`, files `app/src/main/assets/deploy.sh`, `rust-server/net_setup.rs`, `rust-server/tun_device.rs`, and `rust-server/protocol.rs`.
- Integration patcher: `adapt/patch-csqtt-core-source.py` in this repository.

### Confirmed differences

1. Original LaLune Linux client creates `csqtt0`, appends DNS servers to the host `/etc/resolv.conf`, and installs `default dev csqtt0`. That behavior must not be copied into the NanoPi gateway path: it changes host-wide routing/DNS rather than confining client traffic to a dedicated policy route.
2. Original CSQTT v2.1.9 server hard-codes `csqtt1`, `10.66.67.1`, and `10.66.67.0/24` across more than one Rust module. The integration patcher changes both TUN definitions, the subnet prefix used by the route table, model-side subnet references, and protocol test fixtures; changing only `net_setup.rs` would be incomplete.
3. The upstream proxy-routing module uses fixed policy table/priority values, packet marks, and iptables comments. The patcher assigns LaLune-specific values (`47001`, `47066`, `0x6741`, `0x6742`) and LaLune-prefixed comments, and removes global `ip route flush cache` operations. This is a source-level adaptation; runtime ownership behavior still requires behavioral tests.
4. The original server's configuration protocol and the current VM smoke test are different levels of validation. The VM sends the pinned legacy `CSQTT-WIRE-2` fixture and checks only that the server returns at least 32 bytes. It does not prove the current `CSQTT-WIRE-3` configuration request, authenticated `TUNCONF` decoding, or IP packet forwarding.

### Newly explicit acceptance tests still required

- Run the upstream Rust unit tests against the patched server source (including protocol/configuration tests) and verify the subnet-related tests have been adapted rather than silently skipped.
- Add a test that demonstrates the current client configuration request is accepted and that the response is decoded into the expected test IP/DNS using the real session keys.
- Add a two-ended test that sends a valid IPv4 packet through an authenticated session and observes the same packet at the intended peer TUN/UDP endpoint.
- Verify route-rule/firewall cleanup by comparing exact pre-existing entries before and after start/stop, including an adversarial rule with a similar but non-identical comment. Text grep checks alone are not sufficient.
- Keep the deployment gate closed until these tests pass. Do not run the package on NanoPi as part of this audit.


## Latest verified CI attempt (2026-10-12; run #443, commit `e26317c6b3c4a238435b8835f5b1004bf601c88b`)

This section supersedes the older "Current CI status and next end-to-end gate" claims above where they conflict. The earlier sections are retained as historical notes describing the state at the time they were written.

- Workflow: [CI run #443](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38232218662).
- The ARM64 VM job was rerun. The latest-attempt job ID is `114764732312` and completed with conclusion `success`; the build job `114764733592` also completed with conclusion `success`.
- The VM booted a Debian 13 ARM64 guest under QEMU and tested the actual packaged ARM64 server/installer artifact in that disposable guest.
- Direct routing policy lookup passed in the guest: marked traffic selected `direct-test0`; unmarked traffic selected `csqtt-test0` in table 202. The test uses dummy interfaces and test routes, not the production network.
- The live isolated server's encrypted response was authenticated/decrypted by the test client as `TUNCONF`, assigning `10.67.68.2` and DNS `77.88.8.8,77.88.8.1`.
- The TUN dataplane test decrypted a downlink IPv4 packet and reported `LIVE_TUN_BIDIRECTIONAL_IPV4_PASS`; the guest ping reported 1 transmitted, 1 received, 0% packet loss.
- Installer lifecycle/isolation in the ARM64 guest passed (`ARM64_SYSTEM_VM_LIFECYCLE_PASS`). Relevant client TUN failure/rollback Go tests also passed in this run.
- The job log timestamps for these checks are 2026-10-12 10:25–10:29 UTC. This is evidence from the rerun, not merely the earlier attempt.

### What this proves — and what it does not

**Now demonstrated in isolated CI:** ARM64 artifact build; real ARM64 Linux VM boot; Direct policy route selection using test devices/routes; authenticated/decrypted TUNCONF response from the isolated server; bidirectional IPv4 packet delivery through the test TUN dataplane; and installer lifecycle checks in a disposable guest.

**Still not demonstrated:**

1. A complete independent stock LaLune Rust client session running as a separate client process and completing the full handshake/configuration exchange against the packaged server. The patcher includes selected pinned client protocol/wrap/obfuscation code in the server test binary, and the VM probe authenticates/decrypts the server response; this is not the same as a full independent stock-client runtime test.
2. Reconnect/recovery after a real session or transport disconnect, including confirming that stale server/client state and routes are cleaned up and recreated safely.
3. Adversarial runtime cleanup coverage for every firewall/rule ownership case, especially a foreign rule with a similar-but-not-identical comment. Existing installer harness and VM preservation checks provide useful coverage but do not establish every possible host-state collision.
4. Applying the CSQTT panel's Direct-tab settings through the backend to the live LaLune routing policy and proving traffic behavior. The panel/UI persistence test is not proof of this integration.
5. Any production behavior on NanoPi. No production deployment, service, route, firewall, DNS, or network setting was changed by these tests.

### Gate decision

Deployment remains **BLOCKED / NOT AUTHORIZED**. Do not run the installer or uninstaller on NanoPi and do not merge/deploy solely on the strength of CI #443. Next tests should prioritize: (a) an independent client-process end-to-end exchange, (b) disconnect/reconnect recovery, and (c) adversarial ownership/cleanup preservation tests. After those, verify the Direct-tab backend-to-policy integration and review the resulting evidence before reconsidering the gate.


## Existing SESSION_LEASE is not a heartbeat (2026-10-14 follow-up)

A targeted audit of the pinned client and server sources refines the finding above:

- The server defines `SESSION_LEASE = b"\\xffCSQTT_LEASE"`. After decrypting/authenticating an inbound packet, it refreshes that session's `last_inbound_ms` and then returns immediately when the payload equals `SESSION_LEASE`. It sends no acknowledgement for this message.
- The pinned client `rust-client/session.rs` contains no `SESSION_LEASE` sender or lease-ack receiver. Therefore this constant is not an existing end-to-end liveness protocol and must not be treated as proof that the client can detect a dead peer.
- The server's legacy all-0xff keepalive path is also one-way: `is_idle_keepalive` returns without a reply.
- A safe candidate needs an explicit, authenticated request/ack exchange and capability negotiation. Without negotiation, a new client that expects acknowledgements from an old server could misclassify a healthy but incompatible peer as dead and repeatedly reconnect.
- Do not implement a production timeout until capability negotiation, nonce/sequence validation, missed-ack bounds, healthy-idle behavior, and mixed-version behavior are covered in tests. The current test-only timeout remains diagnostic only.

This follow-up is a source-audit correction, not a client fix. Packaged-client provenance and the closed deployment gate remain unchanged.

## Client liveness finding (2026-10-14; pinned-source audit)

Pinned client: `Endlad2/csqtt-core@3eafb7ee868f5f2690edf3b0326153813f88eb5`.
Pinned server: `amurcanov/csqtt@v2.1.9`.

- In the pinned client, `TransportReader::recv()` awaits `TurnReceiver::recv()` without an inactivity deadline. The session supervisor exits into the worker retry loop when its reader/writer returns an error, but an indefinitely pending receive does not produce that error.
- The server accepts and ignores client idle keepalive packets (`is_idle_keepalive`); that behavior does not itself provide a client-observable acknowledgement.
- The server's `STREAM_ALIVE` control frame is part of the stream-repair round and is emitted while a repair round has recently observed the stream inventory. It is not evidence of a periodic, general-purpose session heartbeat.
- The disposable E2E confirms the distinction: with the injected test-only receive timeout, the same client process re-registers all nine workers and restores bidirectional TUN traffic; without it, the process stays alive but receives no fresh TUNCONF within the 80-second bound. Both legs are diagnostic CI successes, not proof that the stock packaged client recovers.

### Required next client-recovery design gate

Do **not** promote the test-only idle timeout into production as a generic fix without a protocol/liveness review: a quiet but healthy tunnel may legitimately have no inbound payload, so an arbitrary receive-idle deadline can cause needless reconnect loops.

Before changing the packaged client, choose and test a bounded liveness signal that distinguishes a dead CSQTT peer/session from legitimate idle traffic. The candidate must specify:
1. Which authenticated message or transport-level failure proves peer liveness, how often it is expected, and how missed responses are bounded.
2. Backward compatibility with the pinned server/client protocol and behavior when one side does not support the signal.
3. Worker/session teardown and retry behavior, including TURN allocation reuse versus replacement, with no duplicate workers or stale dispatcher registrations.
4. Unit tests for healthy idle sessions, missing heartbeat/peer death, delayed responses, packet loss, and recovery; then the existing real Rust client E2E must prove same PID, fresh authenticated TUNCONF for all nine workers, and bidirectional TUN traffic after restart.
5. Build provenance for the client artifact. The currently packaged ARM64 client is a separately pinned runtime artifact, not this disposable source build; a source-only test patch does not update or fix that package.

Until these gates pass, report the stock-client limitation as known and keep **production deployment** disabled. The new button-to-SSH E2E is limited to a disposable VM. Do not touch NanoPi, production services, routes, firewall/DNS, VPS, or Keenetic during these tests.


## CI follow-up (2026-10-14; commit `fd409a401d7136798bdf03a9f863661924212e99`)

- [Pinned Rust client source tests, run 38551706823](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38551706823): success. The Linux workflow runs `cargo test --locked --bin client -- --test-threads=1 --skip dispatcher::tests::uds_tun_replacement_delivers_data_without_restarting_dispatcher`. The skipped test relies on the real TUN file-descriptor receiver, while the pinned `rust-client/tun.rs` implementation on this Linux target is a deliberate stub (the real receiver is Android-only). This makes the Linux suite platform-appropriate but does **not** validate Android UDS/TUN replacement; that remains an explicit gap.
- [Native client process-recovery E2E, run 38551706831](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38551706831): both matrix legs passed. The no-watchdog leg remains diagnostic and confirms the stock receive wait does not recover within the test bound; the watchdog-enabled leg restores TUNCONF and bidirectional dataplane in the same client PID, but only after a timeout was injected into a disposable source checkout.
- [ARM64 build and system VM, run 38551706933](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38551706933): build and VM lifecycle/policy/protocol checks passed in the disposable guest.
- This is CI validation only. It does not fix the packaged client, establish VK-auth compatibility, or authorize deployment to NanoPi. The liveness protocol remains proposal-only until negotiated authenticated request/response is implemented and tested against both endpoints.


## Deploy button wiring and original CSQTT lifecycle (2026-10-15)

The latest build and disposable ARM64 system-VM run is green: [run 38784450196, commit 2ea2f3542b2273fb51d14ba2e927795690aca9ba](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38784450196).

- The original uploaded CSQTT v2.1.9 `app/src/main/assets/deploy.sh` was verified against the pinned SHA-256 `6c94fb8dd033a4f1bab2961e7a1f6edfc1bdc532129d4e9cbf459684ba61ef5a`. `adapt/patch-csqtt-deploy-script.py` derives `deploy-csqtt-lalune.sh` from that exact source; the DeployManager uses the original install, activation, health-check, and uninstall flow. The raw upstream script is never run directly on the target.
- Resource conflicts are namespaced: UDP `47000`, HTTPS/TCP `47002`, TUN `csqtt-lalune0`, subnet `10.67.68.0/24`, service/helper/config/runtime/log/certbot/Docker names, sysctl filenames, firewall markers, and policy identifiers. The original CSQTT `46000/46002`, `csqtt.service`, generic `csqtp*` interfaces, and shared firewall markers are not adopted or removed.
- Before cutover, the derived script checks both ports. Unrelated or unidentifiable listeners fail closed; only a process positively identified as LaLune-owned may be stopped. A foreign TCP/80 listener is preserved and the optional Let's Encrypt issuance is deferred.
- The Go API creates a short-lived mode-`0600` secrets file. DeployManager stages the matching ARM64/x86_64 binary and credential overrides in a unique mode-`0700` remote directory, invokes the derived script over SSH, and removes the staging directory on completion. The server uses `CSQTT_SYSTEMD_UNIT=csqtt-lalune.service` for panel-triggered restart actions.
- Verified in the latest disposable ARM64 VM run: the real ARM64 server authenticated the encrypted GETCONF exchange, returned a decryptable `TUNCONF`, passed bidirectional IPv4/TUN traffic, and passed the test-only same-process recovery and redeploy/persistent-state checks. The real client binary's VK call-hash validation passed. The Deploy API-to-SSH path installed the namespaced service, confirmed UDP/47000 and TCP/47002 listeners and the HTTPS panel, preserved the three pre-existing CSQTT-like services and unrelated routes/rules/firewall state, then uninstalled through the same API while preserving the database/configuration. The run emitted `DEPLOY_BUTTON_INSTALL_UNINSTALL_E2E_PASS`.
- This closes the **CI Deploy API → SSH → original-derived installer/uninstaller integration gate** on a disposable VM. It does not authorize production deployment. Remaining production gates are unchanged: stock packaged-client idle/reconnect behavior (the passing recovery test uses a test-only timeout), exhaustive adversarial ownership/cleanup cases, and Direct-tab UI → backend → live routing-policy integration. Do not deploy to NanoPi or alter production network settings until those gates are reviewed and explicitly authorized.

## Superseding client-recovery evidence (2026-10-15; run #235)

This section supersedes the older recovery-status statements above where they conflict. Those statements remain as a record of the earlier test state.

- [Real Rust client process-recovery E2E](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38801699589): both watchdog matrix legs passed. In the no-watchdog leg, a disposable pinned Rust client/server checkout with the CI-only negotiated-liveness patch recovered after restarting only the isolated server. The same client PID parsed fresh TUNCONF, re-registered all nine workers and restored bidirectional IPv4 through the actual client UDP dispatcher and Linux TUN. Before and after restart, the three-packet probes had 0% packet loss.
- The separate experimental AArch64 client/server artifacts also passed a full Debian ARM64 system-VM test in the same run: quiet-tunnel PING/PONG periods succeeded, and after server restart the same process restored TUN traffic (3/3 packets before; 5/5 after; 0% loss).
- [Build and packaged-artifact ARM64 VM / Deploy API E2E, run #700](https://github.com/Xenofan-git/LaLune-NanoPi-ARM64/actions/runs/38801700145) passed its separate build, isolation, policy, and install/uninstall checks.

### Exact scope and remaining blockers

- **Proven:** same-process Rust-client recovery with the test-only negotiated PING/PONG patch in disposable pinned sources; full ARM64 system-VM dataplane recovery using separate experimental ARM64 binaries.
- **Not proven:** recovery by the unmodified stock packaged ARM64 runtime client. Its audit did not find `CSQTT_LIVENESS_V1`; the test patch is not part of that artifact.
- **Not proven:** live VK authentication or the complete acquisition-plus-recovery flow independently for Manual, Auto API and Auto VK. Parser/preflight/hash-validation tests do not establish those end-to-end flows.
- **Still required before production consideration:** reviewed implementation in maintained source trees; protocol security/compatibility tests; provenance and E2E validation of a newly versioned final artifact; exhaustive ownership/cleanup checks; and Direct-tab UI → backend → routing-policy integration.

The protocol proposal remains CI-fixture-only / not production-ready. Deployment remains **BLOCKED / NOT AUTHORIZED**. Do not merge or deploy as part of this correction; do not touch NanoPi, VPS, production services, routes, firewall, DNS, Tailscale, or Keenetic.

