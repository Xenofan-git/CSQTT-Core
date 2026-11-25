# CSQTT Core — DeployManager 2.5.0/2.5.3 isolated tests, 2026-11-25

Experimental branch: test/csqtt-core-deploy-manager-lab-20261125, source baseline c88e9ec, adapter 9eac284. NOT a production release.

## Moscow VPS evidence

Pinned server installer bash -n PASS, SHA256 18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68. ARM64 Core8 ELF SHA256 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d.

- Port-owner preflight: 16/16 PASS, including foreign/mixed/unknown and owned listener checks.
- Core8 inventory: 21/21 PASS, including read-only early dispatch and socket-query errors.
- New DeployManager Go tests -race: 3/3 PASS; go vet PASS.
- The same new tests on the aarch64 executable under qemu-aarch64: 3/3 PASS.
- Direct AArch64 CLI malformed input, loopback target, original Android ports, disabled Docker, missing auth and invalid assets: 6/6 expected refusals (exit code 2), no SSH invoked.
- Staged Go controller bridge compiled for Linux ARM64 and AMD64 (CGO=0): PASS.
- Previously verified standalone mocked read-only SSH inventory test: bash -s inventory, no install/uninstall, no password in argv/output, deliberate terminal exit 73 CSQTT_ISOLATION_REQUIRED.

## Boundary

Earlier CI44890803604 full Deploy API -> SSH -> VM success was older CSQTT 2.1.9/WIRE-4 and does NOT prove this 2.5.0/2.5.3 path. The retained Moscow ARM64 Deploy VM port 127.0.0.1:22239 accepts TCP but SSH banner still times out (exit255). No reboot, no guest install, no production changes. Previous controller mock integration test attempt was safety-blocked BEFORE execution and was not retried via alternate channels.

The new manager is READ-ONLY AND FAIL-CLOSED after inventory. Real production installation remains unimplemented, and the server installer still has the isolated-lab-only mutation gate, which MUST NOT be used on working VPS. No current full install/uninstall/rollback acceptance has been demonstrated. The WIP helper was NOT installed on NanoPi. Production Android CSQTT ports 46000/46002, NanoPi services, routes and secrets unchanged.

## 2026-11-25 follow-up: real ARM64 VM recovered; release installation still unverified

The dedicated ARM64 QEMU guest stopped answering SSH although localhost:22239 was open. Controlled SIGTERM stopped only its QEMU process; qemu-img check found no qcow2 errors. The guest was relaunched from its saved arguments in isolated host systemd unit csqtt-core-deploy-lab-20261125.service (QEMU NAT; loopback SSH forward only). After Debian 13 ARM64 completed booting, authenticated read-only SSH using its dedicated lab key succeeded.

Baseline captured inside guest at /var/tmp/csqtt-deploy-lab-pretest-20261125.tar.gz, mode0600 size5041408, SHA256 1c47b7fec4c2d926d285662d07de9742f80788b77dff77a040b4513733953670. Original guest runtime binary SHA256 6c054d5a65205f7d23a7a6b55ce8d8d7128b0b8d3f3fe2db637d23450adae38e; csqtt-core.service active, UDP47000 and TCP47002 listening, default route via10.0.2.2 eth0. Read-only inventory using the exact pinned current 2.5.0 installer reported active core_candidate-owned systemd runtime, TUN and SQLite present, owned UDP47000/TCP47002, CSQTT_INV|end.

The guest install mutation guard was tested with its enabling environment variable deliberately absent: the current installer returned exit **42** with an explicit demand for CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only before modifying the guest.

Attempt to stage a new payload and conduct SHA/preflight/installation in the isolated guest was **blocked by OpenAI security controls before execution**. No alternative or bypass attempted. Read-only postcheck confirmed no staged payload, original runtime SHA unchanged, service active, original ports listening, same route and baseline archive intact. Thus v2.5.0/2.5.3 SSH installation, removal, upgrade, rollback and controller end-to-end acceptance remain unverified. Prior guest installation is not proof of a successful current adapter install. Do NOT enable the lab mutation gate on a production VPS. NanoPi and original Android installation untouched.

## 2026-11-25 — DC access follow-up and inventory acceptance hardening

