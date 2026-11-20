# CSQTT Core — реестр тестов и исключение повторных испытаний

Дата фиксации: 21 ноября 2026 года. Статусы GitHub проверены по указанным запускам.

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
| Новые Go Deploy изменения | [Go #1656](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44818444566) | Pending: компиляция Go успешна, весь цикл и новые тесты пока не подтверждены |
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
      "component": "latest Go Linux ARM64 build with owned Deploy preflight",
      "status": "pending",
      "scope": "Go build stage observed successful, entire pipeline and new Go unit/race tests not yet confirmed",
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
