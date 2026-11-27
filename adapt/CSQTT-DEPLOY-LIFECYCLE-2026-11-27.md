# DeployManager CSQTT Core — результат этапа 27 ноября 2026

Статус: реализована связка лабораторного lifecycle с реальным SSH/SFTP транспортом,
контроллером и панелью. Полная эксплуатационная приёмка **не завершена**:
реальные install/update/uninstall с открытым mutation gate в этом этапе не запускались.
Симуляции ниже не являются доказательством установки сервера.

Рабочая ветка: test/csqtt-deploy-authenticated-sftp-20261126.
Исходный HEAD: f85fb8bdc301476fb83b83badd39a10080e23d12.
Оригинал: Afteroid/csqttr, 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.
Исходники прочитаны из checkout с подтверждённым HEAD. Правильный путь менеджера
оригинала — app/src/main/java/com/csqtt/client/DeployManager.kt, без ui/legacy.

## Перенесённое поведение

| CSQTTR 2.5.3 | Реализация Core |
|---|---|
| performDeploy(), Install | runRemoteLifecycle: SSH, архитектура, pinned assets, предварительная проверка, WEB env/main_password JSON, SFTP, однократный запуск install, проверка exit/marker и независимого состояния, очистка |
| performDeploy(), Update | Та же последовательность; новые прикладные пароли не передаются. Отдельный кандидат deploy.sh получает update и копирует существующие настройки на сервере по модели оригинала |
| performUninstall() | Передаётся только сценарий; выполняется uninstall с ownership checks. Успех публикуется после независимого подтверждения отсутствия runtime/портов/TUN и сохранности конфигурации/SQLite |
| DeploySSHClient.execResult() | Общий исполнитель с раздельными stdout/stderr, потоковыми callback, ограничением вывода и подтверждённым exit status. SSH255, timeout, signal и потеря транспорта не становятся успехом |
| DeployManager | Один активный процесс, ограниченная длительность, busy/state/log, прогресс реальных этапов, результат после postcheck, освобождение ресурсов |
| ServerCheck | Существующий строгий inventory сохранён; лабораторный lifecycle дополнен проверками identity/OS/VM, SQLite и hashes постоянных данных |

Путь исполнения действительно подключён к remoteTransport. Старый
simulateDeployLifecycle остаётся отдельной исторической моделью тестов;
новый CLI --isolated-lifecycle её не вызывает.

Панель сохранила семь вкладок, оформление и существующие окна.
На вкладке Deploy добавлен выбор «Установка»/«Обновление с сохранением настроек».
Обновление не требует новых прикладных паролей. Для старого inventory helper
поле action исключается с сохранением отказа при неоднозначных JSON-ключах.
Удаление использует существующий прогресс/журнал. Завершение команды без
единственного корректного маркера теперь имеет неизвестный результат.

## Отличия и ограничения адаптации

- Kotlin/Android/SSHJ заменены Go/Linux и уже имеющимся OpenSSH/SFTP backend.
  Сохранён порядок операций оригинала. Новая модель установки не вводилась.
- Порты Core остаются UDP47000/TCP47002. Канонический установщик, его SHA,
  серверный ELF и прежний production helper pin не заменены.
- Новый сценарий воспроизводимо генерируется отдельно поверх уже проверенного
  baseline и прежнего uninstall candidate. Из 2.5.3 перенесён update; сохранены
  существующие Core ownership, port, rollback и isolation checks.
- Приватный каталог /tmp/csqtt-deploy-<random>,0700 и файлы0600 заменяют общие
  upload-пути оригинала. Сохраняются SHA readback, отсутствие shell/SCP fallback
  и запрет повторного запуска команды при неизвестном результате.
- Установщик сериализует lifecycle через flock. Update сохраняет также
  дополнительные существующие файлы настроек, а не очищает весь config-dir.
- Runtime доступен только при одновременно заданных оператором
  CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only,
  CSQTT_CORE_DEPLOY_LAB_TARGET=IP:SSH_PORT и
  CSQTT_CORE_DEPLOY_LAB_MACHINE_ID=<32 hex>.
  API не передаёт эти разрешения. Проверяются machine-id, Debian/Ubuntu,
  QEMU/KVM, ARM64 и действующие ограничения адресов/SSH host key.
- Docker, purge и разрушительная переустановка не добавлялись.
  Update требует существующий подтверждённый runtime, env и SQLite.
- Независимая проверка использует python3 со стандартным sqlite3 на стенде.
  Отсутствие зависимости или небезопасный путь приводит к отказу до staging.
  SQLite проверяется транзакционно, включая WAL; данные и конфигурация
  сравниваются до/после. Контроль строгий: изменение счётчиков при активном
  трафике также не позволяет объявить полное сохранение подтверждённым.
  Для текущей лабораторной приёмки требуется спокойный стенд.
- В прогресс выводится фактическая доля из установщика с фиксированным
  безопасным названием этапа. Сырой SSH-вывод не публикуется: при update
  он может содержать пароли/пути, неизвестные клиенту.
