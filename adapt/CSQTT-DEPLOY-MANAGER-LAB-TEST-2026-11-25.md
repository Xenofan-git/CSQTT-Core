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