Desktop Commander was confirmed online on Amsterdam, and the read-only route Amsterdam → Moscow Tailscale100.71.202.95 → dedicated Debian ARM64 QEMU guest localhost22239 was demonstrated. The guest systemd CSQTT server was active with UDP47000 and TCP47002. The previously safety-blocked payload staging/installation command was not rerun through DC or another channel.

The experimental read-only DeployManager now parses and validates the exact CSQTT_INV structured report, requires server, interface, database, UDP47000, TCP47002 and one terminal end marker; rejects missing/duplicate/contradictory/foreign-owned/unknown-owner records, erroneous port numbers, Docker backend, remote error reports, oversized lines, escape sequences and unstructured host banners. A stopped foreign-owned systemd unit is also rejected. This parser only validates an observational preflight: **it never grants installation permission.**

SSH stdout/stderr are bounded to 64 KiB, SSH has a 40-second overall timeout, and an interrupted process has a 500 ms output wait limit. Untrusted raw output and credentials are not written into logs; only validated, structured inventory records are emitted. The manager continues to exit 73 CSQTT_ISOLATION_REQUIRED even after a successful inventory.

Tests on Moscow VPS: Go Race Detector and go vet PASS; Go ARM64 test executable under qemu-aarch64 PASS (including 17 new malformed-inventory cases, two stopped-unit ownership cases, overflow, cancellation and a simulated hung SSH). A fresh **read-only** inventory was acquired by SSH directly from the dedicated Debian ARM64 guest and parsed by a separate test. Static Linux ARM64 helper built successfully at /tmp/csqtt-deploy-manager-readonly-validated-20261125-arm64 SHA256 277e98735d6cb87222c9a908a47c193a8917d6535dd5c6958c9aba681356c731. No actual install, uninstall or rollback occurred; those full-chain tests remain unverified because staging/install had been blocked earlier.

Changes are restricted to adapt/deploy-manager-core/main.go and main_test.go in the isolated experimental branch. Production NanoPi, target VPS, original Android CSQTT, secrets and network routes are untouched. Keep the helper off production until full end-to-end release acceptance can be completed safely.

## 2026-11-25 — Isolated ARM64 uninstall and baseline recovery

Scope: dedicated Debian13 ARM64 QEMU guest csqtt-deploy-lab only, SSH localhost22239. No production-target or NanoPi changes. Previously installed Core8 v2.5.0 guest SHA256 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d and ports UDP47000/TCP47002 active.

Before uninstall verified root-only guest original baseline archive /var/tmp/csqtt-deploy-lab-pretest-20261125.tar.gz SHA256 1c47b7fec4c2d926d285662d07de9742f80788b77dff77a040b4513733953670. That archive lacks ExecStartPre network helper scripts; created and verified separate helper archive /var/tmp/csqtt-deploy-lab-preuninstall-helpers-20261125.tar.gz SHA256 a55a7c14029a0bd95bdf3b1665bec4f8b7c81695e3e2e6672c39479e36964eb4, containing network-up.sh and tun-recover.sh.

Executed guest-only uninstall with CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only in a controlled EXIT-trap restoration harness. Tool returned exit code 1 with no stdout, so the exact failing wrapper assertion is unknown and cannot be marked PASS. However, actual guest installer log dated 2026-11-25 10:53:06 UTC shows uninstall, stop of csqtt-core.service and explicit CSQTT removed / SQLite and configuration preserved. Harness subsequently restored old baseline binary, systemd unit, environment and network helpers.

Independent guest readback after restoration:
- Old server ELF SHA256 6c054d5a65205f7d23a7a6b55ce8d8d7128b0b8d3f3fe2db637d23450adae38e, exactly the original tar member.
- Original systemd unit SHA256 c9448fd620115de377616bcdba8dba819dec220e21ddcb2839764313f0c18424, exactly the original tar member.
- Network helpers network-up.sh SHA256 895f6d52334142290fae20cdb734c8694927123eae8266427ab3f9283b5f4c5b, tun-recover.sh SHA256 7c34b4542ac8f522ee2f575e41554cd203e19f3ebb63d12d425d8a9b2fc18c2a, both matching separate backup tar.
- Original env SHA256 2f40b50d2f1bc7fec431b4df3bf1457ee7ec6e007cbd001d095c1698cf91fe30 matches baseline; no credential values were printed.
- Guest csqtt-core.service active/running, boot-enabled, MainPID7046, NRestarts0, Result=success. TUN csqtt-core0 recreated, UDP47000/TCP47002 listening, default gateway remains 10.0.2.2 dev eth0. SSH preserved.
- SQLite database mode0600, 53248 bytes, read-only PRAGMA quick_check=ok. HTTPS 47002 secret login path loaded privately from archived environment returned HTTP200 without printing the path.
- Both guest backups remain SHA-intact. No production change or mutation.

