# CSQTT Core — реестр тестов и исключение повторных испытаний

Дата фиксации: 21 ноября 2026 года. Статусы GitHub проверены по указанным запускам.

**Примечание после коррекции области работ:** результаты, относящиеся
к экспериментальным изменениям Go/Deploy после df2750d, остаются в
истории, но сами изменения отменены. Эти запуски не являются проверкой
текущего рабочего Go/Deploy. Изменение названия не требует повторения
интеграционных испытаний без изменения поведения.

## Правила использования

1. Результат относится к конкретному коммиту и проверенной реализации. Старый успешный запуск не подтверждает новый бинарник автоматически.
2. При изменении исходников запускаем тесты затронутого компонента; другие неизменённые подсистемы без причины не прогоняем.
3. Интеграционная проверка необходима при изменении взаимодействия компонентов, сети, Deploy, конфигурации и бинарной сборки.
4. Неизвестный путь — ручной аудит, а не «тесты не нужны».
5. После завершения миграции — одна полная релизная приёмка на конкретном SHA: ARM64, SOCKS5/TUN, Direct/DNS, Deploy, откат, восстановление.
6. Mock-тесты не заменяют реальные upgrade/rollback и CRUD на изолированном стенде.
7. Изменение только документации или этого реестра не требует ARM64-сборки.

## Зафиксированные результаты

