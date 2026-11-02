# CSQTT–sing-box for Debian/Ubuntu ARM64 — Gateway notes

Этот каталог содержит отдельный legacy-компонент LaLune Gateway. Он не является реализацией sing-box и не должен смешиваться с основной CSQTT/LaLune адаптацией или существующими сетевыми сервисами.

## Изоляция legacy Gateway

Owned resources: `csqtt0`, `lalune-wg`, routing table `200`, ip rule priority `22000`, NAT chain `LALUNE_POST`, UDP `51821` and panel `18787`.

The main routing table, `/etc/resolv.conf`, `xray0`, 3x-ui, HydraRoute and Tailscale are not changed.

Path: Keenetic policy -> WireGuard -> `lalune-wg` -> table `200` -> `csqtt0` -> CSQTT/VK TURN.

Keenetic WireGuard endpoint: `192.168.4.26:51821`. Tunnel subnet: `10.77.0.0/30`. Keenetic address: `10.77.0.2`.

## Важные ограничения

- Не запускать этот Gateway как замену sing-box.
- Не переносить его маршруты и ресурсы в новую архитектуру без отдельного аудита.
- Не менять main routing table, DNS resolver, 3x-ui/Xray, HydraRoute или Tailscale.
- Перед любым использованием сверять актуальный код и изоляционные проверки.

Основная документация проекта: [README](../README.md).
