# CSQTT Core — результат проверки исходной версии 2.5.0

Дата: 21 ноября 2026 года. **Это аудит и тестирование, не изменение рабочего кода.**

## Источники

Оригинальный CSQTT 2.5.0: amurcanov/csqtt, тег v2.5.0,
коммит fd936ef087c34cc46f26111dc2361f43b8b03c07.

В ARM64 клиентский unit-test harness непосредственно включены неизменённые
rust-client/protocol.rs и shared/wire_protocol.rs из указанного тега;
для зависимости anyhow использован исходник 1.0.104, отдельно
скомпилированный с Rust 1.97.1. Данные тестовые файлы находятся
в временном каталоге VPS, не в рабочем репозитории.

Серверные тесты выполнялись из **уже существовавшего ARM64 тестового
ELF производной Rust-сборки 2.5.0** (с изоляционными изменениями и
добавленными тестами). Не выдавать эту сборку за побайтово оригинальный ELF.

Все динамические тесты проведены в пользовательском режиме QEMU ARM64
без VK, TUN, SSH, изменения iptables, systemd или системной маршрутизации.

## Выполненные тесты

| Проверка | Результат | Значение |
| --- | --- | --- |
| Клиент 2.5.0: настоящий исходный Rust-модуль protocol.rs и wire_protocol.rs под ARM64 | **18/18 PASS** | GETCONF, TUNCONF, NOCONF, DENIED:auth/protocol mismatch, управляющие ответы, восстановление фрагментов |
| Сервер 2.5.0: ARM64 unit-тесты из производного сервера | **7/7 PASS** | Расшифровка оригинального GETCONF fixture, верный/ошибочный секрет, создание/замена сессии, epoch, TUNCONF, защита от битовых повреждений |
| Оригинальный Deploy 2.5.0, статическая проверка | **PASS** | Подлинность deploy.sh, синтаксис Bash, preflight, SSH/secret path, install/uninstall |
| Изолированная адаптация Deploy 2.5.0, статическая проверка | **PASS** | Согласованность TUN csqtt-core0 и подсети 10.67.69.0/24 |
| Отдельный ARM64 release-сервер 2.5.0: команда --protocol-revision | **CSQTT-WIRE-3** | Встроенная версия сетевого протокола |
| Старая интегрированная сборка | **CSQTT-WIRE-4** | Прямая замена на сервер 2.5.0 пока не разрешена |

**25 динамических тестов прошли:** 18 оригинальных клиентских +
7 серверных. Нулевые подборки тестов не засчитывались. Количество
реально выполненных Rust-тестов подтверждено выводом test harness.

Серверные тесты:
- client_getconf_fixture_authenticates_with_matching_password_only
- client_fixture_creates_hot_session_and_control_event_through_engine
- client_fixture_with_wrong_server_key_cannot_create_a_hot_session
- getconf_epoch_parser_is_strict
- tunconf_padding_is_sized_for_audio_frames
- client_getconf_fixture_rejects_every_single_bit_wire_mutation
- duplicate_client_handshake_replaces_in_place_without_a_phantom_session

## Идентификаторы проверенного кода (SHA-256)

- Клиентский оригинальный protocol.rs: f7ea7747c9b2e36ee957c90ec60c5a510fb5c63a9086b790fba3f0a92e53cb27
- Общий оригинальный wire_protocol.rs: 7c563f960ec59f1f646c492fec6c370f9cbd533b8cb5c223e992fc362dd1f7ca
- Оригинальный серверный protocol.rs: 19b6926c5c14add5827a87f3f5661137b3bf9ac8e67fe45964747f21e42cb5ed
- ARM64 серверный unit-test ELF: 65c453d5d11be5f4254ab91e6156c4b694f943ac68c25ad3a89943d1a66c562e
- ARM64 клиентский protocol-test ELF: 48affd6ee9b038535732c3f4ff70c271a7b9aac6c592ea00cf416d8490543348
- Отдельный ARM64 серверный release ELF: 6c054d5a65205f7d23a7a6b55ce8d8d7128b0b8d3f3fe2db637d23450adae38e
- Оригинальный deploy.sh 2.5.0: 9a82d4ab5f12b0677b4e11be14ca892fb16c0a771eddb7ed5485d8957e9b9895

## Что НЕ подтверждено

1. Сквозное соединение **двух реально запущенных** процессов Rust
   client 2.5.0 ↔ Rust server 2.5.0 в ARM64 VM. Отдельного готового
   ARM64 client 2.5.0 executable в изолированном VPS не найдено.
   Проверен именно оригинальный модуль client protocol, не полноценный клиент.
2. Приём текущей Go/Linux и sing-box сборкой новой Rust server 2.5.0
   реализации. Текущий старый integrated workflow собирает legacy
   клиент+сервер 2.1.9/WIRE-4. Успех модульных тестов **не доказывает**
   совместимость старой пары с новой.
3. Реальный Deploy обновления/отката серверного ELF 2.5.0 в ARM64 VM,
   а также живой VK/TURN и фактический TCP/UDP трафик.

Идентификаторы WIRE-3/WIRE-4 показывают различие версий, но без
сквозного теста не доказывают ни полную совместимость, ни её отсутствие.

## Минимальный следующий шаг

Собрать **оригинальный ARM64 Rust client 2.5.0** в одноразовом
изолированном окружении с достаточным диском и памятью; затем проверить
парное реальное подключение к серверу 2.5.0 (авторизация, TUNCONF,
TCP/UDP, восстановление). После этого проверить штатный адаптированный
Deploy 2.5.0 и отказ/rollback в отдельной ARM64 VM. Не переписывать Go,
Deploy, sing-box или существующие маршруты ради этой проверки.
