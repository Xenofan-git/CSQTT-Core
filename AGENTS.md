# AGENTS.md — CSQTT Core

Постоянные инструкции для Codex и других агентов разработки. Основной язык общения, документации и интерфейса — русский. Идентификаторы кода и протоколов сохранять в исходной форме.

## 1. Назначение проекта

- Продукт: **CSQTT Core**, репозиторий [Xenofan-git/CSQTT-Core](https://github.com/Xenofan-git/CSQTT-Core).
- Целевые платформы: Debian/Ubuntu Linux ARM64 (aarch64); первая аппаратная платформа — NanoPi R3S LTS, Debian 13.
- Единая веб-панель для двух независимых ядер: CSQTT Core и sing-box.
- Серверные цели Deploy по уточнению владельца: AMD64 (x86_64), ARM64 (aarch64), ARMv7 (armhf). Каталог распознаёт все три; approved payload/исполнение сейчас только ARM64. AMD64/ARMv7 остаются открытой интеграцией до принятия пакетов; не объявлять полную поддержку по одному ELF/CLI. Клиентская панель остаётся ARM64.
- Это Linux-проект, не Android-приложение. Android-исходники служат источником для выборочной адаптации.
- История upstream: [CSQTT 2.1.9](https://github.com/amurcanov/csqtt/releases/tag/v2.1.9), затем [CSQTT 2.5.0](https://github.com/amurcanov/csqtt/releases/tag/v2.5.0), конечный ориентир — [CSQTTR 2.5.3](https://github.com/Afteroid/csqttr/releases/tag/v2.5.3).
- Точный исходный коммит 2.5.3: `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. Для воспроизводимой Linux-сборки применять существующие pinned upstream и selective patches, а не произвольную последнюю версию.
- Из ранней альтернативной реализации использован механизм автоматического получения VK-токена. Не возвращать старое название продукта в интерфейс. Не ломать исторические идентификаторы, лицензии и provenance ради переименования.

## 2. Как начинать каждую задачу

1. Определить фактическую ветку, HEAD, состояние рабочего дерева и актуальный remote HEAD. Не считать имя каталога доказательством ветки.
2. Прочитать этот файл, относящиеся к задаче исходники, последние записи журналов и результаты CI. Учитывать новые явные решения владельца проекта. Старые разделы README и журналов могут описывать уже исправленные дефекты.
3. Найти существующий функционал и тесты. Не выполнять повторно работу, которая уже завершена и не затронута текущим изменением.
4. Вносить минимальные целевые изменения. Не переписывать исправно работающие части, не смешивать несвязанные задачи.
5. Если задача требует внешнего доступа, а его нет, продолжать допустимую работу с доступными исходниками; явно перечислить непроверенное. Не придумывать доступ или результаты.

## 3. Архитектурные инварианты

- CSQTT Core и sing-box имеют независимые процессы, конфигурации, логи и жизненные циклы. Остановка одного ядра не должна автоматически останавливать другое.
- Весь управляемый панелью proxy-routing выполняет sing-box: собственный SOCKS5 inbound → упорядоченные правила → DIRECT, собственный SOCKS5 CSQTT или пользовательский SOCKS5 outbound.
- Рекомендуемый локальный sing-box inbound: `127.0.0.1:19180`; CSQTT SOCKS5 outbound — отдельный endpoint (в текущей спецификации `127.0.0.1:12500`). Перед изменением сверять реальные конфигурации. Не открывать неаутентифицированный публичный SOCKS5.
- DIRECT — первая закреплённая карточка: нельзя удалить или переместить; разрешены переключение и редактирование условий. Legacy Direct-списки мигрировать в одну каноническую карточку, без скрытых правил и потери явного Enabled.
- CSQTT — обязательное условное правило, независимое от включения процесса на вкладке «Подключение»; можно перемещать ниже DIRECT и переключать, нельзя удалить.
- Пользовательские правила: добавление, изменение, удаление, переключение, reorder; текущий лимит 15. First-match сверху вниз. Выключенные правила пропускаются.
- `route.final` — отдельный выход для unmatched traffic (по умолчанию direct); не скрытая безусловная карточка CSQTT.
- Категории доменов/IP: штатные sing-box Rule Sets, local/remote, JSON source/SRS binary. Не писать собственный legacy GeoSite/GeoIP parser. Не выдавать поддержку реестра за наличие полного готового каталога.
- Проверка конфигурации до атомарной активации, rollback при ошибке, сохранение действующей конфигурации и проверка ссылок перед удалением Rule Set.
- Не менять global/default routes, системный DNS, firewall, TUN auto-route/auto-redirect ради sing-box. Не затрагивать соседние службы и маршруты управления.

## 4. Веб-панель

Порядок семи вкладок: **Подключение → Туннель → Деплой → Логи → Директ → Маршрут → Sing-box**.

Сохранять утверждённые оформление, русский язык, графитово-фиолетовую тему и совместимость мобильных/настольных размеров. Не менять UI без необходимости. Карточки маршрутов с drag handles, переключателями и раскрывающимся редактором. Вкладка Sing-box показывает независимое состояние, настройки общего inbound (адрес, порт, протокол), конфигурацию и Rule Sets.

Длительные операции должны отображать фактическое состояние, этапы, результат или ошибку. Не показывать успешную установку по одному маркеру, неполному логу или завершению предварительной проверки.

## 5. DeployManager и безопасность эксплуатации

- Серверный CSQTT Core использует отдельные UDP 47000 / TCP 47002, не вмешивается в оригинальный Android CSQTT на 46000/46002.
- Существующие SHA-256 pins, проверки происхождения компонентов, изоляция, ownership/port checks и production gates сохраняются.
- Не принимать недостоверный inventory, не передавать секреты через аргументы команд, URL, логи и коммиты. Не копировать реальные ключи и пароли в тесты или документацию.
- Различать проверку окружения, локальные тесты, симуляцию, реальное развёртывание и приёмку. Не снимать защитные ограничения ради зелёного теста. Не обходить отклонённые инструментом операции альтернативным каналом или через пользователя.
- Не изменять рабочие VPS, NanoPi, Keenetic, Tailscale, HydraRoute, M-UI Fusion, Xray и другие сервисы без отдельного согласования. Перед разрешёнными эксплуатационными изменениями — план, резервная копия, критерии успеха и отката.
- До отдельной приёмки не продвигать экспериментальный пакет в production. Не публиковать релиз, не сливать в main и не менять рабочую ветку без согласования.

## 6. Карта исходников

| Файл / каталог | Назначение |
|---|---|
| `adapt/csqtt-panel-preview.html` | Веб-панель |
| `adapt/headless_main.go` | Linux Go API/контроллер |
| `adapt/singbox_config_linux.go` | Генерация конфигурации sing-box |
| `adapt/singbox_manager_linux.go`, `adapt/singbox_transaction_linux.go` | Жизненный цикл, применение и восстановление |
| `adapt/deploy_libs_core.go` | Мост контроллер → DeployManager |
| `adapt/deploy-manager-core/` | Go helper; проверки, inventory, локальные контракты |
| `server-assets/deploy-csqtt-core-250.sh` | Канонический закреплённый установщик |
| `adapt/derive-csqtt-core-deploy-250.py` | Воспроизводимая генерация установщика; кандидат отдельно |
| `adapt/derive-csqtt-core-unified-linux-253.py` | Воспроизводимая адаптация объединённых Linux-исходников |
| `adapt/test-csqtt-deploy-manager-offline.sh` | Автономная проверка менеджера |
| `adapt/test-csqtt-panel-contract.py`, `adapt/test-csqtt-panel-browser.py` | Статические и браузерные проверки панели |
| `.github/workflows/` | CI; проверять условия запуска и реальный статус |

## 7. Сборка и испытания

- Для Go DeployManager доступна автономная проверка: `bash adapt/test-csqtt-deploy-manager-offline.sh` (Linux, Go и Python; некоторые тесты требуют OpenSSH SFTP).
- Сохранять совместимость с Linux ARM64. В CI использовать версии toolchain из соответствующего workflow. Не подменять pinned компоненты случайными бинарниками.
- Перед коммитом: целевые регрессионные тесты, форматирование изменённого кода, `git diff --check`; Go race/vet и ARM64 в соответствии с затронутым компонентом.
- Для UI проверять семь вкладок и размеры 390/768/1280/1440/1920 px, API и сохранение настроек. При изменении маршрутизации проверять реальный закреплённый sing-box, а не только генерацию JSON.
- Учитывать происхождение fixture и ограничения среды. Раздельно фиксировать native ARM64, QEMU, локальные реальные протоколы, mock, skipped. Cross-compile не равен выполнению на ARM64.
- CI зелёный только после проверки результата всех нужных jobs именно на проверяемом SHA; старый зелёный run не распространяется автоматически на новый commit.
- Не запускать повторные или тяжёлые тесты без необходимости; сначала сверить реестр доказательств и доступные CPU/RAM/диск.

## 8. Подробное журналирование каждого действия — правило владельца от 2026-11-26

По прямому указанию владельца каждое действие по CSQTT Core конспектировать
подробно и в дальнейшем продолжать это во всех перечисленных местах:

1. **Амстердамский VPS:** /srv/projects/CSQTT-Core/journals/ —
   CSQTT-sing-box-PROJECT-SUMMARY.md и
   CSQTT-sing-box-for-Debian-Ubuntu-ARM64-JOURNAL.md.
2. **NanoPi R3S LTS:** /home/pi/chatgpt/CSQTT-sing-box-for-Debian-Ubuntu-ARM64/ —
   те же два имени файлов. Сначала проверять hostname и модель устройства;
   не путать NanoPi R3S с NanoPi R5C.
3. **Рабочий репозиторий Xenofan-git/CSQTT-Core:** этот AGENTS.md в корне
   актуальной рабочей ветки. Здесь хранить постоянные инструкции, подробный
   конспект действий, решений, ошибок, проверок и текущего состояния проекта.

Это указание заменяет прежнее ограничение «AGENTS.md только для краткого
checkpoint». Подробные действия нельзя оставлять исключительно в чате или
во внешнем отчёте: их конспект должен попасть также в этот файл.

Для каждого действия записывать: дату/время и идентификатор этапа; запрос/цель;
какой узел, ветка, исходный SHA и файл затронуты; команду или точное описание
операции без секретов; результат/ошибку; сделанные изменения; проверку и её
границы; новый SHA и CI при наличии; следующий шаг. Фиксировать и чтение,
проверку доступа, отсутствие изменений, отказ инструмента, неудачный запуск
и исправление. Не подменять неизвестное предположением или старым результатом.
Связанные короткие операции можно описывать одним последовательным пунктом,
сохраняя действие и результат каждой. Не создавать бесконечные записи о
самом журналировании: операция записи/сверки отмечается итогом своего этапа.

После каждого действия или короткой связанной группы дополнять конспект;
перед ответом пользователю завершать записи этапа во всех трёх местах.
После записи читать файл обратно; для точной копии сверять SHA-256.
Не затирать локальную историю NanoPi историей другого узла: дополнять
недостающими записями, перед объёмным переносом сохранять резервную копию.
Точные копии Амстердама хранить отдельно на NanoPi в amsterdam-master/:
CSQTT-AMS-MASTER-SUMMARY-LATEST.md и CSQTT-AMS-MASTER-JOURNAL-LATEST.md.

При недоступности места назначения записывать это в доступные журналы и
AGENTS.md, перечислять недоставленные записи и при восстановлении доступа
досинхронизировать их. Не объявлять запись выполненной без readback.
В AGENTS.md сохранять подробный проверенный конспект с идентификаторами
исходных записей; не копировать секреты или выдавать старую запись за новую.
Документационные изменения публиковать в текущую ветку; не повторять уже
пройденные тесты приложения/локального SSH/SFTP из-за правки журналов.

## 9. Checkpoint для начала работы — 2026-11-26

Это историческая точка передачи, не требование откатывать более новые коммиты.

- Подтверждённая владельцем ветка: `test/csqtt-deploy-authenticated-sftp-20261126`.
- Базовый проверенный SHA перед добавлением AGENTS.md: `ff7f5982be1da3b47713a66ff2bfb41815d13403`.
- CI Deploy: [45796135833](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/45796135833) — SUCCESS на AMD64 и ARM64; opt-in SSH fixture и внешние доказательства исключены из автономного CI.
- На Московском VPS: 20/20 групп Go race, 4/4 новых ARM64 QEMU сценария аутентифицированного локального SFTP. Проверка host/client key, порчи файла и целостности успешной передачи. Это отдельные одноразовые stdio-сеансы SSH с тестовыми ключами, без удалённого TCP-подключения и без запуска установщика.
- На более раннем SHA `c88e9ec` подтверждены CI: [браузер](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/45467844175), [ARM64 apply/rollback](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/45467844126), [SIGKILL recovery](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/45467844077). Не считать их автоматически доказательством новой сборки.
- DeployManager в текущем runtime **inventory-only**; после успешной инвентаризации возвращает `ErrIsolationRequired` / exit 73. Проверка API→controller→helper выполнялась с synthetic SSH. Полный удалённый lifecycle не реализован и не принят.
- Предыдущая прямая лабораторная установка и проверка rollback не доказывают работу полного пути из веб-панели. Реальный авторизованный VK/TURN full-chain также не принят.
- Следующие задачи согласовывать по актуальному состоянию: полноценный Deploy и отдельная изолированная приёмка, затем VK/TURN и окончательная Linux ARM64 приёмка. Не заявлять завершение по симуляциям; уважать ограничения доступного окружения.
- Отчёты: `adapt/CSQTT-DEPLOY-INTEGRATION-REVIEW-2026-11-26.md`, `adapt/CSQTT-DEPLOY-REQUEST-CONTRACT-2026-11-26.md`, `adapt/CSQTT-DEPLOY-EVIDENCE-2026-11-25.md`, `adapt/CSQTT-DEPLOY-MANAGER-LAB-TEST-2026-11-25.md`.

### Источник модели и кода Deploy — уточнение владельца 2026-11-26

Модель и код Deploy брать из оригинального Afteroid/csqttr v2.5.3,
commit 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806:
ServerCheck.kt, DeployOperations.kt (DeploySSHClient.upload/execResult,
performServerCheck/performDeploy/performUninstall), SshTransportConnection.kt,
ServerCoreCache.kt. Адаптировать эту последовательность и семантику к Go/Linux
ARM64 и отдельному namespace CSQTT Core; для каждого существенного переноса
указывать исходную функцию и причины отличий. Это уточняет реализацию раздела 5.

Оригинал использует готовый SSHJ SSH/SFTP backend. Собственный SFTP wire-клиент,
добавленный в b29c8c9, является нашей экспериментальной реализацией транспорта,
а не кодом оригинального Deploy. Его сетевые тесты не подтверждают перенос
performDeploy/performUninstall. При продолжении сверять и приводить реализацию
к исходной модели; не развивать отдельную заменяющую модель установки.
Платформенные отличия, существующие pins, проверка целостности/владения,
сохранение update-конфигурации и ограничения production сохраняются.

## 10. Подробный конспект проекта и действий текущей сессии — 2026-11-26

### 10.1. Принятые решения и фактическая точка продолжения

- Владелец подтвердил репозиторий Xenofan-git/CSQTT-Core и рабочую ветку
  test/csqtt-deploy-authenticated-sftp-20261126. Разработка/сборка ведутся на
  Московском VPS; журналы — на Амстердаме и NanoPi R3S LTS. Имя каталога
  не определяет текущую ветку: каталог csqtt-core-linux-unified-253-20261122
  при проверке фактически содержал branch test/csqtt-core-deploy-manager-lab-20261125,
  HEAD34adbcd, поэтому его не использовали как актуальный исходник.
- Активное дерево:
  /srv/development/isolated-checkouts/csqtt-deploy-authenticated-sftp-20261126.
  Начальная сверка дала ff7f5982be1da3b47713a66ff2bfb41815d13403.
  Перед текущей документационной задачей local/remote совпали:
  b59ae0d69b474a79ebc32958ef777970bf19ceab; дерево чистое.
- Цель проекта и все архитектурные/UI-инварианты описаны в разделах1–5:
  Linux ARM64, независимые CSQTT/Sing-box, маршрутизация только Sing-box,
  семь вкладок, Rule Sets и разделённые порты. Рабочий интерфейс исторически
  базировался на CSQTT2.5.0; конечный upstream для модели/кода — Afteroid
  CSQTTR2.5.3, точный commit84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.
- Пропуск «Директ» в первом перечислении пользователя сверили с журналами
  и кодом: пятая вкладка «Директ», шестая «Маршрут», седьмая Sing-box.
  Новой перестройки интерфейса не делали.

### 10.2. Восстановление контекста и аудит

1. Прочитана локальная SSH-конфигурация для поиска существующего доступа.
   Подключение к Амстердаму подтвердило hostname ams-1-vm-skeg.
   Каталог /srv/projects/CSQTT-Core содержит journals/staging/backups;
   это не рабочее Git-дерево. В локальной рабочей папке исходников не было.
2. Изучены архитектура, индексы и актуальные/относящиеся к задаче записи
   обоих журналов. Старый CONTEXT.md содержит устаревшие snapshots, поэтому
   факты сверялись с поздними датированными записями и кодом. Полный
   построчный аудит всей многомегабайтной истории не заявлялся.
3. GitHub connector использован для чтения веток, исходников, Actions и jobs.
   Приватный ключ доступа к GitHub не извлекался и в документацию не помещался.
   SSH к Московскому VPS подтвердил msk-1-vm-vw4n и чистую актуальную ветку.
4. В коде проверены закреплённый system-direct, независимый system-csqtt,
   порядок правил, реестр Rule Sets, общий inbound и разделение портов.
   Исправление legacy DIRECT Enabled уже присутствует; нового исправления
   маршрутизации в этой сессии не вносили.
5. CI45796135833 на ff7f598: SUCCESS на AMD64/ARM64; предыдущие
   45795740473 и45795194018 также SUCCESS. Это offline/fixture acceptance.
   На c88e9ec подтверждены browser45467844175, ARM64 apply/rollback45467844126
   и SIGKILL recovery45467844077. Старые статусы не распространялись на новый код.
6. Установлено основное незавершённое место: shipped manager выполняет
   inventory и возвращает ErrIsolationRequired/exit73. Ранее ручная установка
   в ARM64 guest и отдельный rollback не доказывают полный путь
   панель→контроллер→manager→SSH install/update/uninstall.
   Реальный авторизованный VK/TURN full-chain также не принят.
7. Результаты и подтверждение рабочей ветки внесены в оба журнала Амстердама
   с readback. В этот момент NanoPi-журналы ещё не были обновлены; пробел
   устранён при синхронизации, описанной в10.6.

### 10.3. Подключение реального транспорта — b29c8c9

Запрос: подключить удалённый транспорт, не повторяя уже пройденные локальные
SSH/SFTP-проверки.

1. Прочитаны main.go, staging_contract.go, lifecycle_contract.go, тесты,
   offline runner и отчёты Deploy. Найдено: SFTP peer существовал только
   в тестах, действующий helper останавливался после inventory.
2. Для редактирования получена рабочая копия Go-файлов в локальное workspace;
   изменения отправлялись обратно в актуальное дерево Московского VPS.
   Сборка выполнялась на VPS Go1.27.3, не подменяла production-пакет.
3. Добавлен remote_transport.go: OpenSSH для аутентификации и выполнения
   inventory, собственный ограниченный SFTPv3 wire-client для subsystem
   channel; строгий known_hosts, собственный временный askpass,
   ключ по существующему пути, отсутствие секретов в argv/сыром выводе.
4. main.go получил verifiedAssets: проверка размера/SHA/ARM64 ELF и передача
   тех же snapshot bytes, без повторного открытия проверенного pathname.
   Существующая inventory использует общий remoteTransport.
5. Новый режим --stage-only --assets PATH:
   validate→pinned assets→inventory→private remote directory→SFTP put→
   bounded readback size/SHA→rename→cleanup. Каталог0700, файлы0600,
   эксклюзивное создание. Маркер CSQTT_STAGE_VERIFIED_AND_CLEANED.
   Установщик не запускается, install-success не сообщается.
6. При неопределённом результате передача/rename не повторяются.
   Cleanup может переподключиться только для удаления известных файлов
   собственного каталога; рекурсивное удаление не применяется.
   Неполная очистка возвращает ошибку с путём каталога.
7. Добавлены remote_transport_test.go; staging_contract.go уточнён для
   рабочего peer; offline runner исключает opt-in network env/tests;
   существующий отчёт CSQTT-DEPLOY-INTEGRATION-REVIEW-2026-11-26.md дополнен.
8. Инструмент редактирования сообщил ошибку исполнения sandbox на одном
   вызове; разрешённые файлы локального workspace изменены через Python.
   Попытка обычного SSH Moscow→Amsterdam остановилась на неизвестном host key:
   known_hosts и доверие не менялись. Для inert network acceptance использован
   уже проверенный доступ Termux→Moscow, а не заблокированная установка в guest.
9. Новые протокольные проверки и отказ staging до подключения прошли -race.
   После изменения чтения assets прошли существующие pinned verification
   и snapshot regressions. Итого четыре top-level группы PASS; go vet PASS.
   Набор прежних local SFTP/stdio SSH fixtures не повторяли.
10. На Moscow собраны ARM64 test executable и ARM64 manager. Test executable
    запущен на физическом ARM64 Termux-клиенте, SSH endpoint — Moscow VPS:
    TestRemoteTransportRealEndpoint PASS24.93s, 266240 bytes inert text,
    final readback после rename, отсутствие part/directory; затем два inert
    объекта через общую функцию staging. Это реальная сеть, не local stdio.
11. TestRemoteTransportLostSessionCleanup PASS6.38s: отменён только дочерний
    SSH-процесс теста; новый канал использован для cleanup; отдельная сессия
    подтвердила отсутствие каталога. Независимый find /tmp подтвердил очистку.
12. ARM64 manager:
    /tmp/csqtt-deploy-remote-transport-arm64,
    SHA2564fbf25bcc35126e87e64ac84d0c971a8311758ccc529e59f5cecb6cdcf837854.
    Это собранный экспериментальный файл; controller pin и установленный
    NanoPi binary не изменялись. CLI с реальными executable release payload
    не запускался; проверки использовали inert text.
13. Проверены форматирование, go vet, diff --check, bash -n runner.
    Код опубликован b29c8c9e9add41cc52ba6e8de758bbdf83792537.
    Непосредственный parent b0be4c7 — отдельный docs-коммит с AGENTS.md,
    появившийся во время работы; сохранён и прочитан после обнаружения.
    Начальная точка аудита была ff7f598. Уточнение отчёта — f4a8070dcef5f9d2420772a7d19bdb0f14c0009e.
14. Оба коммита с [skip ci], чтобы не повторять локальные проверки.
    Свежий полный CI не заявлялся. Runtime/services/routes/DNS/firewall,
    installer gates, main, release и устройства не менялись.

### 10.4. Исправление направления по оригиналу2.5.3 — b59ae0d

- Пользователь уточнил: брать модель и код оригинального CSQTTR2.5.3.
  Проверен tag через GitHub API:84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.
- Прочитаны на immutable SHA:
  DeployOperations.kt, SshTransportConnection.kt и ServerCheck.kt.
  Первичные функции: uploadInternal, execResult, performServerCheck,
  performDeploy, performUninstall, planDeploy.
- Оригинал использует готовый SSHJ backend: SFTP put/lstat, далее предусмотренные
  SCP/stream fallback и ограниченные повторы; performDeploy определяет
  архитектуру, готовит assets, передаёт установщик/сервер и при Install env/
  overrides. Update сохраняет конфигурацию. execResult возвращает вывод/exit;
  успех проверяется по exit0 и соответствующему маркеру.
- Признано различие: наш wire-client — самостоятельный транспортный прототип.
  Он пока не заменён и не считается переносом полного Deploy2.5.3.
  Дальнейший код должен следовать исходным функциям и модели с явными
  платформенными отличиями, а не создавать другую последовательность установки.
- Правило внесено в AGENTS.md и существующий integration report;
  commit b59ae0d69b474a79ebc32958ef777970bf19ceab опубликован, дерево чистое.
  Документация проверена diff --check; тесты/CI/runtime не менялись.
  Оба журнала Амстердама дополнены и прочитаны обратно.

### 10.5. Предшествующие доказательства: не перепроверять без изменений

Эти сведения перенесены из датированных журналов и не являются новыми
испытаниями текущей сессии:

| Этап | Результат и граница доказательства |
|---|---|
| DIRECT legacy toggle, 4b473b1 | Явный Enabled карточки сохраняется при legacy lists; 111 Go top-level PASS, один fwmark VM SKIP, шесть route paths и браузерные сценарии PASS. |
| Rule Sets, 89830a0 | Реальный pinned Sing-box ARM64 через QEMU:12 TCP paths с JSON/SRS, приоритет/выключение/DIRECT; HTTPS source и cache после отключения источника PASS. Remote binary download и timed refresh не объявлены проверенными. |
| Deploy asset integrity, 57c0a68 | O_NOFOLLOW/nonblocking, точный размер/SHA тех же bytes, проверка ARM64 ELF; candidate helper не подменял production pin. |
| Update preservation, 6c451cb/049e942 | Сохранение env/overrides и ограниченный parser environment; локальная логика ещё не подключена к remote update. |
| Lifecycle contract, d922a16 | check→stage→execute once→postcheck, маркеры/exit/отмена; in-memory simulation, не удалённая установка. |
| API/process review, 42dea19 | Локальный ARM64 controller→manager с synthetic SSH и inventory; не полный install. |
| Local SFTP, c5efe5d | Реальный local SFTP subsystem для inert data, повреждения/очистка; без удалённого SSH. |
| Authenticated local SFTP, ff7f598 | Одноразовый sshd stdio с временными ключами; host/client key/целостность; без сетевого install. |
| UI24 ноября, c88e9ec и предшественники | Изменения вкладок, формы, темы, updater и Deploy status/progress описаны в62 восстановленных записях; старые success привязаны к своим SHA. |
| ARM64 guest install/rollback25 ноября | Отдельная прямая установка и восстановление наблюдались; это не доказательство вызова из нового manager/панели; ограничения и недостающие exit assertions сохраняются в журнале. |

### 10.6. CSQTT_CORE_TRIPLE_DETAILED_JOURNAL_POLICY_20261126

Запрос владельца: конспектировать каждое действие в журналы VPS и NanoPi R3S,
а также подробно вести все сведения проекта в корневом AGENTS.md.

1. Проверены branch/HEAD/clean state и remote HEAD на Moscow:
   b59ae0d, совпадение local/remote, без чужих незакоммиченных изменений.
2. Прочитаны текущий раздел8 AGENTS.md и прежняя политика двойного
   журналирования CSQTT_NANOPI_DOUBLE_JOURNAL_POLICY_20261121_1007Z.
   Новый запрос владельца заменяет ограничение кратким checkpoint.
3. SSH через существующий nanopi-vps подтвердил hostname NanoPi-R3S-LTS и
   /proc/device-tree/model:FriendlyElec NanoPi R3S LTS. Tailnet read-only
   подтвердил доступность R3S; отдельный R5C не использовался.
4. Проверены родительские AGENTS-файлы целевого каталога и оба NanoPi-журнала.
   Последние локальные записи были от23 ноября. amsterdam-master существовал.
5. Оба актуальных журнала Амстердама прочитаны в рабочую папку journal-sync;
   выделены62 отсутствующих раздела24–26 ноября из каждого файла.
   Копии переданы на NanoPi в .journal-sync-20261126-full-recording.
6. Для каждого NanoPi-журнала сохранён backup
   .bak-before-full-recording-20261126. Недостающие62 раздела добавлены
   с пометкой исторического переноса; проверено сохранение прежнего префикса
   bytes и наличие каждого перенесённого блока. Runtime/службы не менялись.
7. amsterdam-master обновлён точными copies с backup предыдущих LATEST.
   Проверено равенство bytes/SHA256:
   SUMMARY698afd060cef4969d6976fbb696428181beb8f67b7bb3d8381b92d49d4cac2ae;
   JOURNAL2447a3c7b8e739422cba94f3232dcf7ffa50238ad6ec92fea8ee23ddb256a342.
   Это хеши snapshot ДО итоговой записи текущего этапа; после неё mirror
   обновляется снова. Не сравнивать старый hash с дополненным файлом.
8. Раздел8 AGENTS.md заменён новой постоянной политикой; раздел10 содержит
   подробный конспект выполненных действий, проверок, ошибок и источников.
   Исторический раздел9 оставлен с датой, а не выдаётся за текущий HEAD.
9. Итог этапа и SHA документационного коммита фиксируются одноимённой записью
   в журналах обоих узлов. Сам SHA коммита AGENTS.md определяется Git history,
   чтобы избежать циклического вписывания собственного ещё не созданного SHA.
   После итоговой записи требуется readback всех четырёх journals и exact
   mirror SHA; фактическое завершение указывать только после этих операций.
10. Для этой задачи изменяется только документация. Новая сборка, тесты
    приложения, локальный SSH/SFTP suite и CI не нужны и не запускаются.

### 10.7. Что ещё не завершено

- Приведение транспорта/Deploy к исходной модели и коду CSQTTR2.5.3.
- Реальные exec установщика, install/update/uninstall, результат/exit,
  независимый postcheck и интеграция с controller/UI. Stage-only это не заменяет.
- Отдельная проверка полного авторизованного VK/TURN.
- Подтверждённая приёмка прежде изменения production pins/пакетов.
- Каждое следующее действие и его результат добавлять в этот файл и обе пары
  журналов по разделу8; доступность одного узла не подразумевает запись на другом.

### 10.8. CSQTT_CORE_UPSTREAM_DEPLOY_PREPARE_20261126

- Владелец: повторять модель и код оригинального CSQTTR 2.5.3 настолько,
  насколько позволяют Linux ARM64 и существующие ограничения изоляции.
  Начальный HEAD d1bed6392be154e4b41d87f5c884c5d23e68c92d; локальная
  и удалённая ветка test/csqtt-deploy-authenticated-sftp-20261126 совпали.
- Прочитаны оригинальные DeployOperations.kt: performDeploy,
  performUninstall, serverArchitectureForMachine, systemdEnvironmentValue,
  а также наш transport, update_config и pinned installer. Один read-only
  запрос завершился ошибкой кавычки shell; исправленный запрос прошёл.
- Оригинал проверяет uname -m перед выбором серверного ELF. Добавлен
  requireRemoteARM64: read-only проверка по текущему OpenSSH-транспорту
  с лимитом 10 секунд перед inventory и передачей в stage-only.
  Принимаются только aarch64/arm64; AMD64 и ARMv7 отклоняются, поскольку
  в нашей сборке закреплён только ARM64 ELF. Не подменять pinned assets.
- Новый upstream_prepare.go переносит systemdEnvironmentValue оригинала
  и локальную подготовку WEB env / main_password JSON по режиму:
  Install создаёт настройки; Update сохраняет проверенный existing env,
  overrides и DB (текущий pinned installer требует эти файлы, в отличие
  от оригинала); Uninstall не формирует секреты. Reinstall не принят.
  Это локальная логика, пока не подключённая к удалённому lifecycle.
- Тесты upstream_prepare_test.go: четыре новые группы PASS на Moscow
  Go1.27.3 race и ARM64 QEMU; go vet PASS. SSH architecture test использует
  synthetic executable, не сетевой SSH. Реальная передача/установка не
  запускалась. Старые тесты не повторялись. Offline runner требует новые
  группы; bash -n PASS, git diff --check PASS.
- Изменены main.go, remote_transport.go, upstream_prepare.go и его тест,
  offline runner и AGENTS.md. Без изменения production pins, установщика,
  controller/UI, сети и служб. Следующее: проверить соответствие остальных
  операций оригиналу и отдельно принять удалённый lifecycle в разрешённой
  среде. Не выдавать подготовку конфигурации за установку.

### 10.9. CSQTT_CORE_UPSTREAM_EXEC_RESULT_20261126

- Запрос владельца: продолжить адаптацию оригинального CSQTTR 2.5.3.
  Проверены clean local/remote HEAD cbb271926813d63a1ce9784280b19c2dec10166a,
  upstream 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806 и текущие исходники.
  Сопоставлены DeploySSHClient.execResult, критерии exit/markers и
  последовательность performUninstall.
- Новый upstream_exec_result.go адаптирует границу результата SSH для
  существующих read-only операций uname -m и bash -s inventory.
  Раздельные stdout/stderr, подтверждённый код завершения, неизвестный
  результат при SSH exit255, сигнале, таймауте или превышении лимита.
  Не принимает произвольную команду и не запускает установщик.
  requireRemoteARM64 и inventory используют этот общий механизм.
- Исправлена тестовая модель performUninstall: перед execute необходим
  этап передачи скрипта, как в оригинале, без бинарника и новых паролей.
  Добавлен сценарий отказа передачи скрипта удаления. Модель всё ещё
  in-memory, реальное удаление не подключено.
- Первичная компиляция обнаружила лишний import bytes, удалён.
  Новый overflow-тест затем выявил настоящий дефект: embedded bytes.Buffer
  продвигал ReadFrom, из-за чего os/exec io.Copy мог обходить bounded Write.
  Буфер сделан закрытым полем с явными Len/String/Write; превышение 64 KiB
  на каждый поток теперь отклоняется. Новые тесты покрывают exit0/20/255,
  overflow, timeout, запрет неизвестного действия, отмену и недопустимость
  использования stderr как доказательства inventory.
- После исправления 8 затронутых групп Go race PASS на Moscow и 8 групп
  ARM64 QEMU PASS; go vet, bash -n runner и git diff --check PASS.
  Логи /tmp/csqtt-upstream-exec-20261126-{race,arm64}.log.
  Новые группы включены в required offline CI. Остальные тесты и сетевые
  испытания не повторялись. Полный CI не запускался.
- Production pins, installer, UI, сеть и рабочие службы не менялись.
  Подтверждена адаптация read-only результата, а не полного execResult
  с real-time progress/remote mutation. Реальный install/update/uninstall
  и независимая lifecycle-приёмка остаются незавершёнными.

### 10.10. CSQTT_CORE_UPSTREAM_CHECK_FIRST_COMPLETE_20261126

- Запрос владельца: продолжать адаптацию CSQTTR 2.5.3. Проверены
  local/remote HEAD 4d0a49681499892dc6cab82188cfd7ff8682b092,
  upstream 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.
  Изучены ServerCheck.kt, performServerCheck, controller, UI, тесты.
- Выявлено расхождение: deployProgressSnapshot мог выставлять preflight
  при одном end и текстовом признаке завершения, даже если
  deployInventorySummary отклонял неполный или противоречивый отчёт.
  Новые regression assertions сначала FAIL на неизменённом controller.
- В adapt/headless_main.go статус preflight требует одновременно полного
  валидного inventory, единственного end, точного terminal notice и
  единственного isolation marker, без конфликтующих install/uninstall/
  success markers. Неоднозначные данные дают unknown, а ошибки — error.
  Inventory выдаётся только вместе с подтверждённым preflight.
  Production helper pin, installer, controller deployment permissions
  и UI не изменялись.
- В adapt/deploy_progress_linux_test.go добавлены проверки пропуска каждого
  обязательного поля, дублей, неизвестных записей, конфликтующих markers,
  подстрок вместо точных сообщений и формата действующего helper.
- Targeted controller tests 3/3 AMD64 и 3/3 ARM64 QEMU PASS, go vet PASS.
  Actual pinned ARM64 process contract 2.13s PASS на отдельном scratch
  controller, неизменённый helper SHA277e98735d6cb87222c9a908a47c193a8917d6535dd5c6958c9aba681356c731,
  synthetic SSH and credentials; никакого сетевого Deploy.
  Логи /tmp/csqtt-check-first-20261126-{red,green,arm64,process}.log.
  Полный CI, прочие тесты и сетевые SSH/SFTP проверки не повторялись.
- Это устранение дефекта реального API предварительной проверки по модели
  upstream, не реализация запуска установщика. Install/update/uninstall
  через удалённый transport и приёмка всё ещё отсутствуют. Рабочие
  службы, устройства и production configuration не изменялись.


### 10.11. 2026-11-26 — продолжение полной адаптации DeployManager (этап в работе)

- Проверены clean local/remote HEAD f85fb8bdc301476fb83b83badd39a10080e23d12 и рабочая ветка test/csqtt-deploy-authenticated-sftp-20261126 на Московском VPS. Прочитаны AGENTS.md, последние записи обоих журналов Амстердама, manager/controller/UI, pinned installer и существующий uninstall candidate.
- Сверена локальная копия оригинала Afteroid/csqttr: HEAD 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806. В запросе пользователя путь DeployManager.kt неточен: настоящий файл расположен app/src/main/java/com/csqtt/client/DeployManager.kt (без ui/legacy). GitHub fetch указанного пути вернул404; прочитан правильный файл из pinned checkout, а также DeployOperations.kt, SshTransportConnection.kt, ServerCheck.kt и deploy.sh. Это не отсутствие доступа к исходникам.
- Выявлены незавершённые части: lifecycle_contract.go остаётся симуляцией; controller принимает только старый inventory helper; installer не имеет update; uninstall confirmation существует лишь в отдельном воспроизводимом candidate. Существующие защитные gates и pins сохраняются. Production/NanoPi не изменяются.
- Первый перенос: общий execResult с раздельными ограниченными stdout/stderr и потоковым callback, подтверждённым exit status, без повторного запуска команд при обрыве. Существующие read-only операции используют этот же исполнитель. Сам по себе перенос не запускает установщик и не подтверждает приёмку lifecycle. Проверки и последующие изменения записываются далее.

- Подготовлен отдельный генератор lifecycle candidate поверх прежнего pinned uninstall candidate: update по 2.5.3 сохраняет существующие настройки на сервере, приватный SFTP workspace заменяет общие /tmp uploads, mutex flock сериализует операции. Канонический установщик не изменён. Добавлен targeted test фактической потоковой доставки execResult до завершения процесса. Live install не выполнялся.

- Добавлен реальный --isolated-lifecycle: connect/architecture, pinned assets, проверка target+machine-id+Debian/Ubuntu+QEMU/KVM, SQLite integrity и hashes настроек, SFTP, однократный exec install/update/uninstall, независимый postcheck, cleanup и только затем success marker. API не может задавать lab gate: endpoint/machine-id читаются из окружения оператора. Update не передаёт новые пароли. После неизвестного исхода команды workspace сохраняется до сверки; автоматического повторного запуска нет. Production mode по умолчанию остаётся inventory. Код пока не прошёл компиляцию/испытания.

- Интегрирован bridge контроллера: отдельный candidate digest (пока закрытый placeholder), lab-only capability, Action update/uninstall, timeout и освобождение busy. В controller enrichment обновление не получает новые пароли формы. Статус успеха требует точного terminal notice и единственного маркера операции. Добавлены целевые проверки scope, сохранности, неизвестного исхода и реального exec с synthetic SSH; это не live установка.

- В существующую вкладку Deploy добавлен выбор установки/обновления; сохранены семь вкладок, оформление и существующие модальные окна. Обновление использует только SSH-данные, новые прикладные пароли не запрашиваются. Удаление показывает тот же фактический прогресс. Подготовлен отдельный сборщик лабораторного пакета без запуска/развёртывания. Первые targeted Go race проверки execResult/JSON/SFTP protocol прошли (1.799s).

- 2026-11-27: пользователь подтвердил «Доделай задания». Первая ARM64-сборка готова: SHA256 ced2f35b00ebd5170de701c0f2681fa45fb33adafb2c91dae988f6d6835eaae2. Предыдущий PTY после нового сообщения недоступен; наличие artifact подтверждает прохождение последовательной команды tests/vet/build, но подробный вывод не сохранён. Для итогового отчёта дальнейшие проверки сохраняются в файлы. Исправлена совместимость Action с прежним inventory helper: поле исключается только для старого read-only протокола. Новый digest разрешён исключительно вместе с явной lab scope. Добавлены тесты этого контракта и отсутствия новых прикладных паролей при update. Проверен доступ к NanoPi-R3S-LTS и модель FriendlyElec NanoPi R3S LTS; записи f85fb8b в журналах уже присутствуют. Системный python3 без Playwright; найден ранее установленный /srv/development/csqtt-ui-venv, установки зависимостей не требуются. Один JS orchestration вызов отклонён синтаксисом до исполнения; исправлены кавычки, повтор записал изменения.

- Итоговый targeted прогон: manager race/vet/ARM64 QEMU и controller race/vet/ARM64 QEMU PASS; JavaScript syntax и diff check PASS. Логи /tmp/csqtt-lifecycle-validation-20261127-jev84eq6. Static panel contract обнаружил устаревшую проверку соседних скобок после нового conditional update; обновлён контракт с сохранением проверки обязательных install credentials. Browser не стартовал из-за ожидания отсутствующего Chromium1254; ранее установлен Chromium1194, следующий запуск использует его явный путь без установки пакетов. При review закрыт пропуск symlink в исключённых из hash файлах SQLite/overrides; это изменяет helper и требует нового SHA. Нормализация action сохраняет отклонение дублирующихся JSON-полей. Добавлен функциональный тест prepare_uploaded_release: копирование прежних паролей, сохранение SQLite и отказ при symlink/отсутствующей базе, без установки службы.

- Новый helper после усиления path checks: SHA256 03d312495b9382d1c61966ae14276d947b346d83e8514a62671546ccd3ff4bc1; пакет /tmp/csqtt-deploy-lifecycle-20261127-candidate-2. Candidate installer SHA256 3ef1b3977d289e4bd878fe9ad87395abf30345c9b54c784bf69876ed33c43529,134626 bytes. Три функциональные проверки candidate preparation PASS (включая закрытый mutation gate для update); исправленный static panel contract PASS. Сквозная проверка actual ARM64 manager + локальный SFTP subsystem, с simulated SSH commands, прошла5/5: install/update/uninstall, preservation failure, verified rollback; журнал /tmp/csqtt-lifecycle-process-20261127.log. Installer main ни разу не исполнялся с открытым gate. Browser дошёл до update и завершил операцию через fixture, затем упал из-за чтения строкового post_data как dict в новом тесте; тест исправлен json.loads. Проверены GitHub Actions: последний run45796135833 SUCCESS относится только к ff7f598; новых CI пока нет. Проверен существующий QEMU lab: active, единственный SSH endpoint127.0.0.1:22239. Текущий validTarget намеренно не допускает loopback; исключение и сетевой доступ к guest не создавались, ранее отклонённые live действия не обходились. Полная реальная lifecycle-приёмка остаётся открытой.

- Финальные результаты: Chromium UI PASS для390/768/1280/1440/1920, включая новый update без прикладных паролей. Direct manager process fixture5/5 PASS; затем authenticated HTTP API→controller→pinned ARM64 helper→реальный stdio SFTP с simulated SSH commands5/5 PASS. Последняя ARM64 test-сборка контроллера включает новый helper pin и панель. Bridge -race после normalization fix5/5 PASS; первый запуск вне Go-модуля завершился ошибкой окружения, исправлен cwd и повторён только затронутый набор. Python syntax PASS. Основной прогон: manager17/17 native-race и17/17 ARM64 QEMU; controller+bridge9/9 native-race, controller4/4 ARM64 QEMU; vet PASS. Все ограничения live acceptance сохранены. Подробный итог и полный перечень файлов записаны в adapt/CSQTT-DEPLOY-LIFECYCLE-2026-11-27.md. Текущий конспект уже внесён в четыре удалённых журнала с readback SHA1e8c8c2cbefc8dd45e163a01cacf1dca2bd3cc78fac2447f998f112bbbf7e57e; итоговые результаты и SHA коммитов будут дописаны отдельно. У пользователя асинхронно запрошен допустимый isolated ARM64 endpoint; известный loopback стенд не допускается текущими guards.

- 2026-11-27, LAB-ENDPOINT-AUDIT: получен ответ владельца — использовать отдельный изолированный Debian ARM64 QEMU-стенд на Moscow, проверить собственный доступный IP/SSH без отключения guards; если среду организовать невозможно, завершить доступную разработку/испытания и опубликовать ветку. Повторно прочитаны постоянные инструкции AGENTS.md, отчёт этапа, service metadata, состояние ветки и remote HEAD: оба f85fb8bdc301476fb83b83badd39a10080e23d12, ветка test/csqtt-deploy-authenticated-sftp-20261126. Read-only проверки ip -j address, ip route, free -m, capability metadata, QEMU-файлов и tailscale status показали только собственные host IP200.165.226.52/100.71.202.95, около1374MiB доступной RAM, отсутствие отдельного lab peer. Через существующий ключ и strict known-host SSH прочитано состояние csqtt-deploy-lab: Debian13.8/aarch64/QEMU; eth0=10.0.2.15, hostfwd127.0.0.1:22239→22; tailscale/tailscaled отсутствуют; python3/sshd/flock/curl есть, standalone sqlite3 отсутствует; root authorized_keys есть, PermitRootLogin without-password. Прежняя csqtt-core.service активна, UDP47000/TCP47002 заняты ею, csqtt-core0 существует. Это не новый Deploy и не чистый baseline. Дополнительный маршрутизируемый IP и авторизация регистрации отдельного tailnet guest недоступны; endpoint, удовлетворяющий текущим guards, в имеющейся конфигурации не организован. Guard исключения, подмена адреса, новые forwarding/firewall/маршруты, установка/удаление службы и изменение диска guest не выполнялись. Точные требования к отдельной VM, адресу/SSH/identity, чистому snapshot, проверке update/SQLite и откату добавлены в adapt/CSQTT-DEPLOY-LIFECYCLE-2026-11-27.md. Применяется разрешённый владельцем запасной вариант публикации кода с открытой live acceptance. Проверка исторических записей об отказах не выявила нового отказа инструмента на этом этапе; ранее отклонённые операции не повторялись альтернативным каналом.

- 2026-11-27, PRE-COMMIT: итоговое дерево содержит22 целевых файла. Перечитаны runtime lifecycle и сохранённые logs: manager17/17 race и17/17 ARM64 QEMU, controller/bridge9/9 race (финальный bridge5/5), controller4/4 ARM64, два process fixture набора по5/5, candidate3/3 и Chromium5 размеров PASS. Это чтение доказательств, повторных запусков не было. git diff --check PASS; SHA lifecycle installer3ef1b397... и helper03d312495... сверены с собранным пакетом. Ошибочно предположенное имя DeployManager не существует; фактический helper определён по содержимому пакета и SHA, отсутствие файла с таким именем не является ошибкой сборки. Публикация только рабочей ветки, без main/release/production. Коммит помечен [skip ci]: выполнены адресные VPS-проверки, старые SSH/SFTP испытания и широкие CI заново не запускаются; новый GitHub CI не заявляется.

- 2026-11-27, IMPLEMENTATION-PUBLISHED: commit838141fbd7c2441f09aa87d5880c828fffe61902 создан из22 целевых файлов (1691 insertions/158 deletions); git diff --cached --check PASS, фактический файл deploy-manager имеет ожидаемый SHA03d312495b9382d1c61966ae14276d947b346d83e8514a62671546ccd3ff4bc1. Push origin HEAD:refs/heads/test/csqtt-deploy-authenticated-sftp-20261126 выполнен, git ls-remote подтвердил тот же SHA, рабочее дерево после code commit было чистым. Main, release, runtime и сеть рабочих узлов не менялись. Новый CI не запускался ([skip ci]); сохранённые результаты адресных VPS-проверок относятся к содержимому этого коммита. Следующий связанный шаг — итоговая синхронизация журналов и отдельный documentation commit.

- 2026-11-27, FINAL-JOURNAL-SYNC: итоговый блок CSQTT_CORE_DEPLOY_LIFECYCLE_FINAL_838141F (SHA256 6f4855002b76fe2ba869ffc21c81e986e70c753cd7c2f15725ea672f1d4f0e41) дописан в оба журнала Amsterdam и оба журнала NanoPi; содержимое каждого прочитано обратно и сверено. До записи сохранены .bak-lifecycle-838141f-final; NanoPi hostname/model повторно подтверждены. Истории узлов сохранены отдельно. Отдельные amsterdam-master зеркала обновлены побайтно: SUMMARY SHA92069a23cf742bb425f8ca128a5369d18c442ebdec078f744097eb1d024c96b8, JOURNAL SHA2f9c7b04b7f68720af111721fde46fa0ccf5b21667df5d954019f2cd48906c3e совпадают с Amsterdam на момент этой записи. Через GitHub API после push подтверждено отсутствие нового workflow run: единственный старый run45796135833 SUCCESS относится к ff7f598. В отчёт добавлены опубликованный implementation SHA и результаты синхронизации. Закрывающий documentation commit и его remote SHA фиксируются финальным коротким блоком всех четырёх журналов с обновлением зеркал; рекурсивные коммиты только ради собственного SHA не требуются правилом раздела8. Реальные install/update/uninstall нового helper остаются непроверенными; стенд требует собственного допустимого IP и чистого снимка.

### 10.12. 2026-11-27 — CSQTT_CORE_FINAL_OFFLINE_AUDIT_20261127

- Владелец поручил продолжить самостоятельно после публикации Codex.
  Подтверждены чистое дерево и совпадение HEAD/remote c12bda06185fa6370c7e14da7493fe414e7a9d4d
  рабочей ветки test/csqtt-deploy-authenticated-sftp-20261126.
- Прочитаны автономный runner и его workflow, перечень тестов и условия
  fixture. На Moscow выполнен весь существующий offline runner с Go1.27.3,
  без внешних fixtures и с выключенными lifecycle scope variables.
  Результат: 29 групп PASS, 0 FAIL, 7 штатных SKIP; race, vet, Linux ARM64
  compile PASS. Лог /tmp/csqtt-final-offline-audit-20261127.V6x2qr.log.
  Это первый полный offline прогон финального кандидата, а не live acceptance.
  GitHub Actions нового SHA не запускался; local compile не заменяет
  native ARM64 CI или реальный remote lifecycle.
- Read-only проверка ресурсов: около 1084 MiB доступной RAM, 3.1 GiB
  свободного диска, /tmp 806 MiB свободно. Служба существующего QEMU lab
  активна, SSH по-прежнему слушает только 127.0.0.1:22239. Tailnet inventory
  содержит три рабочих узла, отдельный lab peer не подтверждён. Новый
  разрешённый endpoint и чистый снимок не предоставлены. Не создавались
  новые VM/сети, не менялись маршруты, firewall, существующая лабораторная
  или рабочая установка. Не утверждается, что памяти/диска заведомо
  достаточно для ещё одного гостя.
- Изменения только документации, не production pin или исходный код.
  Следующий необходимый этап — отдельная штатно авторизованная VM с
  подтверждённой адресацией и чистым baseline; настоящий install/update/
  uninstall/rollback остаётся непроверенным. Не повторять локальные тесты
  вместо этой приёмки. Журналы синхронизировать по разделу8.

### 10.13. 2026-11-27 — CSQTT_CORE_MOSCOW_DISK_CLEANUP_20261127

- Владелец поручил освободить место на Moscow и продолжить Deploy.
  Проверены hostname, df/free, du системных каталогов и worktrees,
  процессы сборки, состояние лабораторной VM, текущий Git HEAD.
- Удалён только воспроизводимый GOCACHE штатным go clean -cache -testcache
  после проверки пути /root/.cache/go-build и отсутствия активной Go-сборки.
  Свободное место: 3248173056 -> 5085995008 bytes (+1837821952 bytes).
  Заполнение 93% -> 88%. Сохранены зависимости, Chromium, исходники,
  журналы, архивы, QCOW2 и рабочие службы. Никакие пакеты не удалялись.
- SHA256 сохранённого ARM64 lifecycle helper совпадает с pin:
  03d312495b9382d1c61966ae14276d947b346d83e8514a62671546ccd3ff4bc1.
  Существующая csqtt-core-deploy-lab-20261125.service активна.
- Повторный read-only аудит: около 1145 MiB доступной RAM, SSH гостя
  по-прежнему только 127.0.0.1:22239, отдельного lab peer нет;
  отдельный допустимый endpoint и чистый baseline не подтверждены.
  Удаление кэша не решает адресацию. Не запускались мутации гостя,
  создание сетей/VM, изменение проверок, install/update/uninstall.
- Изменяется только документация. Реальная приёмка остаётся открытой.
  Полный offline runner уже PASS в 10.12, без необходимости не повторялся.

### 10.14. 2026-11-27 — CSQTT_CORE_SERVER_ARCHITECTURE_CATALOGUE

- Владелец потребовал поддержки всех трёх серверных архитектур оригинала.
  Проверены clean HEAD f89fe662914c0c52853a07e0d8e77807ba0fb38d и
  upstream 84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806, DeployOperations.kt
  181–201, текущие asset pins, lifecycle и build scripts.
- Добавлены server_architecture.go и тесты: AMD64/ARM64/ARMv7, все
  upstream aliases, сопоставление имён Core8, ELF class/e_machine/endianness,
  отклонение неоднозначного вывода, неизвестной архитектуры и чужого ELF.
  Существующий requireARM64Machine и verifiedAssets используют общий
  каталог, но сохраняют ARM64-only execution gate и точный исходный pin.
  AMD64/ARMv7 не считаются утверждёнными пакетами; формат ELF сам по себе
  не доказывает происхождение, ABI или работоспособность сервера.
- 5 целевых групп race PASS, 5 групп ARM64 QEMU PASS, go vet, bash -n,
  git diff --check PASS. ELF fixtures являются инертными заголовками.
  Логи /tmp/csqtt-architecture-catalogue-20261127{,-arm64}.log.
  Новые три группы включены в required offline runner.
- На Moscow найдены Rust x86_64/aarch64 targets и gcc, но ARMv7 target
  и arm-linux-gnueabihf-gcc не обнаружены. Новых серверных ядер не собрано,
  не передано и не запущено. Сохранены прежние installer/helper pins,
  ownership, scope, сеть и службы. Изменённый helper ещё не собран
  в эксплуатационный пакет и не прошёл live acceptance.
- Следующий этап: воспроизводимые серверные сборки из тех же адаптированных
  Core8-исходников, отдельная проверка ABI/зависимостей и SHA каждого
  бинарника, затем согласованная интеграция пакетов с installer/lifecycle.
  Поддержка исполнения всех трёх архитектур пока не завершена.

### 10.15. 2026-11-27 — CSQTT_CORE_AMD64_BUILD_CANDIDATE

- На Moscow проверены исходники .csqtt-core-unified-253-final-with-47005-20261122
  через FINAL/tree_digest из derive-csqtt-core-unified-linux-253.py:
  все три дерева совпали, rust-server SHA 2c03df9581e87fe9e1e9ab0baaa7549dadaa8d2c32f5f62cb180193de2cb605a.
  Rust/Cargo 1.97.1, offline locked metadata PASS.
- Отдельный target: /srv/development/isolated-checkouts/.csqtt-core8-amd64-candidate-20261127/target.
  Сборка cargo +1.97.1 build --locked --offline --release --target
  x86_64-unknown-linux-gnu --jobs 1; первые два лимита 160/300 секунд
  не хватили, второй exit 124. Кэш сохранён; при обнаружении работающего
  Cargo не запускали конкурирующий процесс. Финальный повтор с лимитом 900
  секунд завершился, журнал build.log: Finished release in 2m17s.
- Готовый ELF target/x86_64-unknown-linux-gnu/release/csqtt:
  11378352 bytes, ELF64 little-endian x86-64 PIE, SHA256
  56435cc0e443b2e909064cdcf5add390369fa0e317be51d74c60119f336c1f31.
  readelf: libstdc++.so.6, libc.so.6, libgcc_s.so.1, libm.so.6;
  GLIBC_2.39, CXXABI_1.3.7, GCC_4.2.0. Amsterdam glibc 2.43,
  CXXABI до 1.3.17, GCC до 14.0.0: предварительная статическая
  проверка совместимости, не live runtime. Порты 47000/47002 без
  слушателей в host namespace на момент проверки.
- Источник и прежний ARM64 pin 8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d
  повторно проверены без изменений. AMD64 не запускался, не передавался,
  не добавлен в approvedServerAsset. Нет live acceptance или production promotion.
- ARMv7: QEMU-arm присутствует, Rust target отсутствует, apt-cache и
  apt-get -s подтвердили наличие gcc/g++ arm-linux-gnueabihf и armhf sysroot.
  Пакеты не установлены, сборка ARMv7 ещё не начата. Рабочие службы
  не изменены. Следующий этап — подготовить ARMv7 toolchain, проверить
  сборку и затем отдельно провести приёмку новых серверных архитектур.

### 10.16. 2026-11-27 — CSQTT_CORE_ARMV7_TOOLCHAIN_BUILD_START

- Проверены clean HEAD/remote 84a0aa93c7a2a8e2c46ca9a39ec425244925d1b1,
  исходные SHA ARM64/AMD64, ресурсы Moscow (4.2 GiB disk available,
  1689 MiB RAM available). apt-get -s: 19 new packages, 0 upgrades/removals.
- По запросу владельца установлен только cross-toolchain через apt-get
  --no-install-recommends install gcc-arm-linux-gnueabihf
  g++-arm-linux-gnueabihf libc6-dev-armhf-cross; exit 0, GCC/G++ 14.2.0,
  журнал /tmp/csqtt-armv7-toolchain-20261127.log.
- rustup target add armv7-unknown-linux-gnueabihf --toolchain 1.97.1:
  PASS; установлены все три Rust target. Скомпилирован инертный C ABI
  probe (sizeof(void*) == 4), readelf ELF32 little-endian ARM,
  EABI5 hard-float, qemu-arm -L /usr/arm-linux-gnueabihf PASS;
  временный probe удалён. Это проверка toolchain, не runtime CSQTT.
- FINAL source hashes повторно совпали. В отдельном target
  /srv/development/isolated-checkouts/.csqtt-core8-armv7-candidate-20261127/target
  начата offline locked release ARMv7 сборка Rust1.97.1, jobs1,
  arm-linux-gnueabihf gcc/g++/ar, timeout900s. Повторный вызов отказался
  запускать второй Cargo; подтверждён PID 1349337, правильный cwd и
  build.log с продвижением компиляции зависимостей. На момент проверки
  в 14:09:56 MSK финального ELF ещё нет. Статус не считать успешной сборкой.
- Не изменены pins, установщик, рабочие приложения, VM или маршруты.
  Свободно около 3.7 GiB на момент проверки. Следующий этап:
  проверить фактический exit, ELF ABI/зависимости/SHA; не включать
  непроверенный артефакт в production.

### 10.17. 2026-11-27 — CSQTT_CORE_ARMV7_BUILD_CANDIDATE

- Проверен build.log в .csqtt-core8-armv7-candidate-20261127:
  offline locked release Rust1.97.1 завершён за 8m18s, last-build-exit=0.
  В 14:12:32 MSK ещё шла компиляция зависимостей; в 14:27:22 подтверждён
  готовый ELF. Размер 7876704 bytes, SHA256
  718fdcd3a899205e9ec66041203939c16e9101ece7134d2c0b65f28e239a1cc4.
  ELF32 little-endian ARM EABI5 hard-float, dynamic PIE,
  interpreter /lib/ld-linux-armhf.so.3, NEEDED libstdc++.so.6,
  libc.so.6, libgcc_s.so.1, libm.so.6.
- Повторно проверены FINAL tree hashes и SHA всех трёх бинарников.
  Сборки AMD64/ARM64/ARMv7 существуют; наличие ELF и успешная сборка
  не означают runtime acceptance. ARMv7 и AMD64 остаются кандидатами,
  не добавлены в approvedServerAsset. Не выполнялись установка,
  передача, запуск сервера или изменение рабочих служб/сетей.
- Свободно 3.4 GiB на Moscow. Следующий этап: отдельные проверки
  совместимости и приёмка кандидатов, затем интеграция в пакет.
  Действующие ARM64-only gates и pins сохранены.

### 10.18. 2026-11-27 — CSQTT_CORE_THREE_ARCH_STATIC_ABI

- Moscow clean HEAD/remote 202111fef3045cc779ecf252e4945ac91602e9de.
  Rechecked sizes, SHA256, ELF headers, interpreters and dependencies of all
  three exact candidates; added read-only adapt/audit-core8-server-abi.py.
  Audit checks exact reviewed candidate size/hash, class/machine/endianness,
  interpreter, dynamic dependency set, absence of RPATH/RUNPATH/TEXTREL,
  ARMv7 EABI hard-float, v7/VFP/NEON attributes and per-library version
  requirements against version definitions in matching local build sysroots.
- Actual static audit: AMD64 27, ARM64 16, ARMv7 24 required library
  version nodes present. JSONL /tmp/csqtt-three-architecture-abi-20261127.jsonl.
  Negative checks 5/5 (wrong architecture/size, truncated input, symlink,
  missing provider, provider wrong architecture). No candidate executed.
  ARMv7 requires ARMv7-A hard-float/NEON-capable environment; AMD64 and
  ARMv7 candidates require GLIBC_2.39. Version-node check is not full
  symbol relocation or execution proof; not a claim of universal Debian/Ubuntu
  compatibility. No production pins, execution gates, installer or services
  changed. No real remote deployment or live acceptance. Next: separate
  authorized runtime acceptance and package integration after acceptance.

### 10.19. 2026-11-27 — CSQTT_CORE_MULTIARCH_LOCAL_CLI

- Moscow lab inventory: free disk 3.4 GiB, available RAM 1819 MiB.
  qemu-system-aarch64/arm and qemu-aarch64/arm present;
  qemu-system-x86_64 and /dev/kvm absent. Existing ARM64 deploy VM and
  tunnel active; two QEMU processes (CSQTT and unrelated VM) untouched.
  No clean, separately authorized three-architecture deployment lab established.
- Rechecked FINAL source tree and SHA256 of all three candidates; PASS.
  Existing offline Go architecture/integrity/remote-architecture fixture
  test groups 6/6 PASS. Mutation scope variables unset.
- Reviewed exact Rust main.rs CLI branches: --help and --protocol-revision
  return before root check and service startup. Local namespace probe:
  no routes, CapEff/CapBnd zero, NoNewPrivs=1. Tested only these two flags
  using timeout15s, separate user/network namespace, stripped capabilities,
  clean environment; no server mode, no transfer or service changes.
- Actual AMD64 native and ARMv7 QEMU-user CLI PASS: CSQTT-WIRE-3,
  help shows UDP47000, HTTPS47002, /etc/csqtt-core. ARM64 CLI attempt
  exited 1 with no result; pinned package mode0600 observed, exact cause
  unconfirmed. Did not modify permissions or retry via another route.
  Previous ARM64 evidence remains distinct from this attempt.
- CLI values are not listening-port or data-plane evidence. No real
  install/update/uninstall, remote acceptance, live VK traffic, or production
  promotion. No operational changes to existing services, pins or guards.
  Next: authorized isolated runtime acceptance; no claim that three
  full VM targets are available. Do not remove existing gates.

### 10.20. 2026-11-27 — CSQTT_CORE_MULTIARCH_LAB_PREP

- По запросу владельца подготовлены отдельные заготовки стендов на Moscow,
  без изменения существующих CSQTT/HydraRoute VM. Исходный HEAD/remote
  26f5debf33d510e8d773057ab0a5db983d35780a; clean. Изначально 3.4 GiB
  disk free, 1816 MiB RAM available, /dev/kvm отсутствует. QEMU ARM
  уже был установлен, x86 отсутствовал. apt simulation: 3 new packages,
  zero upgrades/removals; установлен qemu-system-x86 10.0.13 с seabios
  и ipxe-qemu; журнал /tmp/csqtt-qemu-x86-prep-20261127.log. Все три
  эмулятора QEMU 10.0.13 доступны. Existing lab PIDs 1057321/1338051
  сохранились; unrelated QEMU PID1230655 не затронут. Старый локальный
  HTTP47084 недоступен (HTTP000); не восстанавливался в этом этапе.
- Отдельный каталог /srv/development/isolated-checkouts/csqtt-multiarch-lab-prep-20261127
  с amd64/arm64/armv7. Загружены из Debian trixie APT (download only)
  debian-installer-13-netboot-{amd64,arm64,armhf}, version 20250803+deb13u8,
  сверены SHA256 из APT metadata:
  amd64 00ff9a603bb4938411510a69e261c4ce4dda2ec7709f6f8e40457cceb8104c70;
  arm64 7c182987f0288aa6fd7c605b17c043ce19738b0a7af2ebd6192a91833f32cf36;
  armhf a1ab085e369bcf1959a0963788708bc7b3f0fe5f881e4c5a8fd9dca23091ecb0.
  APT warning unsandboxed download due private root dir; SHA checks PASS.
  Извлечены только text installer kernel/initrd.gz; первый strip-components=9
  оставил подкаталог amd64 и проверка отказала; исправлено перемещением двух
  файлов в boot и strip-components=10 для остальных. Все файлы проверены.
- Созданы три новых sparse QCOW2 empty.qcow2 3 GiB virtual /196 KiB
  initial allocated, qemu-img check PASS 3/3. QMP preflight 3/3 with
  machine q35/max, virt/cortex-a57, virt/cortex-a15 respectively,
  TCG 512 MiB/1 vCPU, -S paused, no NIC, read-only disk, official
  installer kernel/initrd, query-status and quit. No guest boot, OS
  installation, network forwarding, SSH credentials, service or payload
  staged. Logs each arch/qmp-preflight.log; exact file hashes/size/state
  saved PREP-INVENTORY.json. No old VM disk reused or overwritten.
- Free disk 2.9 GiB after prep (~474 MiB media + disk stubs), insufficient
  to assume three populated 3 GiB disks fit. Plan sequential use, preserve
  sufficient host free space. Full labs require clean OS with separate
  authorized identity/connection, acceptance and sufficient storage.
  QMP proof is configuration only, not boot or runtime/Deploy acceptance.
  Existing mutation scope, ARM64-only execution gate and pins unchanged.

### 10.21. 2026-11-27 — CSQTT_CORE_OLD_BACKUPS_CLEANUP

- По запросу владельца проверены старые резервные копии CSQTT на Moscow.
  Измерены размеры, даты, типы, точные пути; не читались секреты.
  Удалены только явно устаревшие backup dirs
  /srv/development/migrated-root/csqтt-backup-20261006 (69287 logical bytes)
  и /srv/development/migrated-root/lalune-csqtt-backup-20261007 (10336176 bytes).
  Проверены отсутствие symlinks/mountpoints, точные realpath, затем
  rm -rf --one-file-system с двумя явными путями и readback отсутствия.
  Доступно до 3011092480, после 3021520896 bytes, прирост 10428416 bytes.
- Снимок snapshot-pre-upgrade-20261117 (994 MiB) сохранён: точка
  восстановления существующей VM, более новая равноценная копия не
  подтверждена. Исходники, актуальные сборки, компиляторные кэши,
  журналы, остальные проекты и диски VM не удалены. CSQTT lab/tunnel
  активны с прежними PID1057321/1338051. Свободно ~2.9 GiB.
  Установка Debian в новые VM не начата; очистка старых backup dirs
  практически не решает дефицит диска. Следующий безопасный вариант —
  отдельно проверить воспроизводимые промежуточные build caches.

### 10.22. 2026-11-27 — CSQTT_CORE_CARGO_INTERMEDIATES_CLEANUP

- Владелец запросил удаление крупного объёма данных. На Moscow проверены
  размеры Cargo target AMD64 (430 MiB), ARMv7 (397 MiB), ARM64 (850 MiB),
  отсутствие активных cargo/rustc, точные пути и SHA готовых ядер.
- Удалены исключительно воспроизводимые подкаталоги deps, build,
  .fingerprint, incremental, examples в host/target release трёх
  явно указанных Cargo target trees. Проверены realpath, отсутствие
  mounts (в том числе вложенных), сохранение готового csqtt вне путей
  удаления. Исходники, toolchain, Cargo registry, готовые ядра, журналы,
  VM/snapshots и другие проекты не удалены.
- После очистки SHA всех трёх финальных бинарников повторно совпали.
  Доступно до 3021463552, после 4748787712 bytes; прирост 1727324160 bytes
  (~1.61 GiB). df -h: 4.5 GiB available, 89% used. CSQTT VM и tunnel
  active, прежние PID1057321/1338051. Повторная компиляция потребует
  восстановления промежуточного Cargo cache; новых испытаний или
  установки гостевых ОС не проводилось.

### 10.23. 2026-11-27 — CSQTT_AMD64_DEBIAN_INSTALLER_BOOT

- Owner requested actual Debian installer startup. Moscow disk free
  4.5 GiB, RAM available 1604 MiB. Verified exact installer kernel/initrd
  SHA256 from prep and new empty QCOW2. Launched generic Debian 13 AMD64
  installer in separate QEMU TCG VM (q35/max, 512 MiB, 1 vCPU, no NIC,
  no host port forwarding, private Unix console/QMP sockets, dedicated
  writable 3 GiB disk). PID1355668, QMP query-status running=true.
  Serial log confirms kernel startup and Run /init as init process.
- Directory /srv/development/isolated-checkouts/csqtt-multiarch-lab-prep-20261127/amd64,
  installer-console.log, installer.pid, installer-qmp.sock and
  installer-console.sock. No CSQTT payload, SSH credentials, or guest
  network configured. Netboot media needs package source to complete
  OS installation; not a completed guest or accepted deployment target.
  Existing CSQTT lab/tunnel unchanged PID1057321/1338051 active.

### 10.24. 2026-11-27 — CSQTT_DEPLOY_MANAGER_REGRESSION

- Owner requested routine DeployManager verification. Moscow HEAD
  33eb440676f347f68165d04bd9e6c1cf62c85320 matched origin, clean.
  Verified pinned local assets in /tmp/csqtt-deploy-manager-core-assets-ci-20261125
  (installer SHA18b8fba... and ARM64 SHA8cd789e...).
- Actual Go1.27.3 native test -race -count=1 -timeout=100s -v:
  36 top-level PASS, 0 FAIL, 3 SKIP, exit0; package time3.926s.
  Local authenticated SFTP fixture used ephemeral keys and stdio,
  no network listener. Lifecycle install/update/remove/rollback,
  cancellation, malformed results and architecture tests passed
  using mocks/inert fixtures. go vet PASS.
  Log /tmp/csqtt-deploy-manager-regression-20261127.log.
- Skipped recorded ARM64 inventory fixture, real remote transport and
  remote lost-session cleanup tests (no corresponding environment
  parameters). No live deployment or server execution performed.
  Architecture catalogue recognizes all three, approved payload
  and live install path remain ARM64-only. No production changes.

### 10.25. 2026-11-27 — CSQTT_DEPLOY_CONTROLLER_REGRESSION

- Continued routine local controller testing. Verified relevant controller
  source/tests unchanged since 838141fbd7c2441f09aa87d5880c828fffe61902.
  Existing ARM64 controller test binary under candidate-2 run with QEMU-user.
- 3/3 progress, exact result marker and complete inventory report groups
  passed; 6/6 local synthetic credentials/API authorization and preservation
  groups passed (temporary config/home, synthetic credentials, httptest).
  Total 9 PASS, 0 FAIL for this selected run. Logs
  /tmp/csqtt-controller-progress-regression-20261127.log and
  /tmp/csqtt-controller-api-regression-20261127.log.
- No actual panel update, browser run, real remote installation or full
  controller-to-installer process acceptance in this step. Existing live
  services, deployment gates and pins unchanged. No production credentials used.

### 10.26. 2026-11-27 — CSQTT_DEPLOY_REMAINING_OFFLINE_CHECKS

- Relevant browser/controller sources unchanged since previously tested
  lifecycle code 838141f. Static panel contract PASS: 66 fixed buttons,
  7 tabs, 38 API paths. Reused recorded ARM64 inventory report
  /tmp/csqtt-deploy-vm-readonly-inventory-20261125.txt: previously skipped
  TestRecordedARM64GuestInventory passed Go race test, exit0.
- Added read-only browser regression adapt/test-csqtt-deploy-ui-status-offline.py.
  Chromium1194, all HTTP routes intercepted; ordinary UI clicks open
  historical status/progress, no POST or live controller. Four scenarios PASS:
  completed without confirmed installation -> neutral; running then unknown
  terminal status -> unknown/error UI; transient HTTP503 -> recovery without
  false success; persistent HTTP503 -> unknown outcome. Zero mutating HTTP
  requests assertion PASS. Initial temporary test erroneously called
  private JS function; corrected test to use UI buttons; production unchanged.
- Logs /tmp/csqtt-recorded-inventory-regression-20261127.log and
  /tmp/csqtt-deploy-ui-status-regression-20261127.log. Cumulative recent
  Go test groups 46 PASS (37 manager +9 controller), 0 FAIL, two remote
  tests still unrun. Browser scenarios counted separately. No actual
  remote deployment, live connection or panel update. Existing earlier
  full mock lifecycle/browser evidence not counted as new.

### 10.27. 2026-11-27 — CSQTT_DEPLOY_REMOTE_EVIDENCE_REVIEW

- Reconciled two opt-in network groups skipped in recent local regression
  with historical evidence in AGENTS section 10.3 and
  adapt/CSQTT-DEPLOY-INTEGRATION-REVIEW-2026-11-26.md. Recorded prior
  TestRemoteTransportRealEndpoint PASS24.93s, 266240 inert bytes,
  rename/readback and two-object staging; TestRemoteTransportLostSessionCleanup
  PASS6.38s, reconnect for cleanup and verified directory absence.
  Historical network was Termux ARM64 to Moscow, no actual installation.
- Compared b29c8c9e9add41cc52ba6e8de758bbdf83792537 to 7f2e0d3:
  remote_transport_test.go/staging_contract.go unchanged; remote_transport.go
  changes limited to requireRemoteARM64, inventory read-only result handling,
  and peer filename allowlist. Core SFTP transfer/cleanup code unchanged.
  Historical evidence not promoted to fresh HEAD network acceptance.
  Current host/key opt-in environment variables absent; no new remote SFTP
  test, credentials, deployment or guard changes. Current /tmp strict
  staging-directory count zero. AMD64 installer still running at locale
  prompt; no installed OS. Tailnet three existing hosts online.
- Recent regression remains 46 PASS, two network groups not rerun.
  Prior network evidence accounted for separately. Full live lifecycle
  and multiarchitecture package acceptance remain open.

### 10.28. 2026-11-27 — CSQTT_DEPLOY_ACCEPTANCE_RECONCILIATION

- Новый запрос: сверить CSQTTR2.5.3, архитектуры AMD64/ARM64/ARMv7, провести только недостающие допустимые проверки, подготовить отдельную нейтральную среду, сохранить службы Amsterdam46000/46002,46100/46102,48000/48002 и панель NanoPi. Начальный HEAD/remote5420d649cde69fcc8ea7d7d4ee1696b0470ec02a совпали, дерево чистое. Прочитаны инструкции AGENTS, история10.12–10.27, два журнала Amsterdam, каталог архитектур, ABI audit, lifecycle/transport/controller tests, кандидат установщика и original DeployOperations/DeployManager/SshTransportConnection на84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806. rg на Moscow отсутствует; первая попытка listing завершилась FileNotFoundError, чтение продолжено Path.glob без установки пакетов.
- Учтены ранее пройденные46 Go групп, static ABI трёх бинарников, AMD64 native/ARMv7 QEMU CLI, process/browser fixtures и исторические remote SFTP. Не повторены. В Amsterdam появился отдельный AMD64_NEUTRAL_DPKG_LIFECYCLE:8 assertions на инертном deb в отдельном dpkg-root; это не DeployManager и не автоматический rollback. Каталог трёх архитектур завершён, но approvedServerAsset и exec path сохраняют ARM64-only; новые AMD64/ARMv7 payload не приняты и не подключены.
- Владелец подтвердил отсутствие готовой VM/нового endpoint и разрешил подготовить отдельное окружение для нейтрального пакета. AMD64 installer PID1355668 остаётся на этапе Debian installer, NIC отсутствует. На Moscow доступны4.5GiB disk,806MiB /tmp и1282MiB RAM; cloud-localds/genisoimage/QEMU имеются, debootstrap/nspawn отсутствуют. Найден отдельный immutable Debian13 ARM64 base QCOW2(431423488bytes/3GiB virtual), без backing file/corrupt flag. Рассматривается новый copy-on-write ARM64 guest с собственной UEFI/seed, без NIC/forwarding/host mounts; старые VM не затрагиваются.
- При сверке performUninstall обнаружена избыточная зависимость от локального server ELF: upstream загружает только script, runRemoteLifecycle до action ветвления вызывает verifiedAssets для server+baseline. Будет выполнена новая адресная регрессия, затем исправление только подтверждённого случая. Проверки trust/identity/scope не снимаются.

- NEUTRAL-VM-PREP: создан новый csqtt-neutral-arm64-lab-20261127: отдельные COW overlay/UEFI/seed поверх неизменённого Debian13 ARM64 base,512MiB/1vCPU, NIC/forwarding/shared host mounts отсутствуют. MANIFEST.json содержит argv и SHA base/test. В репозиторий добавлен test-neutral-package-qemu-guest.py: запускается только root в QEMU/KVM с hostname csqtt-neutral-* и единственным lo, отказывается при Core runtime/config. Только инертные deb без maintainer scripts/исполняемого payload; настоящий dpkg и SQLite. Никакие Core pins/installer gates не подменяются. Это отдельные ОС/пакетные проверки, не remote DeployManager и не автоматический Core rollback. После результата guest выключается; успех загрузки/тестов ещё проверяется. Откат среды — остановить только новую VM и пересоздать её overlay из сохранённого base, старые VM/службы не затрагиваются.

- UNINSTALL-DEFECT: новый Test253UninstallWithoutReplacementPayload воспроизвёл2 FAIL до исправления (без server ELF; только candidate script); install/update без payload и corrupt-script отказали корректно. Точечное изменение runRemoteLifecycle: verifiedAssets требуется только install/update, uninstall продолжает проверять точный pinned lifecycle script, scope, SSH identity, ARM64, inventory/ownership и postcheck. SHA pins не ослаблены.7 затронутых групп native Go race PASS, новый тест ARM64 QEMU PASS, vet и новая ARM64 candidate сборка PASS. Логи в /srv/development/isolated-checkouts/csqtt-acceptance-evidence-20261127, red log /tmp/csqtt-uninstall-no-payload-red-20261127.log. Новый helper SHAcbcf09bc03e3d1189253ab7ec34cf53f2e2be5e2813623e999096790c0280914; controller lab pin ещё будет обновлён и проверен отдельно.

- ARM64-CLI-ISOLATED: для ранее незавершённого ARM64 CLI случая создана отдельная временная копия точного ELF SHA8cd789e... с mode0500; исходный asset mode/SHA не изменены. Единственные argv --protocol-revision/--help, рассмотренные ранние return в original Rust main; отдельные user/net/mount/pid namespaces, CapEff/CapBnd=0, NoNewPrivs, пустой env и отсутствие routes, timeout20s. Результат PASS: оба CLI, CSQTT-WIRE-3 и порты47000/47002. Временная копия удалена, серверный режим/передача/установка отсутствуют. Причина старого exit1 не объявляется доказанной; новый запуск отличается изолированной исполняемой копией, существующие gates не менялись. Лог /srv/development/isolated-checkouts/csqtt-acceptance-evidence-20261127/arm64-cli.json.

- HELPER-INTEGRATION: обновлён исключительно laboratory helper pin на cbcf09bc03e3d1189253ab7ec34cf53f2e2be5e2813623e999096790c0280914 после пересборки изменённого helper; прежний inventory pin277e9873... и server8cd789e.../installer3ef1b397... не менялись. Bridge5 групп race PASS, ARM64 controller test сборка PASS. Process fixture дополнен отсутствием replacement payload при uninstall и2 прерываниями exec: exit255 после прогресса и после неоднозначного success marker. Подтверждены один запуск команды, отсутствие ложного успеха и сохранение staging при неизвестном исходе (фикстура затем удаляет только собственные инертные файлы). Direct actual ARM64 helper и authenticated HTTP API→controller→helper прошли по7/7 сценариев; SSH commands simulated, SFTP stdio настоящий; реальный installer/SSH lifecycle не запускался.

- PROGRESS-DEFECT: дополнительная сверка execResult/progress с upstream numeric regex выявила NaN: Go ParseFloat принимает NaN, сравнения диапазона его не исключают. Новый Test253LifecycleProgressRejectsNonFinite воспроизвёл FAIL; добавлено math.IsNaN без изменения нормального числового прогресса, targeted race PASS (NaN/Inf/out-of-range/обычный0.5). Требуется финальная пересборка и обновление lab helper pin.
- NEUTRAL-VM-RESULT: настоящий offline Debian ARM64 QEMU guest выполнил6/6 dpkg/SQLite checks и штатно poweroff (console.log содержит machine=aarch64, только lo). SHA нейтральных deb1.0=1011bb67c8f479b33b089770fc90244c5e9f421975833be325af144edbbe4830,2.0=4e22e2f9d9a70d0a11fd8f57963d7164c7c59fd1c42f4e4296cb8ad709c93bfb. SQLite fixture вне package payload; dpkg её не мигрирует. Это реальное ОС-исполнение, но не Core installer/rollback/SSH. После результата preflight тестового guest script вынесен до записи result.json и заменён на явные проверки, действующие с python -O; проверка отказа на Moscow PASS без записи /var/lib/csqtt-neutral-lab. Успешный guest прогон относится к исходному SHA script в MANIFEST; после изменения guard повторены только отрицательные проверки host refusal, сами package steps не менялись.

- FINAL-CANDIDATE: после исправления NaN выполнены vet, ARM64 helper/controller test builds, новый progress regression под QEMU и полный authenticated API process fixture7/7 с окончательным pin285c8882a122ab146a1a89df488d1336ac9529b13856c5cc8ec028fb4fcb3d64 — PASS. Предыдущие неизменённые standalone тесты повторно не запускались. Финальный bundle /srv/development/isolated-checkouts/csqtt-acceptance-evidence-20261127/candidate-final; git diff --check PASS. API fixture остаётся simulated SSH commands/реальный local stdio SFTP, не remote install. Прежние inventory/server/installer pins неизменны.

- FINAL-COMPONENT-STATUS:
  - Полностью реализованы в разрешённой ARM64 lab области: SSH/SFTP authentication/host-key и integrity staging, inventory/identity/ownership/scope, последовательности install/update/uninstall, сохранение config/SQLite, execResult с неизвестным исходом без повтора, cleanup, bridge/API/progress/результат. Реальный полный lifecycle ещё не принят; это статус кода, не эксплуатации.
  - Каталог AMD64/ARM64/ARMv7 и static ELF/ABI/SHA проверены, все три CLI реально исполнены (AMD64 native, ARM64/ARMv7 QEMU-user). Целевые пакеты AMD64/ARMv7 собраны, но approvedServerAsset/installer/lifecycle integration остаются незавершёнными; требование полного Deploy этих архитектур FAIL.
  - Новые доказательства: uninstall red2 FAIL→5 subcases PASS;7 связанных manager race групп+1 progress race;5 bridge race;по1 новой ARM64 группе uninstall/progress;final API process7/7;ARM64 CLI2/2;neutral ARM64 full VM dpkg/SQLite6/6;host refusal script PASS. Исторические46 Go/browser/SFTP/ABI не пересчитаны как новые. В матрице разграничены native/QEMU, локальное исполнение, фикстуры и настоящий SSH.
  - Открыты обязательные A0–C1: реальный Core install/update/uninstall и automatic rollback/rollback_failed, обрыв SSH в разных стадиях и отсутствие повтора, реальная UI-интеграция, сохранность чужих ресурсов; все три архитектуры пока NOT TESTED по этим критериям. Нейтральный dpkg/ручной downgrade/отказ повреждённому пакету не доказывают автоматический rollback Core.
  - Подготовлены отдельные adapt/CSQTT-DEPLOY-ACCEPTANCE-MATRIX-2026-11-27.md, adapt/CSQTT-DEPLOY-ISOLATED-LAB-REQUIREMENTS.md и индекс evidence/deploy-acceptance-20261127.json. В runbook указаны собственный IP/SSH/root/host-key/machine-id, Debian/Ubuntu ABI(GLIBC_2.39), ARMv7 hard-float/NEON, зависимости/TUN, бюджет VM, чистый snapshot, fault injection и восстановление только лабораторного overlay. Новая offline VM без NIC не объявлена подходящим Deploy endpoint.
  - Новый ARM64 neutral guest завершился штатно, pidfile отсутствует; SHA base повторно совпал. Старые VM не менялись. На Amsterdam read-only подтверждены active PID1409/1649/1308 и порты46000/46002,46100/46102,48000/48002 внутри соответствующих network namespaces; host47000/47002 свободны. Панель NanoPi не менялась. Следующий шаг — commit/push только текущей ветки, сверка HEAD и синхронизация журналов.

- PRE-PUBLISH: подготовлены10 целевых файлов (два runtime fixes, lab pin, адресные regression fixtures, neutral guest scenario, матрица/runbook/evidence и AGENTS). Python AST/diff check PASS, SHA окончательного helper285c8882... совпал с controller lab pin. GitHub API подтверждает только старый SUCCESS45796135833 наff7f598; новый CI не заявляется. Commit будет [skip ci], поскольку связанные VPS-проверки выполнены и повторять прежние широкие suites без изменений не нужно. Публикация только test/csqtt-deploy-authenticated-sftp-20261126; без release/main/production.

- PUBLISHED-AND-SYNCED: code commit eb5c94e549761f2cdc809f109b554a83a0015de2 опубликован (10 файлов,704 insertions/9 deletions); remote HEAD и чистое дерево подтверждены. Подробный блок10.28, матрица и runbook доставлены в оба журнала Amsterdam и оба журнала NanoPi, прочитаны обратно; SHA блока3735de028fd92dc2f24fe0c6342527aecc4979ea18fcfca71c1e042beb7165a0. Первая попытка NanoPi дала Connection closed by UNKNOWN port65535; повторные read-only hostname и listener127.0.0.1:2226 подтвердили восстановленный доступ, повторён только неудавшийся перенос тем же каналом. Службы/туннель не перезапускались. NanoPi hostname/model подтверждены, backups и отдельные amsterdam-master зеркала побайтно сверены. Этот doc-only commit фиксирует результат; его SHA и завершающий remote/clean check дописываются короткой записью всех четырёх журналов. Тесты не повторяются ради документации.
