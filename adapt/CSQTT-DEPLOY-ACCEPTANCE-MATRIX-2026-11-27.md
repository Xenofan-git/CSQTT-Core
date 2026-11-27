# DeployManager: сверка и матрица приёмки — 2026-11-27

Полная приёмка **НЕ ЗАВЕРШЕНА**. Исходная точка этапа:
5420d649cde69fcc8ea7d7d4ee1696b0470ec02a, ветка
test/csqtt-deploy-authenticated-sftp-20261126. Старые успешные проверки
учтены как исторические; заново запускались только затронутые регрессии
и ранее незавершённые случаи.

## Соответствие оригиналу

Оригинал: Afteroid/csqttr, 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.
Сверены точные локальные исходники, включая DeployOperations.kt,
DeployManager.kt, SshTransportConnection.kt, deploy/ServerCheck.kt и assets/deploy.sh.

| Оригинал 2.5.3 | Core | Состояние реализации и отличия |
|---|---|---|
| performDeploy(), строки652–843 | upstream_lifecycle.go, runRemoteLifecycle | ARM64 lab path реализован: SSH, architecture, assets, inventory, staging, однократный exec, независимый postcheck, cleanup. Реальный полный lifecycle не принят |
| Update в performDeploy() | тот же путь + отдельный pinned lifecycle installer | Не отправляет новые прикладные пароли; сохраняет серверные env/overrides/config/SQLite. Нет destructive reinstall |
| performUninstall(), строки845+ | lifecycleRemove | Передаёт только закреплённый установщик. Исправлена лишняя локальная зависимость от replacement ELF и install-only baseline |
| DeploySSHClient.execResult(), строки252–342 | upstream_exec_result.go | Раздельные ограниченные stdout/stderr, потоковый callback, timeout/cancel/unknown; команда не повторяется |
| Числовой progressRegex оригинала | publishLifecycleProgress | Исправлен пропуск NaN; обычный прогресс сохранён. Сырой вывод скрыт для защиты неизвестных клиенту паролей при update |
| DeployManager.kt | deploy_libs_core.go, headless_main.go | Busy/state/log, один процесс, timeout, фактический прогресс, результат после проверки. Android coroutine/StateFlow заменены Go/API |
| ServerCheck.kt | validatedInventory/checkLifecycleState/Post | Сохранено требование законченного inventory, добавлены Core ownership/ports/identity и проверка постоянных данных |
| serverArchitectureForMachine(), строки190–201 | server_architecture.go | Все aliases AMD64/ARM64/ARMv7; неоднозначный SSH-вывод отвергается. Наличие alias не разрешает установку пакета |

Обоснованные отличия: Go/Linux/OpenSSH вместо Kotlin/Android/SSHJ;
root-only, private staging0700, строгие SHA/ELF/host-key/machine-id/VM/scope,
порты UDP47000/TCP47002; сохранение настроек/SQLite и независимая проверка результата.
Не добавлялись Docker, purge/reinstall, произвольные порты, автоматическое
переключение на чужие instances, ослабление trust или повтор неизвестной команды.
Исходная серверная модель сохранена в пределах разрешённой Core-адаптации.

## Матрица архитектур

PASS означает только указанную проверку. FAIL означает несоответствие
конкретному требованию; NOT TESTED — соответствующее исполнение отсутствует.
Статическая проверка и CLI не равны приёмке сервера.

| Проверка | AMD64 | ARM64 | ARMv7 |
|---|---|---|---|
| Определение uname/aliases и отказ неизвестной/неоднозначной архитектуре | PASS | PASS | PASS |
| ELF class/machine/endianness и отказ несовместимому ELF | PASS | PASS | PASS |
| Точный SHA/размер готового Core8-кандидата | PASS | PASS | PASS |
| Статические зависимости/ABI на соответствующем build sysroot | PASS | PASS | PASS |
| Реальный --help и --protocol-revision в изоляции | PASS, native, ранее | PASS, QEMU-user, новый | PASS, QEMU-user, ранее |
| Нейтральный dpkg install/update/remove | PASS, прежний отдельный dpkg-root на AMD64 | PASS, новая отдельная полноценная QEMU VM | NOT TESTED |
| Нейтральный SQLite/config preservation | NOT TESTED для SQLite; conffile PASS ранее | PASS, SQLite вне package payload | NOT TESTED |
| Требование полного подключённого серверного Deploy для архитектуры | FAIL: пакет не подключён/не принят | PASS только наличие ARM64 lab path; приёмка ниже | FAIL: пакет не подключён/не принят |
| Реальный Core install/update/uninstall через DeployManager | NOT TESTED | NOT TESTED | NOT TESTED |
| Автоматический Core rollback после искусственной ошибки | NOT TESTED | NOT TESTED | NOT TESTED |
| Реальный обрыв SSH во время выполнения установщика | NOT TESTED | NOT TESTED | NOT TESTED |
| Реальная панель → SSH → установщик → runtime | NOT TESTED | NOT TESTED | NOT TESTED |

Архитектурные tests выполнялись на хосте и ARM64 QEMU с инертными ELF
заголовками; это проверка выбора/формата, а не запуск трёх серверов.
AMD64/ARMv7 положительные SHA/ABI/CLI не продвигают пакет автоматически в
approvedServerAsset. Существующий ARM64-only execution gate сохранён.
Это открытый функциональный пункт, а не объявление полной поддержки трёх целей.
Платформа панели/контроллера остаётся Debian/Ubuntu ARM64.

Все три текущих Core8 ELF требуют GLIBC_2.39; локальный matching sysroot
имеет требуемые version nodes. ARMv7 дополнительно EABI5 hard-float,
ARMv7-A/VFP/NEON. Это не обещание работы на каждом Debian/Ubuntu/armhf:
фактические CPU, loader и libraries целевой ОС ещё проверяются на стенде.