| Компонент | Проверенный запуск | Граница результата |
| --- | --- | --- |
| Семь вкладок, API-контракты | [UI #8](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44818444696) | Mock HTTP API, не live CRUD |
| Установщик и откат | [Deploy #20](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44602714530) | Изолированный mock; не новый ELF в VM |
| Серверная панель | [Browser #2](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44605678231) | Offline browser; авторизованное чтение отдельно проверено на лабораторном стенде |
| Rust ARM64 | [Rust #7](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44553540024) | ARM64 ELF + одноразовый smoke; не upgrade/forced rollback |
| Go Linux VM | [Go #1651](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44797656023) | Предыдущая версия: реальная сеть и recovery; не покрывает последние Go Deploy изменения |
| Go Deploy, отменённый эксперимент | [Go #1656](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44818444566) | SUCCESS исторически, но изменения затем отменены: не принимать как проверку текущего Go-кода |
| Возврат к рабочей Go/ARM64 сборке | [Go #1657](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44826258557) | SUCCESS на коммите e1cf635; без новых изменений в сетевой логике |
| Go/ARM64 после изменения карточек правил | [Go #1658](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886141340) | FULL SUCCESS: Go build и отдельный ARM64 system VM; коммит 2902431, не заменяет более поздние VM запуски |
| Семь вкладок, карточки маршрутов | [UI #11](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886141341) | SUCCESS на 2902431; не проверяет новое мобильное исправление |
| Оригинальный Rust клиент 2.5.0 | [ARM64 client #2](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44833128457) | SUCCESS: оригинальный ARM64 ELF; не полноценный VK/TURN релиз |
| Rust 2.5.0 клиент ↔ сервер | [E2E #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44833443344) | Отдельный обмен GETCONF/TUNCONF и ICMP PASS, но общий workflow FAILURE: тест 10 сек не успел |
| Восстановление Rust 2.5.0 | [Диагностика #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44834213912) | SUCCESS после изменения только тестового лимита 10 → 40 сек |
| Go-адаптер запуска 2.5.0 | [Isolated #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886448519) | SUCCESS только в экспериментальной ветке; в рабочий Go-раннер не включён |
| Физический NanoPi: Go HTTP API → оригинальный Rust client 2.5 | Локальный RK3566, CI кандидата 44894005565 | PASS: реальный /vpn/connect → Rust ELF → /vpn/disconnect; no secret in logs; host default route unchanged. Full VPN/TUN/SOCKS/sing-box dataplane NOT tested |
| Реальный клиент v2.5.0 запускается из Go адаптера | [Native ARM64 #2](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44892486891) | SUCCESS на a56e96b: проверенный оригинальный ELF 2.5.0, пароль только env, не argv; тестовый процесс завершён до TUN. Не объединён с production Go |
| Физический NanoPi R3S LTS — нативный ARM64 Core №7 | Физический RK3566, 21.11.2026 | PASS: два нативных запуска, 64/64 real API, TUN/iptables, SIGKILL/restart, штатная очистка; маршруты и службы хоста неизменны |
| Реальный клиент 2.5.0 / Core №8: современный TURN/DTLS/SRTP/ICMP | GitHub 44891239516 | SUCCESS: исходный Rust-клиент 2.5.0, синтетический local TURN, DTLS, SRTP, GETCONF/TUNCONF, двусторонний ICMP в диспетчере, 1 passed; 0 failed |
| Реальный клиент 2.5.0 / Core №7: двусторонний ICMP | GitHub 44890243962 | FAIL: DTLS/SRTP/GETCONF/TUNCONF PASS, но обратный ICMP отсутствует; route_index() оставался на 10.66.67/24 при Core 10.67.69/24 |
| Core №8: исправление downlink subnet и нативный запуск | GitHub Rust release №8: 44890803542, NanoPi RK3566 | PASS: build+source guard+runtime smoke, ELF SHA256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706; NanoPi TUN/HTTPS/firewall and cleanup. Modern original 2.5 client roundtrip with Core №8 now PASS in GitHub Actions 44891239516 |
| Реальный native ARM64 systemd install и forced rollback | GitHub Actions run 44890007663 | SUCCESS: verified старый 10aa3f1 → новый 21c59ba, сбой активации вызван намеренно; прежний бинарник/unit/env/state, active/boot-enabled и HTTPS восстановлены, default route не изменён |
| Новый Rust Core ARM64 №7: живой HTTPS/API, 3 старта | Локальный тест 21.11.2026, ELF 21c59ba | PASS: 96/96 API; аварийное завершение SIGKILL, следующий старт и HTTPS, 0 тайм-аутов startup cleanup. Изолированный QEMU, не Native ARM64 VM |
| Новый Rust Core ARM64 №7: реальный Linux TUN/NAT | Локальный тест 21.11.2026, ELF 21c59ba | PASS: TUN 10.67.69.1/24, 3 правила iptables, SIGKILL/restart без дублей, SIGTERM полностью удаляет правила, 3 запуска, host default unchanged |
| Новый Rust Core ARM64 №7: аутентифицированный legacy UDP GETCONF | Локальный тест 21.11.2026, ELF 21c59ba | PASS: upstream WIRE-2 fixture, действительный UDP, HMAC-SHA1/AES128-CTR, TUNCONF 10.67.69.2. Не полноценный WIRE-3/DTLS data E2E |
| Адаптированный Rust Core ARM64: реальный HTTPS CRUD | Локальный live-test 21.11.2026, артефакт 10aa3f1 | PASS: 2 запуска одной базы, 32+32 проверки, авторизованные клиенты и группы, очистка, HTTPS 200, изолированная сеть. Не новейший выпуск №7 |
| Сохранность API адаптированного Rust Core (статический код) | Локальная проверка b7cb832 | PASS: оригинальный v2.5.0 и производный web_panel.rs имеют 91 одинаковый маршрут; только семь разрешённых замен адреса подсети. Это не live CRUD адаптированного ELF |
| Оригинальный CSQTT 2.5.0: реальный авторизованный CRUD ARM64 | [Original CRUD #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44887573311) | SUCCESS: настоящий ARM64 Rust-сервер из официального APK, изолированная сеть, HTTPS secret path, группа+клиент create/read/update/delete, cleanup, перезапуск HTTPS, маршрут по умолчанию неизменен |
| Реальный Rust ELF upgrade/forced rollback | Нет подтверждения | Pending |
| Реальный авторизованный CRUD клиентов и групп в адаптированном Rust Core | Оригинальный сервер уже проверен; Core-адаптация отдельно | Pending для адаптированного Core и целевой VM |

## Машиночитаемый реестр

Быстрый CI читает JSON ниже и проверяет статусы, коммиты и маршрутизацию изменений. Никакие результаты не переходят из pending в passed автоматически.

```json
{
  "schema_version": 1,
  "project": "CSQTT Core",
  "recorded_at": "2026-11-21",
  "policy": "Evidence applies to the cited commit and tested implementation, never automatically to a later changed binary.",
  "evidence": [
    {
      "id": "native-ui-browser-8",
      "component": "native seven-tab UI and static API contracts",
      "status": "passed",
      "scope": "mock HTTP API; browser viewports, seven tabs, source contracts and source guards; not live CRUD",
      "commit": "3728052fb8526a71b68e2c374ac0c12959a4f83b",
      "run_id": 44818444696,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44818444696"
    },
    {
      "id": "deploy-mock-rollback-20",
      "component": "Rust Core systemd installer and rollback mock lifecycle",
      "status": "passed",
      "scope": "isolated mock lifecycle, snapshot/hash/symlink and safety regression; not a real new binary upgrade in QEMU",
      "commit": "5cc135e0d1dd4270424b8c10c2ba6ac3ba56bd6b",
      "run_id": 44602714530,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44602714530"
    },
    {
      "id": "server-panel-browser-2",
      "component": "server panel read-only and login safety scripts",
      "status": "passed",
      "scope": "browser smoke and offline guard; live authenticated three-viewport acceptance separately observed in dedicated ARM64 guest",
      "commit": "b89407a73d135839f3d75b187f9d5ef19ad4f127",
      "run_id": 44605678231,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44605678231"
    },
    {
      "id": "rust-arm64-verified-7",
      "component": "standalone Rust CSQTT Core ARM64 release binary",
      "status": "passed",
      "scope": "compiled native ARM64 ELF and disposable-runner smoke, not deployed upgrade/forced rollback",
      "commit": "21c59ba2918fd04e4324a897d7afc2e53a2e0cae",
      "run_id": 44553540024,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44553540024"
    },
    {
      "id": "go-arm64-vm-1651",
      "component": "Go Linux client and server: real ARM64 VM network and recovery",
      "status": "historical",
      "scope": "older tested source, actual TCP/UDP SOCKS5/TUN/routing/recovery; cannot validate later Go Deploy edits",
      "commit": "2b48f6c083a0e293f648f02d80b58bda61719947",
      "run_id": 44797656023,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44797656023"
    },
    {
      "id": "go-arm64-1656",
      "component": "Go Deploy preflight experiment (later reverted)",
      "status": "historical",
      "scope": "workflow completed SUCCESS for SHA 3728052, but experiment was reverted in e1cf635; not evidence for current main integrated implementation",
      "commit": "3728052fb8526a71b68e2c374ac0c12959a4f83b",
      "run_id": 44818444566,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44818444566"
    },
    {
      "id": "real-deploy-rust-elf-guest",
      "component": "new Rust ARM64 ELF install, forced rollback and post-restart service health",
      "status": "pending",
      "scope": "real binary upgrade/rollback not proven; previous tool limitation still applies",
      "commit": "unverified",
      "run_id": null,
      "url": null
    },
    {
      "id": "real-api-crud",
      "component": "authorized real client/group mutation lifecycle",
      "status": "pending",
      "scope": "Official original 2.5.0 server CRUD passed separately in GitHub run 44887573311; current derived Core server and user VM live CRUD remain unverified; mock E2E tested before.",
      "commit": "unverified",
      "run_id": null,
      "url": null
    },
    {
      "id": "go-arm64-1657",
      "component": "Go ARM64 integrated after restored functional sources",
      "status": "passed",
      "scope": "CI SUCCESS on correction commit e1cf635; restored prior behavior, not final original 2.5.0 full integration",
      "commit": "e1cf635fe414650c419dc9b994a76122848671db",
      "run_id": 44826258557,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44826258557"
    },
    {
      "id": "go-arm64-1658",
      "component": "Go ARM64 integrated after Route card UI-only patch",
      "status": "pending",
      "scope": "in_progress as of 2026-11-21 audit; do not count as passed until completion",
      "commit": "29024310e655c2e38a6caf9071b2d92f28075cd1",
      "run_id": 44886141340,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886141340"
    },
    {
      "id": "native-ui-browser-11",
      "component": "seven-tab UI with Route-card toggles and fixed CSQTT outbound",
      "status": "passed",
      "scope": "CI SUCCESS on 2902431; native app mock route and interaction, no real traffic and no 390px Route overflow assertion yet",
      "commit": "29024310e655c2e38a6caf9071b2d92f28075cd1",
      "run_id": 44886141341,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886141341"
    },
    {
      "id": "original-v250-arm64-client",
      "component": "original CSQTT 2.5.0 native ARM64 client binary",
      "status": "passed",
      "scope": "original pinned Rust client built on ARM64 from fd936ef; SHA256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa; no VK live acceptance",
      "commit": "c89e18aa13a17d554278f42401fc66551e0b0410",
      "run_id": 44833128457,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44833128457"
    },
    {
      "id": "original-v250-realpair",
      "component": "original 2.5.0 isolated real TURN client-session engine and independent server E2E",
      "status": "historical",
      "scope": "overall workflow FAILED due separate recovery 10s limit; one independent DTLS/GETCONF/TUNCONF/ICMP client-server test PASSED, not an overall green run",
      "commit": "28a75a998e11cee88cfc55f02c9e0302ffb3cfed",
      "run_id": 44833443344,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44833443344"
    },
    {
      "id": "original-v250-recovery-diagnostic",
      "component": "isolated ARM64 recovery test with longer fixture timeout",
      "status": "passed",
      "scope": "focused workflow SUCCESS after changing only disposable Rust test timeout 10 to 40 seconds; product client/server untouched",
      "commit": "7e907e18318cc118806fb9e9b7d1102e0af2b21c",
      "run_id": 44834213912,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44834213912"
    },
    {
      "id": "go-launch-v250-experimental",
      "component": "isolated Go-to-original-2.5 password environment contract",
      "status": "passed",
      "scope": "experimental branch only, native ARM64 test success; not compiled into main Go controller or deployed to NanoPi",
      "commit": "bf4ba8c4f14c2a9c3ab69c4220e406867a801cf9",
      "run_id": 44886448519,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886448519"
    },
    {
      "id": "original-v250-real-arm64-crud",
      "component": "Unmodified original CSQTT v2.5.0 ARM64 real HTTPS API: authorized client and group CRUD, cleanup, restart",
      "status": "passed",
      "scope": "Native ubuntu-24.04-arm disposable GitHub-hosted runner, official APK server binary SHA256 d8930e19d628dec7715def67c6cc7a9a2d66120bbb7559b2db403763fa6fc73e, WIRE-3; network namespace without default route; real HTTPS auth, group and client create/edit/delete, login cookie, retained global settings and baseline, HTTPS relaunch, host default route unchanged and namespace cleaned. Does NOT prove derived Core server variant, user VPS guest, actual Deploy/rollback or VK traffic.",
      "commit": "74e3345bf2b0ce471539c05f12214c678670d137",
      "run_id": 44887573311,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44887573311"
    },
    {
      "id": "derived-v250-web-panel-parity",
      "component": "Original CSQTT v2.5.0 vs derived Core Rust web_panel.rs static API equivalence",
      "status": "passed",
      "scope": "Executed locally on Moscow VPS from clean pinned official git archive fd936ef with isolated generated source. 91 route paths identical; 9 critical API paths present; exact panel source parity with only 7 allowed subnet replacements. SHA original 6bf445acb280e3506ef3f0ec494488307836f16d4eedb61fb4ada35121bc81ea; derived 07a73dfb378fb7ffe40410778f9ec721e5ace9f1abbada3b6e84fe35b8e86ef2. NOT native ARM64 live CRUD, installer rollback or runtime Go/sing-box.",
      "commit": "b7cb8329648b62aee8acec8611b5a39800fd4ee1",
      "run_id": null,
      "url": null
    },
    {
      "id": "derived-core-10aa3f1-real-api-local",
      "component": "ARM64 derived CSQTT Core 2.5.0 real HTTPS auth and client/group CRUD in QEMU",
      "status": "passed",
      "scope": "2026-11-21 Moscow VPS, verified ARM64 ELF SHA256 771453b7efc155ffc60cbecdf03581a957e6b292c7d5fb63435ec4a482a570ad built at 10aa3f1, QEMU user mode, outer UID nobody, separate user+network namespaces loopback only, network command stubs. Existing real E2E Python run twice on same temporary config: 32/32 + 32/32 PASS, real login and Secure/HttpOnly cookie, client/group CRUD and baseline unchanged, host default route unchanged. Test temp deleted. NOT newest release #7, host network TUN nor Deploy/rollback. Report adapt/CSQTT-CORE-DERIVED-API-LIVE-2026-11-21.md.",
      "commit": "10aa3f1e4344d317bf5c2f638da80e4d065401fc",
      "run_id": null,
      "url": null
    },
    {
      "id": "derived-core-21c59ba-real-api-local",
      "component": "New verified Rust Core #7 ARM64 actual HTTPS API 3-start E2E",
      "status": "passed",
      "scope": "Three restarts on one disposable SQLite; actual live client/group CRUD 32/32 each, 96/96 total; one SIGKILL; 3 successful cleanup stage logs, 0 startup cleanup timeouts; no real network commands in this API-focused leg. Local Moscow VPS 2026-11-21, verified ELF sha256 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb. QEMU user-mode aarch64; outer nobody, isolated unprivileged user/net namespace; no host default route modifications. Report adapt/CSQTT-CORE-ARM64-LIVE-RELEASE7-NETWORK-2026-11-21.md. No new GitHub workflow run.",
      "commit": "21c59ba2918fd04e4324a897d7afc2e53a2e0cae",
      "run_id": null,
      "url": null
    },
    {
      "id": "derived-core-21c59ba-real-net-tun-local",
      "component": "New verified Rust Core #7 ARM64 actual Linux TUN and firewall in separate netns",
      "status": "passed",
      "scope": "Actual Linux TUN 10.67.69.1/24, subnet route, three managed real iptables rules. Three boots with SIGKILL/SIGTERM; no duplicate rules after crash, no remaining rules after clean stops; no default route. Local Moscow VPS 2026-11-21, verified ELF sha256 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb. QEMU user-mode aarch64; outer nobody, isolated unprivileged user/net namespace; no host default route modifications. Report adapt/CSQTT-CORE-ARM64-LIVE-RELEASE7-NETWORK-2026-11-21.md. No new GitHub workflow run.",
      "commit": "21c59ba2918fd04e4324a897d7afc2e53a2e0cae",
      "run_id": null,
      "url": null
    },
    {
      "id": "derived-core-21c59ba-legacy-getconf-live",
      "component": "New verified Rust Core #7 ARM64 real encrypted UDP legacy GETCONF",
      "status": "passed",
      "scope": "Pinned upstream legacy WIRE-2 ciphertext sent to Rust Core, actual UDP SRTP session and reply, HMAC-SHA1 verified and AES128-CTR decrypted to TUNCONF with assigned 10.67.69.2 and DNS. Does not prove full modern WIRE-3 DTLS client session or bidirectional ICMP. Local Moscow VPS 2026-11-21, verified ELF sha256 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb. QEMU user-mode aarch64; outer nobody, isolated unprivileged user/net namespace; no host default route modifications. Report adapt/CSQTT-CORE-ARM64-LIVE-RELEASE7-NETWORK-2026-11-21.md. No new GitHub workflow run.",
      "commit": "21c59ba2918fd04e4324a897d7afc2e53a2e0cae",
      "run_id": null,
      "url": null
    },
    {
      "id": "physical-nanopi-r3s-native-arm64-release7",
      "component": "Actual physical NanoPi R3S RK3566 native ARM64 Core release7 isolated acceptance",
      "status": "passed",
      "scope": "Native ELF 21c59ba SHA256 3295c38f, outer UID nobody, isolated user+network namespaces; real HTTPS/API 32+32 PASS, real TUN 10.67.69.1/24, iptables 3 rules, SIGKILL/restart without duplicates, clean SIGTERM, host route and M-UI/HRCore/Tailscale unchanged; no production systemd install.",
      "commit": "21c59ba2918fd04e4324a897d7afc2e53a2e0cae",
      "run_id": null,
      "url": null
    },
    {
      "id": "real-arm64-systemd-release7-fault-rollback",
      "component": "Actual native ARM64 systemd baseline install and forced release7 candidate activation failure with rollback",
      "status": "passed",
      "scope": "GitHub hosted ephemeral ubuntu-24.04-arm; old verified 10aa3f1 ELF distinct from new verified release7 ELF; real unmodified installer; injected systemctl start failure at candidate activation, previous binary/unit/env and preserved state restored, systemd active+enabled, previous HTTPS verified, default route unchanged. Production NanoPi/VPS not touched.",
      "commit": "9e80feaa2699fe800e43ae06efc182c10e7f0421",
      "run_id": 44890007663,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44890007663"
    },
    {
      "id": "core-release7-modern-realpair-icmp-failure",
      "component": "Original CSQTT 2.5 client vs adapted Core release7 real TURN DTLS/SRTP E2E",
      "status": "failed",
      "scope": "GitHub isolated ARM64 run 44890243962: real original client ↔ derived release7 DTLS/SRTP, GETCONF/TUNCONF pass; final return ICMP to client dispatcher FAILED. Root cause Core route_index raw SUBNET_PREFIX [10,66,67] despite TUN/assigned IP 10.67.69/24. Corrected in release8.",
      "commit": "1da885ab884bbaf3e07c741093b2290d0946d8ed",
      "run_id": 44890243962,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44890243962"
    },
    {
      "id": "core-release8-fixed-route-source-build-native-smoke",
      "component": "Corrected route_index prefix [10,67,69] in verified Core 2.5 ARM64 Rust release8",
      "status": "passed",
      "scope": "Fixed generator commit 91b154011ca16c219f57eef99462d3408dccfeeb, verified ARM64 Rust release run44890803542 SUCCESS, SHA256 ELF a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706; source route prefix guard passes; on physical NanoPi in user/net namespace real ARM64 TUN/HTTPS/iptables clean start/stop PASS and host services/routing unchanged. Does NOT yet prove modern client ICMP return; separate CI in progress.",
      "commit": "91b154011ca16c219f57eef99462d3408dccfeeb",
      "run_id": 44890803542,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44890803542"
    },
    {
      "id": "core-release8-original-client-realpair-turn-icmp",
      "component": "Original CSQTT v2.5.0 client vs fixed derived ARM64 Core release8 real TURN/DTLS/SRTP and bidirectional ICMP E2E",
      "status": "passed",
      "scope": "GitHub native ubuntu-24.04-arm isolated local TURN client/server E2E. Client integration source original pinned v2.5.0 fd936ef; verified derived Rust Core release8 source commit 91b1540, binary SHA256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706. Real DTLS, SRTP, GETCONF/TUNCONF and ICMP return through original client dispatcher PASS, 1 passed 0 failed. Test fixture destination Core TUN 10.67.69.1; no client product code changes; no production routing. Historical release7 failure 44890243962 separately retained.",
      "commit": "642eee277408d0b30ad40bc580643bae1e5ab404",
      "run_id": 44891239516,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44891239516"
    },
    {
      "id": "original-v250-real-go-client-launch",
      "component": "Go exec.Cmd launches the actual official CSQTT v2.5.0 ARM64 client with env-only password",
      "status": "passed",
      "scope": "Native ARM64 GitHub run44892486891 SUCCESS, test branch a56e96ba2f775d72195ceb6d9884334e829356fa; authentic pinned upstream v2.5.0 client ELF sha256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa. Go helper launches real client with CSQTT_PASSWORD only in environment; absent password rejected; intentional invalid obfs exits before TUN/VK/network. Not integrated production Go supervisor and not VPN/Direct/sing-box E2E.",
      "commit": "a56e96ba2f775d72195ceb6d9884334e829356fa",
      "run_id": 44892486891,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44892486891"
    },
    {
      "id": "go-v250-candidate-native-http-control",
      "component": "Native ARM64 Go HTTP /vpn/connect launches verified original CSQTT 2.5 Rust ELF and disconnects",
      "status": "passed",
      "scope": "2026-11-21 physical NanoPi R3S RK3566 user+network isolated loopback-only. Test branch ed7516a, run44894005565 SUCCESS; real HTTP API synthetic profile and tunnel password, POST /vpn/connect 200, actual Rust child launched by Go LinuxRunner, POST /vpn/disconnect 200 and cleanup. No secret in logs, outer default route unchanged, running services active. No real VPN/TUN/SOCKS5/sing-box traffic (fake peer). See adapt/CSQTT-CORE-250-NATIVE-GO-CANDIDATE-API-2026-11-21.md.",
      "commit": "ed7516a82c909e2f405a2e655c0f849c1ed08964",
      "run_id": 44894005565,
      "url": "https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44894005565"
    }
  ],
  "impact_rules": [
    {
      "group": "documentation",
      "paths": [
        "README.md",
        "adapt/*.md",
        "docs/**/*.md"
      ],
      "tests": []
    },
    {
      "group": "evidence-ledger",
      "paths": [
        "adapt/CSQTT-CORE-TEST-EVIDENCE-2026-11-21.md"
      ],
      "tests": [
        "ledger-validation"
      ]
    },
    {
      "group": "native UI",
      "paths": [
        "adapt/csqtt-panel-preview.html",
        "adapt/nanopi.js",
        "adapt/package-csqtt-core-web.py"
      ],
      "tests": [
        "seven-tab-browser",
        "go-arm64-integrated"
      ]
    },
    {
      "group": "UI test sources",
      "paths": [
        "adapt/test-csqtt-panel-browser.py",
        "adapt/test-csqtt-panel-contract.py",
        "adapt/test-csqtt-panel-api-method-contract.py",
        "adapt/test-csqtt-core-branding.py",
        "adapt/test-csqtt-core-web-package.py",
        ".github/workflows/csqtt-core-app-panel-browser.yml",
        "adapt/test-csqtt-core-no-dart-patching.py",
        "adapt/test-csqtt-core-go-api-boundary.py"
      ],
      "tests": [
        "seven-tab-browser"
      ]
    },
    {
      "group": "server panel",
      "paths": [
        "adapt/csqtt-server-panel-upstream-250.html",
        "adapt/test-original-server-all-dialogs-browser.py",
        ".github/workflows/csqtt-core-server-panel-browser.yml"
      ],
      "tests": [
        "server-panel-browser"
      ]
    },
    {
      "group": "Go HTTP and Deploy",
      "paths": [
        "adapt/headless_main.go",
        "adapt/headless_main_test.go",
        "adapt/headless_api_contract_test.go",
        "adapt/csqtt_core_deploy*.go"
      ],
      "tests": [
        "go-unit-race",
        "go-arm64-integrated",
        "deploy-fixture"
      ]
    },
    {
      "group": "Go client and routing",
      "paths": [
        "adapt/csqtt_core_socks5*.go",
        "adapt/apply-csqtt-core-socks5.py",
        "adapt/apply-pinned-original-linux-arm64-policy.py"
      ],
      "tests": [
        "go-unit-race",
        "go-arm64-integrated",
        "isolated-routing-vm"
      ]
    },
    {
      "group": "Go/Rust Deploy transport",
      "paths": [
        "adapt/patch-csqtt-deploy-manager.py",
        "adapt/patch-csqtt-core-source.py"
      ],
      "tests": [
        "go-arm64-integrated",
        "isolated-deploy-vm"
      ]
    },
    {
      "group": "Rust installer",
      "paths": [
        "server-assets/deploy-csqtt-core-250.sh",
        "adapt/test-csqtt-core-rollback-lifecycle.py",
        ".github/workflows/csqtt-core-deploy-regression.yml"
      ],
      "tests": [
        "deploy-regression",
        "isolated-upgrade-rollback-if-new-binary"
      ]
    },
    {
      "group": "Rust core",
      "paths": [
        "adapt/derive-csqtt-core-rust-250.py",
        "adapt/instrument-csqtt-core-startup-cleanup.py",
        "adapt/tproxy-udp-*.py",
        ".github/workflows/csqtt-core-verified-arm64-release.yml"
      ],
      "tests": [
        "rust-arm64-binary",
        "isolated-runtime-vm"
      ]
    },
    {
      "group": "VK token/CAPTCHA",
      "paths": [
        "captcha-bridge/*",
        ".github/workflows/vk-hash-secret-validation.yml"
      ],
      "tests": [
        "vk-token-contract",
        "captcha-origin-guard"
      ]
    },
    {
      "group": "integrated build and VM",
      "paths": [
        ".github/workflows/build-original-linux-arm64.yml"
      ],
      "tests": [
        "go-arm64-integrated"
      ]
    },
    {
      "group": "isolated ARM64 VM workflow",
      "paths": [
        ".github/workflows/csqtt-core-arm64-system-vm-test.yml"
      ],
      "tests": [
        "isolated-routing-vm"
      ]
    }
  ]
}
```

## Принцип завершения

Не переписывать работающие компоненты ради косметической чистоты. Мигрировать одну зависимость за раз, фиксировать тесты конкретного изменения, при выпуске провести одну полную комплексную приёмку. Рабочий NanoPi, HydraRoute Core и маршруты в эту операцию не входят.
