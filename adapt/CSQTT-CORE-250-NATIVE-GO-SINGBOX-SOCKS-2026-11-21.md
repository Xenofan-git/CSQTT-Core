# CSQTT Core 2.5.0 — совместная ARM64 приёмка Go и sing-box SOCKS5

Дата: 21 ноября 2026 года.

## Исходный экспериментальный пакет

- Четыре проверенных ARM64 ELF из CI 44894005565: Go supervisor, original Rust client v2.5, derived Core8 Rust server и sing-box 1.14.4.
- Обновление пакета Core7 -> Core8 и принудительный откат проверены отдельно в GitHub run 44894840740 SUCCESS. Запуск sing-box добавлен в сценарий отката *без изменения исходного продукта*, отдельная экспериментальная CI-ветка.
- GitHub test branch commit 419e3bfc29194802a50f7d9f9216872aa0ed4356.
- Фактический запуск: https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44895178052 — SUCCESS.

## Проверенный объединённый dataplane без VPN: PASS

Нативная одноразовая ARM64 Ubuntu GitHub Actions с настоящим systemd и выделенным PrivateNetwork namespace. Unit Go supervisor User=runner, NoNewPrivileges, ProtectSystem=strict, no capabilities, без интернет-маршрута в приватной сети. Параметры CSQTT_SING_BOX_BINARY и CSQTT_SING_BOX_DATA_DIR указывают строго на sing-box ELF и каталог пакета.

1. В исходном пакете Core7 реально запущен Go supervisor и работает его авторизованный приватный HTTP API.
2. В HTTP API записана sing-box конфигурация с SOCKS5 inbound 127.0.0.1:19180 и локальным CSQTT SOCKS outbound 127.0.0.1:12500; для этого теста CSQTT не включён, выбран fallback DIRECT. Конфиг проверен реальным sing-box 1.14.4.
3. HTTP POST /singbox/start вернул OK; GET /singbox/status подтвердил RUNNING. В этой же PrivateNetwork запущен временный Python HTTP server 127.0.0.1:47093.
4. Настоящий запрос curl через SOCKS5 127.0.0.1:19180 к этому HTTP server получил точное содержимое тестового marker-файла. HTTP POST /singbox/stop вернул OK.
5. Был намеренно сорван запуск нового пакета Core8 при обновлении; откат восстановил старый Go systemd unit. После отката снова пройдены пункты 2–4, real SOCKS5 и HTTP DIRECT запрос подтвердили работоспособность sing-box и его manager после восстановления.

GitHub log markers REAL_GO_PANEL_PINNED_SINGBOX_DIRECT_SOCKS5_LOOPBACK_PASS встречаются ДВАЖДЫ (до и после отката); REAL_NATIVE_ARM64_FOUR_COMPONENT_BUNDLE_ATOMIC_ROLLBACK_AND_GO_HTTP_PASS завершает успешный run.

## Ограничения — важно

Это реальный Go HTTP API → native sing-box → SOCKS5 → DIRECT → local HTTP server, **не** путь Go → Rust CSQTT client → Core8 server WIRE-3 → TUN → CSQTT SOCKS5 → sing-box. В этой проверке CSQTT-сессия специально выключена и внешний default route отсутствует. По отдельности доказаны 1) Go→оригинальный Rust client v2.5 subprocess API lifecycle, 2) original Rust client 2.5 ↔ Core8 WIRE-3 TURN/DTLS/SRTP+ICMP (44891239516), 3) Go→sing-box local SOCKS5 и пакетный rollback (44895178052), 4) Go legacy CSQTT SOCKS5 TCP/UDP tests. Они НЕ подтверждают полного объединённого authenticated VPN dataplane.

Основной Go WIRE-4 runtime, NanoPi/Keenetic, VPS и производственные маршруты не менялись. Автоматический производственный updater/Deploy четырёхкомпонентного пакета ещё не выпущен.
