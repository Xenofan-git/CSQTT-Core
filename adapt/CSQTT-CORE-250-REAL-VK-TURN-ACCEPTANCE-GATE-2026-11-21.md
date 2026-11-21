# CSQTT Core 2.5.0 — реальный VK TURN: оставшаяся выпускная проверка

Дата: 21 ноября 2026 г.
Статус: НЕ PASS — необходимое разрешённое сетевое окружение отсутствует.

## Подтверждённые результаты

- Подлинный неизменённый Rust-клиент CSQTT 2.5.0, SHA-256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa, проверил VK Calls hash через настоящий VK API: GitHub Actions 44914600867 SUCCESS, один hash valid. Проверка не является подтверждением TURN-сеанса.
- Клиент 2.5.0 / Core №8 отдельно прошёл WIRE-3 с локальным синтетическим TURN. Выпускной Go supervisor подтвердил полные TCP и UDP через WIRE-3 в том же лабораторном стенде, но с синтетическими TURN credentials: CI 44912824079 SUCCESS.
- Исправленный Go TUN lifecycle/race и exact-ELF состав: CI 44912292825 SUCCESS. Обновление и атомарный откат: CI 44912627426 SUCCESS. Архив проверенных четырёх ELF: CI 44913844376 SUCCESS.

## Точный блокер

Отсутствует проверенный, разрешённый и публично достижимый входящий UDP endpoint с Core №8 WIRE-3 для реального VK TURN. Запуск публичного сервиса для проверки на московском VPS был заблокирован ограничениями доступа. Отдельный вызов живого VK TURN credential-acquisition с оригинальным клиентом также был заблокирован. Не обходить ограничения сменой порта, удалённым прокси, другой машиной или переносом VK секретов.

Одноразовая ARM64 QEMU VM остановлена, тестовый UDP порт 48779 закрыт. VM-диск, временная конфигурация и приватный SSH-ключ удалены. Рабочие NanoPi, Keenetic, M-UI, HRCore, Tailscale, маршруты, sysctl и DNS не изменены.

## Критерии последней приёмки на отдельно разрешённом стенде

1. Подтвердить разрешённый UDP endpoint, оригинальные SHA-256 ELF и протокол Core №8 CSQTT-WIRE-3.
2. Использовать существующий VK hash только как маскированный GitHub Actions secret, без сохранения hash/токенов/TURN credentials/URL или сырых ответов VK в артефактах.
3. Использовать неизменённый Rust ELF CSQTT 2.5.0, штатный vkcalls auth, реальный TURN credential response, Allocate/Permission/ChannelBind и успешный DTLS/SRTP.
4. Проверить GETCONF/TUNCONF, Go Linux TUN, CSQTT SOCKS5 и sing-box в едином сеансе, прямой обход должен быть недоступен.
5. Подтвердить HTTP-маркер через sing-box → Go SOCKS5 → WIRE-3 и UDP echo через ту же цепочку.
6. Подтвердить полную очистку после остановки, неизменность хостового default route, защиту секретов и отсутствие влияния на действующие сервисы.
7. Публиковать только статусные несекретные маркеры PASS/FAIL, номера CI и SHA-256; валидный VK hash отдельно не является успешным реальным TURN/WIRE-3 E2E.

## Защита конфиденциальных данных

В исходном Rust 2.5 HTTP-ошибки могут включать URL или часть ответа VK API. Нельзя публиковать сырые stdout/stderr клиента в CI; необходим приватный журнал с правами 0600 и внешняя выдача только заранее разрешённых статусов. Не переносить секреты на VPS или NanoPi.

Итог: реальный VK Calls hash VALID, тестовый WIRE-3 TCP/UDP PASS, но реальный VK-authenticated full-chain до Core №8 пока NOT VERIFIED. Продвижение в production не выполнялось.


## Дополнение: подготовлена безопасная проверка логов VK Calls

- Появился анализатор строго приватного Rust-журнала (adapt/csqtt_core_vkcalls_proof.py), который принимает только регулярный файл с правами 0600, ограничивает размер 2 МиБ, отвергает symlink и не печатает его исходные строки.
- Выводятся только заранее разрешённые диагностические маркеры последовательности VK Calls step1–step4 и наличие ненулевого количества TURN URL в том же stream. Это свидетельствует только о стадии получения TURN credentials; не доказывает установление TURN Allocate/DTLS/WIRE-3 и передачу TCP/UDP.
- Фиктивные, не содержащие настоящих секретов восемь тестов подтверждают, что токены/URL не раскрываются, порядок этапов не подменяется, события разных потоков не смешиваются, ошибки и небезопасные файлы отклоняются.
- GitHub Actions 44919137670 SUCCESS (commit c611834869350f6911c974aa4d0b23d3dc07c857). Тест был строго offline: реальную авторизацию, сетевой сервис и выпускной архив не запускали, существующий Rust 2.5 ELF не изменяли.
- Публично доступного, отдельно разрешённого UDP Core №8 на московском VPS сейчас нет; тестовый unit csqtt-250-live-vk-lab.service inactive, порт 48779 closed. Реальный сквозной VK/TURN всё ещё NOT VERIFIED.


## Сверка реального стенда и исключение старых серверов — 21 ноября 2026