## Реальное исполнение нейтрального пакета

Новый стенд Moscow:
 /srv/development/isolated-checkouts/csqtt-neutral-arm64-lab-20261127

Отдельный Debian13 ARM64 QEMU guest,512MiB/1vCPU, новый COW overlay,
отдельные UEFI/NoCloud seed, NIC/forwarding/shared host mounts отсутствуют.
После проверки guest штатно выключился. Исходный base сохранён; его SHA:
2021d78771e0e9a656b1dad9f54b4223151324fc64a8bf3299d5ff61520378c8.

Настоящий dpkg внутри VM выполнил6/6 PASS:
install1.0; update2.0 с сохранением изменённого conffile и отдельной SQLite;
отказ повреждённому deb до изменения состояния; remove с сохранением данных;
purge; очистка временных файлов. Пакеты содержат только текст, без maintainer
scripts и исполняемого payload. Пароль в fixture синтетический.

Это испытание ОС/dpkg/SQLite. CSQTT deploy.sh, DeployManager и автоматический
rollback Core в VM не запускались. Отказ повреждённому deb не является rollback.
Удаление fixture через purge также не означает поддержку Core purge.

Тестовый сценарий: test-neutral-package-qemu-guest.py.
Первый успешный guest запуск привязан к SHA сценария в MANIFEST.json.
Позже усилен только отказ вне нужного guest: до записи result.json,
включая python -O. Отрицательная проверка на Moscow PASS без побочных записей;
неизменённые package steps повторно не запускались.

SHA256 нейтральных deb:
- 1.0: 1011bb67c8f479b33b089770fc90244c5e9f421975833be325af144edbbe4830
- 2.0: 4e22e2f9d9a70d0a11fd8f57963d7164c7c59fd1c42f4e4296cb8ad709c93bfb

## Новые проверки кода и фикстуры

| Проверка этапа | Результат | Граница доказательства |
|---|---|---|
| uninstall без replacement ELF / только script | До исправления2 FAIL, после5 subcases PASS | Подставной SSH останавливается на architecture; сети нет |
| Затронутые assets/scope/postcheck/uninstall Go race | 7 групп PASS | Модульные/локальные файлы |
| Новый NaN progress regression | FAIL до исправления, PASS после, race | Не выдаёт нечисловой прогресс |
| Новые uninstall и progress tests ARM64 | По1 группе PASS под QEMU-user | Реальный ARM64 test executable, не сервер |
| Bridge lab pin regression | 5 групп race PASS | Контроллер/вызов helper |
| ARM64 helper/controller test builds и vet | PASS | Сборка/статическая проверка |
| Actual helper → stdio SFTP → simulated SSH commands | 7/7 PASS на промежуточном helper cbcf09bc… | Установщик никогда не вызывается |
| Authenticated API → controller → окончательный helper | 7/7 PASS | Тот же fixture; не real remote install |
| ARM64 Core CLI | 2/2 PASS | Только early-return CLI, namespace без сети/caps |
| Отказ guest script на рабочем host с python -O | PASS | Никаких dpkg действий/записи result на host |
| diff check / формат Go / Python syntax | PASS | Изменённые файлы |

Process fixture проверяет install/update/uninstall, потерю сохранённых данных,
подтверждённый rollback и два unknown exec:
exit255 после прогресса и после неоднозначного success marker.
Для каждого exec подтверждён ровно один вызов. При unknown staging сохраняется;
его удаляет только сам локальный fixture после проверки. Это не фактический
сетевой обрыв SSH и не доказательство rollback настоящей службы.

Прежние46 Go групп, static ABI, browser390/768/1280/1440/1920 и4 случая
offline UI interruption учтены по AGENTS10.24–10.27; не выдаются за новый прогон.
Исторические реальные SFTP upload/readback и cleanup после потери сессии
учтены отдельно (AGENTS10.3/10.27), не повторены.

## Артефакты и оставшаяся работа

Окончательный laboratory helper SHA256:
285c8882a122ab146a1a89df488d1336ac9529b13856c5cc8ec028fb4fcb3d64.
Bundle:
 /srv/development/isolated-checkouts/csqtt-acceptance-evidence-20261127/candidate-final

Остальные pins неизменны:
- inventory helper277e98735d6cb87222c9a908a47c193a8917d6535dd5c6958c9aba681356c731;
- lifecycle installer3ef1b3977d289e4bd878fe9ad87395abf30345c9b54c784bf69876ed33c43529;
- ARM64 server8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d.

Логи, exit codes и SHA: csqtt-acceptance-evidence-20261127 на Moscow;
машиночитаемый индекс — evidence/deploy-acceptance-20261127.json.
Полный порядок подготовки и сценариев приёмки:
[CSQTT-DEPLOY-ISOLATED-LAB-REQUIREMENTS.md](CSQTT-DEPLOY-ISOLATED-LAB-REQUIREMENTS.md).

Открыто: допустимый отдельный IP/SSH identity; ARM64 полный lifecycle и fault
injection; нейтральные ARMv7 проверки; принятие/подключение AMD64 и ARMv7
пакетов и соответствующий lifecycle; полноценная панель/прогресс на реальном
установщике. Нейтральная offline VM не является разрешённым Deploy endpoint.

Amsterdam csqtt250/csqtt25/csqtt-48000 active, PID1409/1649/1308 и их
UDP/TCP46000/46002,46100/46102,48000/48002 проверены в соответствующих network
namespaces. Host47000/47002 свободны; они не использовались как разрешение
на экспериментальную установку. Панель NanoPi не изменялась.
Main/release/production promotion не выполняются. Новые коммиты публикуются
только в рабочую ветку; старый CI SUCCESS не переносится на новый SHA.
