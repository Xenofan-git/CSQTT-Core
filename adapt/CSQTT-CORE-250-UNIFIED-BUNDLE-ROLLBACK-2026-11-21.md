# CSQTT Core 2.5.0 — настоящее испытание отката экспериментального пакета

Дата: 21 ноября 2026. Статус: PASS, изолированный GitHub-hosted ARM64 systemd, не производство.

## Проверенный пакет

Ранее проверенные четыре ARM64 ELF: кандидат Go supervisor, оригинальный клиент CSQTT 2.5.0, исправленный сервер Rust Core8 и sing-box 1.14.4 из GitHub Actions 44894005565. ZIP SHA256 513c71630655d51ebdb4ea506116b13146772e100144938ffc16f26599d34427.

Для исходного пакета сервер Core8 заменён на отдельный проверенный сервер Core7 (SHA256 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb); новый Core8 SHA256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706. Три остальные ELF неизменны, сверены по SHA256.

## Реальный systemd test — SUCCESS

GitHub Actions: https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44894840740

Одноразовая нативная ARM64-машина Github Actions, выделенный systemd unit csqtt-core-250-unified-rollback-ci.service, User=runner, NoNewPrivileges, PrivateNetwork, ProtectSystem=strict, no capabilities. Файлы в индивидуальном временном каталоге, trap завершает unit и удаляет тестовые данные.

- Все четыре исходных ELF и нового кандидата проверены.
- systemd реально запустил прежний пакет с Core7, enabled+active, настоящий приватный Go HTTP API отвечал в сетевом namespace службы.
- Служба остановлена; переключение на новый пакет Core8 выполнено атомарным переименованием симлинка.
- При реальной активации нового пакета специально вызван отказ ExecStartPre; systemctl start вернул ошибку, проверенный активный симлинк ещё указывал на новый Core8.
- После atomic rollback восстановлен пакет Core7, прежний Go supervisor реально снова запущен (enabled+active), приватный HTTP API доступен; сохранены контрольная сумма исходного сервера, unit file, тестовая метка данных, внешний default route.
- Логовые маркеры: FOUR_COMPONENT_BASELINE_AND_CANDIDATE_VERIFIED_DISTINCT_CORE_ELFS; REAL_FOUR_COMPONENT_BASELINE_SYSTEMD_AND_PRIVATE_GO_HTTP_PASS; EXPECTED_CANDIDATE_ACTIVATION_FAILURE; REAL_NATIVE_ARM64_FOUR_COMPONENT_BUNDLE_ATOMIC_ROLLBACK_AND_GO_HTTP_PASS.

Успешный тестовый коммит ветки test/csqtt-250-unified-bundle-rollback-20261121: f30c740f99f007e9c7fea7fc0252a90714e9bdc5. Предыдущие runs 44894577279, 44894626890, 44894694668 имели ошибки только в тестовом сценарии (YAML и сокращённый ожидаемый SHA256) до активации systemd; остаются корректно отмеченными как FAIL, код приложения не менялся.

## Ограничения

Это проверка реального отката каталога четырёхкомпонентного пакета, а не production updater/Deploy UI и не штатная успешная активация нового Core8 в установленном пакете. Полный авторизованный сквозной Go→Rust v2.5→TUN→SOCKS5→sing-box TCP/UDP dataplane на единой сборке пока НЕ подтверждён. Физический NanoPi, VPS, Keenetic и рабочие маршруты не менялись.
