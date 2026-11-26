# DeployManager candidate integration review, 2026-11-26

Parent fa33cff65101e864ce4ed65d6b0980fc18c18fa2, Moscow development VPS. All 17 manager test groups pass with Go race detector, and all 17 pass on the actual ARM64 test executable under QEMU user-mode. Go vet passes; static ARM64 manager compiled SHA256 cd2b3e9e1e5eccfb0b7cd77c60e0bd3cdc1f10458b0657584d9c2cdeba183002. The actual previously pinned server assets are used, recorded ARM64 inventory is historical, SSH is simulated. Logs /tmp/csqtt-deploy-review-20261126-race.log and /tmp/csqtt-deploy-review-20261126-arm64.log.

The existing controller plus original approved inventory helper passed the authenticated API process test (2.20 s). Unchanged controller separately rejected the unapproved new helper. In a disposable test-only controller build, with the candidate SHA pinned in scratch source only and PATH limited to a local synthetic SSH executable, the actual new ARM64 manager passed the extended HTTP -> controller -> manager process contract (5.74 s). Reordered valid inventory is canonicalized and exposed in status API, unverified/misreported TUN ownership is rejected, incomplete reports fail, concurrent request and uninstall guards hold. No remote SSH connection or real credentials. Scratch source pin and helper reverted after the test; candidate test executable removed. The production/repository pin remains unchanged. Log /tmp/csqtt-deploy-api-integration-20261126.log.

This confirms the new read-only helper is compatible with the controller in a controlled process fixture. It does NOT implement SFTP/SCP, real installer execution, update or uninstall on a VPS. No production package, release, actual deployment or new CI result. Previous remote-operation restrictions are not circumvented.

## 2026-11-26 — Real remote transport integration

Branch: test/csqtt-deploy-authenticated-sftp-20261126, parent ff7f5982be1da3b47713a66ff2bfb41815d13403.

DeployManager now uses a shared OpenSSH transport for normal inventory and a real
SFTP v3 subsystem adapter for staging. No Go dependencies were added. Host keys
must already be trusted in the invoking account's known_hosts; unknown/changed
keys are rejected. User SSH configuration and forwarding are disabled. Password
authentication uses a private askpass helper; secrets never enter command argv
or remote logs. Key authentication uses the supplied existing identity.

The manager accepts an additional --stage-only option before --assets.
It decodes/validates the existing request, reads the pinned installer and ARM64 ELF
once, validates exactly those bytes, performs inventory, then transfers both
snapshots into an exclusive remote /tmp/csqtt-deploy-<random> directory (0700).
Files are created exclusively (0600), read back with bounded SFTP packets and
checked by exact size/SHA-256 before rename. The mode removes its own files and
directory and emits only CSQTT_STAGE_VERIFIED_AND_CLEANED on success.
It never emits installation success or executes the installer. This explicit
mode has not been invoked against a real server with executable release payloads.

SFTP mutations are not retried after ambiguous results. After channel loss,
only cleanup may reconnect to remove the known private paths; cleanup errors
retain the directory in the returned diagnostic. No recursive remote deletion.
Three-minute staging limit, 40-second inventory limit, bounded cleanup context.
The shipped controller's helper digest, panel mode, installer mutation guards
and production deployment are unchanged.

New acceptance actually performed:
- Build and go vet on Moscow VPS using Go1.27.3.
- Targeted -race: new bounded-protocol/malformed-response tests and manager
  rejection before SSH; pinned asset verification/snapshot regressions after
  factoring verifiedAssets. All four top-level groups passed.
- Actual Linux ARM64 test executable built on Moscow, executed on the ARM64
  Termux client over a real network SSH connection to the Moscow VPS.
  TestRemoteTransportRealEndpoint passed in 24.93s: 266240 inert text bytes,
  SHA/size verification, final readback after rename, temporary-file absence,
  directory absence, and the same two-object staging function used by the CLI.
- TestRemoteTransportLostSessionCleanup passed in 6.38s: deliberately canceled
  only this adapter's SSH child, cleanup reconnected and a separate SFTP session
  proved directory absence. No system SSH process/service/config changed.
- Independent Moscow /tmp listing found no session staging directories.
- ARM64 manager SHA256:
  4fbf25bcc35126e87e64ac84d0c971a8311758ccc529e59f5cecb6cdcf837854.

Previously passed local SSH/SFTP fixture suites were not repeated, per user
instruction. Offline CI now clears both network-test environment variables and
explicitly skips these two opt-in network tests; this update was syntax checked,
not a claim of a fresh complete CI run. New source is compiled, but not shipped
to NanoPi or pinned by the controller. Real install/update/uninstall execution
and independent lifecycle postcheck still need integration.

### Уточнение базы изменения
Код транспорта опубликован коммитом b29c8c9e9add41cc52ba6e8de758bbdf83792537.
Его непосредственный родитель — b0be4c7 (добавление AGENTS.md), появившийся
в рабочей ветке во время выполнения задачи; ff7f598 — исходная точка аудита.
Промежуточный коммит изменяет только AGENTS.md, сохранён без изменений.
Инструкции прочитаны после обнаружения. Проверенный исходный код транспорта
совпадает с опубликованным; повторная проверка исходников из-за документации
не требуется. Коммит содержит [skip ci], чтобы не повторять пройденные
локальные SSH/SFTP-проверки вопреки указанию пользователя.

## 2026-11-26 — Уточнение источника модели и кода по указанию владельца

Пользователь подтвердил: модель и код берём из оригинального CSQTTR 2.5.3.
GitHub tag v2.5.3 повторно проверен и указывает на
84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.

Проверены первоисточники:
- https://github.com/Afteroid/csqttr/blob/84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806/app/src/main/java/com/csqtt/client/ui/legacy/DeployOperations.kt
- https://github.com/Afteroid/csqttr/blob/84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806/app/src/main/java/com/csqtt/client/ui/legacy/SshTransportConnection.kt
- https://github.com/Afteroid/csqttr/blob/84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806/app/src/main/java/com/csqtt/client/ui/legacy/deploy/ServerCheck.kt

Сопоставление текущего адаптера с оригиналом:
- Оригинальный DeploySSHClient.uploadInternal использует SSHJ SFTP put/lstat,
  далее SCP/stream fallback и ограниченные повторы. Новый remote_transport.go
  реализует SFTP packets самостоятельно и SHA readback; это собственный
  транспортный прототип, не буквальный перенос оригинального кода.
- performDeploy проверяет архитектуру VPS, подготавливает/проверяет assets,
  передаёт установщик и сервер; при Install передаёт WEB env и password
  overrides, при Update не подменяет сохранённую конфигурацию.
- execResult запускает удалённую команду и собирает вывод/exit status;
  performDeploy проверяет exit0 и маркер CSQTT_DEPLOY_OK.
  performUninstall имеет отдельный путь и маркер CSQTT_UNINSTALL_OK.
- Наш stage-only передаёт/проверяет/удаляет пару файлов, но не выполняет
  оригинальный install/update/uninstall. Его успешные тесты не закрывают
  перенос lifecycle. Предыдущую запись о транспорте трактовать в этих границах.

Следующий код должен переносить исходную модель и функции с явными
платформенными отличиями Linux ARM64, портами47000/47002 и изоляцией CSQTT Core.
Использование Go вместо Kotlin требует адаптации библиотечного backend;
это не основание придумывать другую последовательность Deploy. Исходный
SFTP wire-прототип пока не заменён и не объявляется оригинальной реализацией.
В этом уточнении меняются только документация/постоянные инструкции.
Новых тестов, CI, изменения исходного runtime или развёртывания нет.