- При обрыве на стадии передачи сохраняется прежний безопасный cleanup.
  При неизвестном исходе **выполнявшейся команды** staging сохраняется до
  проверки сервера: удалять файлы работающего установщика или повторять
  команду автоматически нельзя. Это отдельно сообщается в ошибке.

## Проверки и их границы

| Проверка | Результат |
|---|---|
| Targeted manager Go race | 17 групп PASS |
| Те же manager-тесты ARM64 под QEMU | 17 групп PASS |
| Controller/bridge Go race | 9 групп PASS; после финального изменения bridge дополнительно 5 групп PASS |
| Controller ARM64 QEMU | 4 группы PASS |
| go vet manager/controller/bridge | PASS |
| Подготовка candidate update и закрытый mutation gate | 3 теста PASS; использованы временные файлы/SQLite, без служб |
| Actual ARM64 manager → stdio SFTP → simulated SSH commands | 5/5: install, update, uninstall, отказ при потере сохранённых данных, подтверждённый rollback |
| Аутентифицированный HTTP API → controller → pinned ARM64 helper → тот же fixture | 5/5 тех же сценариев PASS |
| Chromium UI, 390/768/1280/1440/1920, семь вкладок, новый update | PASS; API подменён |
| Статический контракт панели, JS/Python/bash syntax | PASS |
| Реальный удалённый lifecycle | НЕ ВЫПОЛНЕН |
| Новый GitHub CI | Не запускался; предыдущий SUCCESS45796135833 относится только к ff7f598 |

Уже завершённые сетевые аутентификационные SSH/SFTP испытания не повторялись.
Новые SFTP process fixtures проверяют изменённую интеграцию, а не повторяют
старую проверку авторизации. Никаких реальных ключей/паролей в fixtures нет.

Ошибки, исправленные по ходу работы:
- Неверный upstream путь DeployManager.kt дал404; использован настоящий файл.
- После нового сообщения PTY предыдущего прогона стал недоступен; дальнейшие
  результаты сохранялись в файлы.
- Static UI assertion ожидал старую последовательность скобок до update.
- Playwright ожидал отсутствующий Chromium1254; использован уже установленный1194.
- Новый browser assertion пытался читать строку post_data как dict; добавлен json.loads.
- Финальный bridge test сначала запущен вне Go-модуля; исправлен cwd и ./Libs.
- Review обнаружил исключённые из config hash symlink-файлы; проверка типов
  и прав теперь выполняется до исключения SQLite/overrides из hash.

Основные логи на Moscow:
- /tmp/csqtt-lifecycle-validation-20261127-jev84eq6/
- /tmp/csqtt-lifecycle-process-20261127.log
- /tmp/csqtt-lifecycle-validation-20261127.json

## Артефакт для последующей лабораторной приёмки

Каталог: /tmp/csqtt-deploy-lifecycle-20261127-candidate-2.
Helper SHA256:
03d312495b9382d1c61966ae14276d947b346d83e8514a62671546ccd3ff4bc1.
Lifecycle installer SHA256:
3ef1b3977d289e4bd878fe9ad87395abf30345c9b54c784bf69876ed33c43529,
134626 bytes.

Сборщик: adapt/build-csqtt-deploy-lifecycle-candidate.sh PINNED_ASSETS_DIR NEW_OUTPUT_DIR.
Он создаёт отдельный пакет, проверяет server/installer bytes и компилирует
Linux ARM64. Не устанавливает и не запускает его. Для зафиксированного helper
использован Go1.27.3, -trimpath, -buildvcs=false, CGO_ENABLED=0, GO111MODULE=off,
из текущего Moscow checkout. BuildInfo сохраняет GOPATH package path:
сборка другим toolchain/в другом checkout может получить другой digest.
Контроллер откажет непроверенному digest; его нельзя автоматически подменять.

## Что осталось

1. Реальные install → update с сохранением паролей/SQLite → uninstall на
   допустимом изолированном ARM64 стенде, затем проверка отказов/rollback.
2. Полная приёмка через панель на этом стенде, включая потерю SSH во время
   настоящего установщика, сохранность чужих сервисов/46000/46002/маршрутов
   и проверку runtime после операции.
3. Только после такой приёмки — отдельное решение о production capability,
   упаковке в рабочую сборку/релизе. Сейчас promotion, merge в main и релизов нет.

Проверка стенда по уточнению владельца от 2026-11-27:
- Moscow host располагает только собственными IPv4 200.165.226.52 и
  100.71.202.95; второй выделенный адрес в интерфейсах/маршрутах не найден.
  Эти адреса запрещены self-target проверкой при запуске helper на Moscow.
- Служба csqtt-core-deploy-lab-20261125.service активна. Виртуальная машина
  csqtt-deploy-lab: Debian13.8/aarch64/QEMU, 640MiB RAM, user networking.
  Проверены metadata QEMU, ip address/route, tailnet peer inventory и
  состояние гостя через существующий SSH-ключ со StrictHostKeyChecking=yes.