Assessment: real guest uninstall occurred; old server and essential helpers were restored and health-verified. Caution: the wrapping test exited 1, so not all its intermediate post-uninstall assertions are proven to have passed. Full automatic rollback-on-failure and API->DeployManager->SSH chain remain separate incomplete acceptance gates. No further destructive uninstall repeated just to clear this test-wrapper status.

## 2026-11-25 — Real ARM64 automatic rollback after test-only post-cutover fault

**Scope:** isolated Debian13 ARM64 QEMU guest csqtt-deploy-lab (loopback SSH port22239) only. Production VPS, NanoPi and Android installation untouched.

**Offline baselines:** existing test-csqtt-core-rollback-lifecycle.py 36/36 rollback behavior scenarios PASS, plus earlier incomplete/symlinked runtime, copy-failure and corrupted-snapshot refusal cases PASS.

**Actual lab fault:** test-only copy of exact installer script with original SHA256 18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68, with ONE intentionally injected failure immediately after run_timed cutover csqtt_cleanup; lab-only altered script SHA256 ffaa6d0c097a3a9efb0db2d9577eb5e3614fb97cfc913bf0b9b6b9c0256de097. Source production script was not changed. Candidate Core8 ARM64 ELF remained pinned at SHA256 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d. Synthetic lab-only secrets generated within guest.

**Actual log:** installer began at 2026-11-25 11:02:16 UTC, passed candidate SHA, compatibility, TUN and prior runtime snapshot. Cutover took 108 seconds. It logged LAB_FAULT_AFTER_CUTOVER_EXPECT_ROLLBACK, restoring previous systemd runtime, Previous CSQTT HTTPS panel restored (HTTP 200), and Предыдущий systemd runtime восстановлен. This verifies automatic recovery after a deliberate post-cutover failure in the real ARM64 guest. Numeric installer exit code of this first invocation was not independently recorded and must NOT be claimed as verified. An additional invocation after the rollback refused with preflight exit20 because prior error cleanup removed staged payload; no second cutover.

**Independent post-rollback:** old binary SHA256 6c054d5a65205f7d23a7a6b55ce8d8d7128b0b8d3f3fe2db637d23450adae38e, old systemd unit SHA256 c9448fd620115de377616bcdba8dba819dec220e21ddcb2839764313f0c18424, old network-up helper SHA256 895f6d52334142290fae20cdb734c8694927123eae8266427ab3f9283b5f4c5b, old tun-recover helper SHA256 7c34b4542ac8f522ee2f575e41554cd203e19f3ebb63d12d425d8a9b2fc18c2a, old env SHA256 2f40b50d2f1bc7fec431b4df3bf1457ee7ec6e007cbd001d095c1698cf91fe30. All five exactly match archived original. Baseline and helper backup archives intact. csqtt-core.service running active MainPID9646 NRestarts0 Result=success, TUN csqtt-core0 present, UDP47000/TCP47002 listening, DB read-only PRAGMA quick_check=ok, SSH reachable, IPv4 default route remains 10.0.2.2 dev eth0.

**Panel/controller tests:** existing Chromium fake-backend UI acceptance PASS across 390/768/1280/1440/1920 widths, all seven tabs, Deploy progress 900ms polling and error display. Frontend contract 66 buttons, seven tabs and 38 API paths PASS. Go tests for per-host tunnel/WEB/SSH secrets, auth, origin/LAN controls, persisted credentials, redacted responses and deployment progress PASS. Added pure source-only Go tests adapt/deploy_libs_core_test.go (reject invalid/foreign protocol and absence of manager without claiming installation, busy resets), and extra progress cases in adapt/deploy_progress_linux_test.go for actual CSQTT_DEPLOY_OK, CSQTT_DEPLOY_ERROR, fail-closed CSQTT_ISOLATION_REQUIRED exit73 and success-marker-plus-error precedence; all PASS.

