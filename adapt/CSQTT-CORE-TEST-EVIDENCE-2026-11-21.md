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
| Go/ARM64 после изменения карточек правил | [Go #1658](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886141340) | В процессе на коммите 2902431; итог пока не подтверждён |
| Семь вкладок, карточки маршрутов | [UI #11](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886141341) | SUCCESS на 2902431; не проверяет новое мобильное исправление |
| Оригинальный Rust клиент 2.5.0 | [ARM64 client #2](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44833128457) | SUCCESS: оригинальный ARM64 ELF; не полноценный VK/TURN релиз |
| Rust 2.5.0 клиент ↔ сервер | [E2E #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44833443344) | Отдельный обмен GETCONF/TUNCONF и ICMP PASS, но общий workflow FAILURE: тест 10 сек не успел |
| Восстановление Rust 2.5.0 | [Диагностика #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44834213912) | SUCCESS после изменения только тестового лимита 10 → 40 сек |
| Go-адаптер запуска 2.5.0 | [Isolated #1](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44886448519) | SUCCESS только в экспериментальной ветке; в рабочий Go-раннер не включён |
| Реальный Rust ELF upgrade/forced rollback | Нет подтверждения | Pending |
| Реальный авторизованный CRUD клиентов и групп | Только mock E2E | Pending |

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
      "scope": "mock E2E present; live isolated server create/edit/delete/persistence not proven",
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