- Guest eth0 имеет 10.0.2.15; единственный hostfwd —
  127.0.0.1:22239 → guest22. RFC1918 и loopback не допускаются validTarget.
  В guest отсутствуют tailscale/tailscaled; отдельного csqtt/deploy-lab
  узла в доступном tailnet peer inventory нет.
- Python3, OpenSSH, flock имеются, root key authentication настроена;
  standalone sqlite3 CLI отсутствует, но адаптер использует Python sqlite3.
  Уже работает прежний csqtt-core.service, UDP47000/TCP47002 и csqtt-core0.
  Это не результат новой установки и не доказательство работы нового helper.
- Доступно около1374MiB RAM; основное препятствие — допустимая адресация
  и регистрация отдельного узла, а не доказанная нехватка ресурсов.
  Ни дополнительный публичный IP, ни авторизация регистрации нового tailnet
  узла в доступном контексте не предоставлены. В этой конфигурации подходящий
  endpoint не организован. Изменять работающий host forwarding/firewall,
  подменять собственные/зарезервированные IP и ослаблять guards не требуется.
  Новые сети/VM/службы и экспериментальная установка не создавались.

Точные требования к стенду для окончательной приёмки:
1. Отдельная Debian/Ubuntu aarch64 VM под QEMU/KVM с systemd, /dev/net/tun,
   Python3 со sqlite3, OpenSSH server с SFTP, bash/coreutils/flock/curl и
   зависимостями канонического установщика. Должны работать необходимые
   исходящие подключения установщика. Запуск helper — на другом узле.
2. Собственный достижимый IPv4 и SSH-порт: выделенный маршрутизируемый
   публичный адрес либо отдельно зарегистрированный Tailscale guest.
   Адрес не должен быть loopback/RFC1918/адресом контроллера; hostname
   вместо IPv4 текущий валидатор не принимает. Нужны разрешение на новый
   tailnet узел и штатная авторизация регистрации, если выбран этот вариант.
   Не использовать IP рабочего VPS с перенаправлением вместо identity VM.
3. Отдельный root SSH credential, независимо сверенный SSH host key,
   machine-id гостя и явная operator scope из раздела выше. Реальные
   credentials не сохранять в репозитории или отчётах.
4. Перед изменениями сохранить исходный диск/UEFI и описание сети; создать
   отдельный overlay/снимок с уникальной machine-id. Для install необходим
   чистый baseline без Core runtime/config/SQLite/TUN и с незанятыми
   UDP47000/TCP47002. Существующую лабораторную установку не считать чистой.
5. Выполнить через настоящий API/панель install, заполнить тестовые настройки
   и SQLite, выполнить update и uninstall. Сопоставить пароли, файлы и
   содержимое SQLite до/после; на время проверки исключить клиентский трафик.
   Отдельно проверить отказ/rollback и потерю SSH, не повторяя неизвестную
   команду. Зафиксировать процессы, порты, маршруты, состояние посторонних
   служб и Android46000/46002 до/после.
6. Успех: подтверждённый status/marker плюс независимая проверка состояния,
   реальная работоспособность установленного runtime и неизменность
   посторонних ресурсов. При ошибке — остановить испытание, сохранить
   диагностику и восстановить только lab overlay/снимок; рабочие VPS и
   NanoPi не должны быть целью установки или восстановления.

Владелец явно разрешил при невозможности получить подходящий endpoint
закончить разработку/сборку/доступные испытания и опубликовать рабочую ветку.
Применён этот вариант; полная lifecycle-приёмка остаётся открытой.

Подробный конспект находится также в AGENTS.md, раздел10.11, и синхронизируется
в оба журнала Амстердама и оба журнала NanoPi R3S LTS.

## Изменённые файлы

- AGENTS.md
- adapt/CSQTT-DEPLOY-LIFECYCLE-2026-11-27.md
- adapt/build-csqtt-deploy-lifecycle-candidate.sh
- adapt/csqtt-panel-preview.html
- adapt/csqtt_deploy_lifecycle_candidate.py
- adapt/deploy-manager-core/lifecycle_state.py
- adapt/deploy-manager-core/main.go
- adapt/deploy-manager-core/remote_transport.go
- adapt/deploy-manager-core/upstream_exec_result.go
- adapt/deploy-manager-core/upstream_exec_stream_test.go
- adapt/deploy-manager-core/upstream_lifecycle.go
- adapt/deploy-manager-core/upstream_lifecycle_test.go
- adapt/deploy_credentials_linux_test.go
- adapt/deploy_libs_core.go
- adapt/deploy_libs_core_test.go
- adapt/deploy_process_linux_test.go
- adapt/deploy_progress_linux_test.go
- adapt/headless_main.go
- adapt/test-csqtt-deploy-lifecycle-candidate.py
- adapt/test-csqtt-deploy-lifecycle-process.py
- adapt/test-csqtt-panel-browser.py
- adapt/test-csqtt-panel-contract.py