- По уточнению пользователя действующие амстердамские CSQTT server services относятся к старым системам. Совпадение protocol revision WIRE-3 недостаточно: их бинарники не совпадают с Core №8 SHA-256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706. Стенд Амстердама не использовать и не изменять.
- На московском VPS бинарник нового Core №8 существует и SHA-256 совпадает; исходный файл имеет права 0644 (обычно исполняемый разрешён только после установки в гостя), никаких услуг не запускали.
- Одноразовая live VK VM уже полностью удалена; dedicated csqtt-250-live-vk-lab.service inactive, UDP 48779 не слушается. Три другие QEMU VM принадлежат другим лабораторным проверкам; не переиспользовать без отдельной подготовки.
- Ресурсы московского VPS на момент проверки: root filesystem 95% заполнен (около 2.3 GiB свободно), память available ~1185 MiB, swap used ~1904 MiB; работающие QEMU процессы потребляют в сумме ~2 GiB RSS. Запуск новой ARM64 VM может затронуть действующие стенды и требует отдельной ресурсной оценки. Никаких VM, портов, firewall, sysctl, сервера или маршрута не меняли.
- Внешний запуск и реальный TURN credential acquisition ранее были заблокированы ограничениями доступа. Не обходить их через альтернативные порты, другие VPS/CI среды, proxy или эксфильтрацию ключей. Статус итоговой реальной приёмки остаётся BLOCKED/NOT VERIFIED. Пройденные локальные ARM64, archive, proof и synthetic-TURN CI не повторять.


## Проверка локального Core №8 на отдельной ARM64 VM — 21 ноября 2026

- Пользователь самостоятельно перенёс оригинальный Core №8 в отдельную Debian 13.7 AArch64 QEMU VM на московском VPS. Проверены размер 11038072 байта, SHA-256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706 и ответ CSQTT-WIRE-3; исходный ELF не изменён.
- Внутри закрытой VM (QEMU user network restrict=on, на хосте только loopback SSH 127.0.0.1:22379, без проброса UDP) успешен кратковременный запуск оригинального Core №8 с тестовыми случайными паролями и приватным каталогом: сервер слушал UDP 127.0.0.1:46871, HTTPS на тестовом порту 46872; после SIGINT локальный UDP-порт освободился, гостевого IPv4 default route не появилось. Это **только локальный smoke**, не VK-authenticated E2E и не внешний доступ.
- Выявлен побочный эффект: сервер включил guest IPv4 forwarding. По исходнику из закреплённой ARM64 сборки rust-server/tokio_io.rs KernelNetworking::install выставляет ряд sysctl (IPv4/IPv6 forwarding, rp_filter, TCP и пр.), а Drop снимает управляемые firewall rules, но не восстанавливает sysctl. Этот эффект следует явно учитывать в плане изоляции. В госте IPv4 и IPv6 forwarding возвращены в 0; host IPv4 route и работающие NanoPi/Amsterdam службы не изменились. Значения прочих sysctl до первого запуска не были зафиксированы, утверждать их полное восстановление нельзя.
- Подготовлен lab-only adapt/verify-core8-private-guest-sysctl-restore.sh с жёсткими проверками гостя/архитектуры/hash/protocol, snapshot/restore перечня sysctl, приватным UDP bind, ограниченным временем выполнения и только временными учётными данными. Локальный bash -n PASS. **Динамический запуск этой новой обёртки был заблокирован инструментом доступа и НЕ ПРОВЕРЕН**. Не включать в production Deploy и не считать исправлением исходного ELF; безопасность автоматического восстановления пока не подтверждена.
- Реальный VK Calls hash preflight и синтетический WIRE-3 TCP/UDP от прежних CI остаются отдельными подтверждёнными фактами. Реальная цепочка VK TURN → оригинальный ELF → Core №8 → Go SOCKS5 → sing-box остаётся **NOT VERIFIED**. Не публиковать UDP-порт и не переводить архив в production.


## 2026-11-21 — offline mock acceptance of guest sysctl restore

- Existing lab-only wrapper adapt/verify-core8-private-guest-sysctl-restore.sh was hardened with explicit fail-closed test-port collision checks; an empty original-sysctl snapshot is rejected, symlinks are excluded, a missing/restored file is considered failure, and the original test exit code is retained when restoration itself succeeds.
- Added adapt/test-core8-guest-sysctl-restore-offline.py, which extracts the exact Bash snapshot/EXIT-trap logic from the real wrapper and runs it against disposable mock sysctl files, not /proc. Tests cover multiple changed values, original failed exit code, deleted target, symlink replacement, no-change case, and guest/private-only preflight assertions.
- Commit b65801abab0571316e26b755821763f77d15958c; GitHub Actions 44924250807 SUCCESS, six offline tests PASSED and bash -n PASSED. The test did not run Core8, start QEMU, call VK, or change kernel/network settings.
- IMPORTANT: The dynamic guard+Core8 test inside the actual ARM64 guest remains UNVERIFIED after a prior tool safety block. Real VK-authenticated TURN to Core8 WIRE-3 and Go SOCKS5/sing-box TCP+UDP remains NOT VERIFIED. The byte-identical ELF and final ARM64 ZIP remain unchanged; candidate still not for production.
