# CSQTT Core 2.5.0 — план поэтапного сведения приложения без переделки работающего кода

Дата: 21 ноября 2026. Статус: исходники и CI сверены; рабочие системы не изменялись.

## Что уже действительно проверено

- **Rust CSQTT Core 2.5.0 / WIRE-3, релиз №8:** GitHub Actions 44890803542 SUCCESS. Подтверждён обратный маршрут 10.67.69/24, включая byte-array prefix RouteTable; бинарник SHA256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706.
- **Оригинальный Rust-клиент 2.5.0 ↔ адаптированный Rust-сервер Core №8:** настоящий TURN/DTLS/SRTP, GETCONF/TUNCONF и возврат ICMP в клиентский диспетчер; run 44891239516 SUCCESS.
- **Физический NanoPi R3S:** нативный ARM64 сервис в отдельном user/network namespace; реальный TUN/iptables, HTTPS и аварийный перезапуск; временные тестовые файлы удалены.
- **Отдельный Deploy с systemd:** реальное принудительное восстановление предыдущей ARM64 версии после отказа запуска кандидата; run 44890007663 SUCCESS. Этот тест проверял переход на №7, а **не** на №8.
- **Интегрированная Go/ARM64 сборка №1661:** build job SUCCESS с тестами Go, -race, всех семи вкладок в Chromium и закреплённым sing-box 1.14.4. Реальные sing-box TCP/UDP ASSOCIATE, DNS hijack, reverse mapping и внешний SOCKS5 с авторизацией PASS. Длинный system VM job остаётся отдельным и не должен ошибочно маркироваться SUCCESS.
- **Go запуск клиента 2.5.0:** экспериментальная безопасная обвязка в тестовой ветке bf4ba8c прошла ARM64 unit/process test. Добавлен реальный ELF тест: native ARM64 GitHub Actions **44892486891 SUCCESS**, commit a56e96b; оригинальный v2.5.0 ARM64 клиент (SHA256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa) действительно запущен Go helper с секретом только в CSQTT_PASSWORD и достигает проверки параметров после авторизации. Это не полноценный Go supervisor/TUN/соединение.
- **Системная Go/ARM64 VM №1658:** GitHub Actions 44886141340 теперь **FULL SUCCESS**, оба build и VM jobs. Более поздние №1659–1661 остаются отдельными; не подменять их статусы №1658.

## Точный разрыв, который остаётся

Основной workflow .github/workflows/build-original-linux-arm64.yml продолжает собирать согласованные legacy client/server с CSQTT-WIRE-4 и серверной базой v2.1.9. Отдельный утверждённый Rust Core v2.5.0 использует CSQTT-WIRE-3. Эти два комплекта нельзя просто скрестить.

Дополнительно текущий Go LinuxRunner.StartCore (генерация через adapt/apply-pinned-original-linux-arm64-policy.py) вызывает appendCSQTTAuthArgs и передаёт флаги -device-id и -password посредством sudo/sh. Оригинальный rust-client/main.rs v2.5.0 требует CSQTT_PASSWORD в окружении и не принимает старые identity/password flags. Нельзя менять путь к ELF без переключения этого точного процесса запуска; пароль нельзя выводить в argv, логи или упаковку.

Проверка формата 2.5.0 и fake-process запуска Go уже была сделана. Её повторять не нужно. Адаптер изолированной ветки не включён в production Go.

## Следующие последовательные этапы

1. **Go supervisor → оригинальный Rust client 2.5.0.** В изолированной сборке подключить только проверенный помощник запуска через env CSQTT_PASSWORD, сохранив существующие модели конфигурации, lifecycle, обработку stdin, TUN-UDS, user actions и process-group cleanup. Не заменять в работающем bundle WIRE-4 на WIRE-3 до прохождения реального Go→клиент→сервер теста.
2. **Единый CSQTT Core 2.5.0 bundle.** В candidate bundle должны присутствовать именно проверенные клиент 2.5.0 и Rust сервер №8. Проверить sourceTag, точные SHA256, WIRE-3 и отсутствие смешанных клиентских/серверных WIRE-4 components. Утверждённую семивкладочную панель, Direct policy и sing-box manager не переделывать.
3. **Адресная интеграционная приёмка.** В отдельной ARM64 VM запустить Go supervisor → original 2.5 client → release8 2.5 server, проверить WIRE-3, VK-token injection contract, конфигурационный ответ, TUN, SOCKS5 :12500, sing-box inbound :19180, Direct исключения и явно выбранные outbounds; после SIGKILL проверить восстановление. Никакого host default route.
4. **Deploy релиза №8.** Повторить systemd upgrade/rollback только для реально нового сборочного bundle №8 (предыдущий SUCCESS проверял №7) и сверить защиту секретной ссылки, состояние файлов и работающий HTTPS после обновления и rollback.
5. **Финальный release gate.** На конкретном SHA завершить ещё незавершённые Go ARM64 system VM jobs, проверить защищённый UI/API Desktop/Mobile (7 вкладок), отсутствие лишних фоновых процессов, работу маршрутизации в изолированной policy на NanoPi. Рабочие M-UI/HydraRoute/Tailscale и Direct LTE не переключать.

## Не делать повторно без изменения компонента

Не повторять уже зелёные Chromium-проверки интерфейса, отдельный original client↔Core №8 E2E, чистые Rust API CRUD, маршрутизацию sing-box TCP/UDP/DNS, предыдущий forced rollback и Go/race unit tests. Повторять только при целевом изменении их входов/контрактов.

**Источники:** README.md; adapt/CSQTT-CORE-ARCHITECTURE-DECISION-2026-11-21.md; adapt/CSQTT-CORE-TEST-EVIDENCE-2026-11-21.md; adapt/CSQTT-CORE-THREE-FINAL-ACCEPTANCE-2026-11-21.md; GitHub Actions run IDs, указанные выше.

**Основные журналы:** Amsterdam VPS, /srv/projects/CSQTT-Core/journals/CSQTT-sing-box-PROJECT-SUMMARY.md и CSQTT-sing-box-for-Debian-Ubuntu-ARM64-JOURNAL.md.
