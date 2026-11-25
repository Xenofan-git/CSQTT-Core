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
