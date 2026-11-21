# CSQTT Core — три заключительных испытания, 21 ноября 2026

## 1. Физический NanoPi R3S LTS (RK3566) — PASS

Нативный (без QEMU) verified Rust Core ARM64 №7 SHA256 3295c38ffdca3011f91990a16eff9bcd4d25df73b9cd49b3cc59d50d1ddfc7fb: два запуска временной независимой user/network namespace, HTTPS, API 32/32 + 32/32, реальный TUN, NAT, SIGKILL между запусками и корректное восстановление, отсутствие дублей, полная штатная очистка.

После точечного исправления маршрутизатора новый verified Rust Core №8 (commit 91b1540), SHA256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706, также нативно проверен на NanoPi: HTTPS, TUN, iptables, штатная очистка PASS. Все временные файлы удалены. Default route 192.168.4.1, M-UI, HydraRoute Core и Tailscale не менялись. Никакая постоянная CSQTT-служба на NanoPi не устанавливалась.

## 2. ARM64 systemd Deploy, принудительный отказ и rollback — PASS

GitHub Actions: https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44890007663 — SUCCESS на disposable native Ubuntu ARM64 runner.

Реальный неизменённый installer CSQTT Core: verified старый ELF 10aa3f1 установлен с systemd, service active/enabled, HTTPS доступен. Попытка обновления до verified Rust Core №7 (21c59ba) с принудительным отказом systemctl start нового кандидата после cutover. Восстановлены старый ELF, unit, env, данные, active/enabled и прежний HTTPS; default route не изменён. Это тест отката релиза №7; отдельной live systemd установки исправленного релиза №8 не было.

## 3. Оригинальный клиент 2.5.0 против исправленного Core №8: настоящий TURN/DTLS/SRTP/ICMP — PASS

GitHub Actions: https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44891239516 — SUCCESS, 1 passed; 0 failed.

Клиент интеграционного теста собран из оригинального CSQTT 2.5.0 (upstream fd936ef087c34cc46f26111dc2361f43b8b03c07). Сервер — verified ARM64 Core №8 release run44890803542, исправленный commit 91b154011ca16c219f57eef99462d3408dccfeeb и SHA256 ELF a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706. В отдельной native ARM64 runner namespace: локальный тестовый TURN, настоящий DTLS handshake, SRTP, GETCONF/TUNCONF, worker ready, обратный ICMP-пакет появился в исходном клиентском dispatcher.

Исправленная причина: первоначальный Core менял строковую сеть с 10.66.67/24 на 10.67.69/24, но raw SUBNET_PREFIX в rust-server/tun_device.rs оставался [10,66,67]. RouteTable::route_index отвергал обратную доставку клиенту 10.67.69.2. Commit 91b1540 изменил только Core-generating prefix и связанные unit-test vectors, добавлен защитный тест. Original client fixture для проверки направляет пакет на TUN 10.67.69.1 (меняются только данные теста). Core №7 realpair 44890243962 правильно отражает исторический FAIL; исправленный Core №8 realpair 44891239516 PASS.

## Ограничения

Три указанных испытания выполнены и имеют PASS в своих изолированных условиях. Это НЕ подтверждение полной Go/sing-box/Desktop/Mobile интеграции и НЕ разрешение делать CSQTT глобальным маршрутом на рабочем NanoPi. Рабочие политики и службы NanoPi/Keenetic/HRCore/Tailscale не менялись.
