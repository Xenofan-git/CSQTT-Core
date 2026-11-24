# CSQTT Core — канал безопасных обновлений

Источник: Xenofan-git/CSQTT-Core (не Endlad2/csqtt-core).
Исходная рабочая ветка: test/csqtt-core-linux-unified-253-20261122.
Проверка: GET /api/updates/core/check. HTTPS GitHub API; TLS сохраняется
и при резервном доступе через локальный SOCKS5 Sing-box.

Для разрешённого автоматического обновления необходим опубликованный
нечерновой и не prerelease GitHub-релиз с тегом csqtt-core-vX.Y.Z.
В релизе должен быть бинарник csqtt-core-headless-linux-arm64 (ELF64
AArch64, модуль Go lalune-desktop) размером 5–128 МиБ с GitHub asset
digest sha256. Go buildCommit должен совпадать с коммитом тега.
Установленный коммит должен быть предком коммита релиза (compare ahead
или identical). Посторонние URL и недоверенные редиректы запрещены.

POST /api/updates/core отключается с HTTP 410. POST /api/updates/core/wait
доступен после авторизации только из LAN/Tailscale и при отключённом
CSQTT-подключении. После проверки SHA-256 отдельная systemd-служба
создаёт резервную копию контроллера и unit, атомарно заменяет ТОЛЬКО
csqtt-core-headless, перезапускает ТОЛЬКО csqtt-core.service, проверяет
HTTP login, sing-box inbound, состояние остальных служб и маршруты.
При сбое автоматически возвращается старый бинарник.

Статус: GET /api/updates/core/status, /var/lib/csqtt-core/updates/last-status
и журнал transient unit csqtt-core-upgrade-*.
Rust CSQTT, Core8, Sing-box и маршрутизация NanoPi не обновляются.

На 24.11.2026 совместимого опубликованного ARM64-релиза нет.
Кнопка «Обновить CSQTT Core» поэтому остаётся заблокированной.