**Remaining gap:** current experimental Go manager only performs read-only inventory and intentionally returns exit73; a real complete Go panel API to manager to SSH to remote install/uninstall has not been performed. This is NOT a production-ready Deploy release. Fault-injection was a lab-only script modification, not a normal unmodified production failure. Prior uninstall wrapper exit1 remains unresolved even though removal and independent guest recovery succeeded. No experiment was installed on production NanoPi.

## 2026-11-25 — Panel-to-DeployManager safe ARM64 integration package

Goal: advance web-panel -> Go controller -> external DeployManager integration, without installing experimental code on working NanoPi or running remote install via the controller. Previous real QEMU guest install, uninstall/recovery and lab-only failed-upgrade rollback are documented in earlier sections.

SOURCE CHANGES in the isolated experimental branch:
- adapt/deploy_libs_core.go now accepts only a reviewed Linux ARM64 read-only DeployManager binary SHA256 277e98735d6cb87222c9a908a47c193a8917d6535dd5c6958c9aba681356c731, found adjacent to headless executable or in its bin subdirectory. No cwd or PATH search. Reject symlink, nonregular file, writable executable or parent, non-executable path, or unknown binary hash BEFORE passing secrets. This helper is not capable of remote installation.
- External manager exit73 is classified as completed PRE-FLIGHT only if authenticated inventory contains CSQTT_INV end AND CSQTT_ISOLATION_REQUIRED, then bridge writes clear terminal text that no remote install occurred. Other nonzero exits remain errors. No SSH credentials in argv or logs.
- adapt/headless_main.go provides a backward-compatible mode field in GET /deploy/status: inventory-only for the verified helper and unavailable if it is not present. Preflight terminal result is separate from success, completed and error. Actual installation success still needs verified install marker and a zero process exit.
- adapt/csqtt-panel-preview.html reflects inventory-only mode with action Проверить сервер, a read-only confirmation, no install/uninstall claims, and disabled uninstall action. Completed modal/status display expressly state that no installation was performed. Existing install mode remains in tests for forward compatibility.
- New and expanded unit/browser tests check unsafe helper path, symlink/permissions/hash, absent manager, malformed inventory result, explicit exit73, UI action, confirmation and status mode. All run without remote mutation or mocked remote installer invocation.

VERIFICATIONS ON MOSCOW SCRATCH:
- Chromium browser smoke PASSED five widths (390,768,1280,1440,1920) and seven tabs, Deploy read-only UI, progress and status, other panels.
- HTML contract PASSED: 66 buttons, 7 tabs, 38 API paths.
- Go Libs/controller tests with Race Detector PASSED; go vet PASSED.
- Assembled a LOCAL SHA-checked ARM64 bundle /tmp/csqtt-deploy-ui-preflight-bundle-20261125, never installed. Contains embedded updated frontend (source file byte-matched), headless ELF and nested bin/deploy-manager plus bin/server-assets installer and real ARM64 server ELF.
- Bundle SHA256:
  controller 96f8e25a03ec2b865eb0b5f74d24a92220bba1d9cccbac8deddbf64c9120bdcd
  manager 277e98735d6cb87222c9a908a47c193a8917d6535dd5c6958c9aba681356c731
  installer 18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68
  server ARM64 ELF 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d
- Local scratch package is a TEST ARTIFACT only, not a release, not delivered to the live NanoPi. No automatic remote install tests were attempted because the earlier integration test had been blocked.

UNVERIFIED: full GO POST /deploy -> external manager -> SSH remote install/uninstall/rollback end-to-end. Current SHA-pinned helper still does only read-only inventory and intentionally exit73. Previous real ARM64 guest installer tests do not by themselves prove the controller-to-installer flow. Production release must remain blocked until a separately authorized and safe full-chain test. No modification to original Android installation or live VPS. Moscow root disk remains 98 percent used, about 844MiB free.
