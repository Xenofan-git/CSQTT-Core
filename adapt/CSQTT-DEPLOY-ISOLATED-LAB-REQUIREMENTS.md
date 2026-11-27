# Изолированный стенд DeployManager: требования и приёмка

## Назначение и фактическая готовность

Этот документ задаёт оставшуюся настоящую приёмку install/update/uninstall,
автоматического rollback и обрыва SSH. Модульные tests, QEMU-user CLI,
stdio SFTP fixtures и нейтральный dpkg не заменяют этот этап.

Подготовлен отдельный offline ARM64 QEMU guest на Moscow:
 /srv/development/isolated-checkouts/csqtt-neutral-arm64-lab-20261127

MANIFEST.json содержит точные QEMU args, SHA immutable Debian base и первого
guest test script; рядом overlay.qcow2, vars.fd, seed.img, user-data/meta-data,
network-config, console.log. Guest успешно загрузился, выполнил6 нейтральных
dpkg/SQLite проверок и штатно выключился. Сеть полностью отсутствует.
Это среда пакетных испытаний; SSH endpoint ещё не подготовлен.

Существующий csqtt-deploy-lab с127.0.0.1:22239 содержит прежний Core runtime,
не является чистым install baseline и не проходит address guard.
AMD64 подготовленный guest остаётся в Debian installer без NIC.
ARMv7 имеет media/disk preflight, но законченная ОС не установлена.
Не использовать ни один рабочий VPS/NanoPi как целевой сервер установки.

## Обязательные технические условия

1. Отдельная QEMU/KVM VM с Debian/Ubuntu, systemd PID1 и root SSH.
   Для каждой серверной архитектуры — свой согласованный образ/снимок:
   x86_64; aarch64; armv7l с EABI5 hard-float и NEON для текущего кандидата.
   Наличие qemu-user или пустого QCOW2 не означает готовность guest.
2. Проверить ELF loader, libc/libstdc++/libgcc/libm и фактические symbol versions.
   Текущие три кандидата требуют GLIBC_2.39. Нужен достаточный ABI самой
   гостевой ОС, а не только build sysroot на Moscow.
3. Python3 со sqlite3, OpenSSH server/SFTP, bash/coreutils/sha256sum/stat/od,
   flock, systemctl, iproute2, curl и зависимости точного закреплённого
   установщика. /dev/net/tun доступен. Нужен рабочий guest uplink для Core
   и зависимостей установщика. IPv4 forwarding заранее настраивается
   только внутри разрешённой VM; host-global sysctl/firewall не меняются.
4. Планировать последовательный запуск одной тестовой VM. Рекомендуемый
   бюджет для полного сценария —1GiB RAM,1–2vCPU,6GiB virtual disk;
   оставить host не менее1GiB RAM и запас фактического диска под COW/snapshot.
   Это плановый бюджет, не измеренные минимальные требования продукта.
   На Moscow недостаточно доказательств для одновременного наполнения
   и запуска трёх полных VM; ресурсы проверить перед запуском.
5. Собственный доступный IPv4 guest и SSH-порт, которые проходят validTarget:
   выделенный маршрутизируемый IP либо штатно зарегистрированный отдельный
   Tailscale guest с разрешённым ACL. Для Tailscale нужна авторизация нового
   узла; не копировать ключи/identity рабочего host. Не использовать loopback,
   RFC1918, hostname, self address, присвоенный наугад публичный/служебный IP
   или перенаправление рабочего VPS как замену идентичности guest.
6. Отдельные SSH credentials и заранее независимо сверенный host key.
   StrictHostKeyChecking=yes остаётся включённым. machine-id guest уникален,
   Debian/Ubuntu OS ID и QEMU/KVM проверяются независимо. Реальные секреты
   не записывать в Git/journals. Оператор задаёт вне API:
   CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only,
   CSQTT_CORE_DEPLOY_LAB_TARGET=IP:SSH_PORT,
   CSQTT_CORE_DEPLOY_LAB_MACHINE_ID=<32 hex>.
7. Для install чистый baseline: нет Core binary/unit/config/SQLite/TUN,
   UDP47000/TCP47002 свободны. Для update — контролируемая старая установка,
   известные настройки и тестовые записи SQLite. Нет клиентского трафика:
   текущий postcheck строго сравнивает данные, включая счётчики.
8. Контроллер/панель приёмки — отдельный лабораторный экземпляр.
   Рабочую панель NanoPi не обновлять. Новый helper должен иметь проверенный
   SHA; серверный payload/installer сохраняют существующие trust pins.
   AMD64/ARMv7 не подключать одним изменением allowlist без отдельного
   рассмотрения provenance/ABI/сферы теста и последующей runtime-проверки.
9. До изменения VM сохранить snapshot/overlay, UEFI, machine-id/host-key
   fingerprints, inventory, sockets, routes, rules и hashes всех постоянных
   данных. Сторонние services/ports сравнить до/после. Снимок guest не должен
   подменять историю журналов или затрагивать диски рабочих узлов.

## Сценарии приёмки и критерии

| ID | Действие | Обязательные доказательства |
|---|---|---|
| A0 | Проверить identity, ABI, pins, зависимости и чистый baseline | Независимый inventory; неверный host key/machine-id/ELF/SHA отклоняется до мутации |
| A1 | Install через настоящий HTTP API/панель | Ровно один remote exec; фактический progress; exit0+единственный marker+postcheck; служба и ожидаемые порты реально работают |
| A2 | Задать известные config/passwords/SQLite, выполнить update | Значения/файлы/SQLite integrity+логический dump до/после совпадают; настройки не заменены полями формы; версия runtime подтверждена |
| A3 | Ошибка до изменения runtime | Старые процессы/данные не затронуты; ошибка показывается в панели; staging очищен |
| A4 | Искусственный сбой после snapshot/замены runtime | Автоматический rollback именно штатного установщика; старые binary/unit/data/service/ports восстановлены, exit30/marker и независимый postcheck; update остаётся неуспешным |
| A5 | Отказ rollback в отдельном disposable snapshot | Однозначный rollback_failed, отсутствие ложного success; диагностика и ручное восстановление только lab |
| A6 | Uninstall через панель | Удалены только принадлежащие Core runtime/unit/TUN/порты, config/passwords/SQLite сохранены, exit0+marker+postcheck |
| S1 | Потеря SSH до staging | Нет мутации и ложного успеха |
| S2 | Потеря SSH во время SFTP/до exec | Неполный payload не запускается, bounded cleanup/reconnect удаляет только собственный workspace |
| S3 | Потеря SSH после начала exec | Результат unknown/error; команда не повторяется; staging сохраняется до независимого осмотра, потом контролируемая очистка |
| S4 | Потеря ответа после фактического завершения | Нет автоматического повторного install/update; состояние выясняется независимой проверкой |
| U1 | UI на реальных A1–A6/S1–S4 | Фактические этапы и итог; ошибки не становятся успехом, completed без подтверждения остаётся неопределённым; секреты не отображаются |
| I1 | Проверка изоляции | Сторонние процессы,47000/47002 scope, Android46000/46002,46100/46102,48000/48002, routes и чужие настройки не изменились |
| C1 | Cleanup и повторный ручной запуск после выяснения исхода | Нет неизвестных активных операций, удалены только принадлежащие workspace, новый запуск не конфликтует со старым |

Инъекцию ошибки выполнять только в выделенном guest/snapshot. Не менять
production SHA, адресные/ownership проверки или проверенный installer ради
прохождения теста. Для A4 нужен предусмотренный и проверяемый внешний сбой
в окружении guest; повреждённый пакет, отклонённый до изменения runtime,
проверяет A0/A3, но не доказывает A4. Ручной downgrade также не доказывает A4.

Обрывы сети организовать на NIC/SSH-сеансе только этого guest; не менять
firewall/routes управления Moscow/Amsterdam. После S3/S4 не повторять
команду до установления исхода. Для каждого случая записать начало/конец,
точный SHA/версию, guest identity fingerprint, один exec, stdout/stderr/status,
inventory/data hashes и решение оператора. Секреты в доказательства не включать.

## Нейтральный пакет и границы доказательств

test-neutral-package-qemu-guest.py использует настоящие dpkg и SQLite в
guest без NIC; отказывается от неподходящего hostname/VM/сети/существующего
Core. Пакет — только текст, без серверного ELF и maintainer scripts.
Тестовый пароль синтетический. SQLite находится вне package payload.

Такой пакет не проходит Core asset pin и не должен проходить его:
не добавлять нейтральный SHA в approvedServerAsset, не подменять production
payload или --assets. Нейтральный dpkg не запускает Core deploy.sh.
Поэтому результаты этих ОС-проверок вести отдельной таблицей.
Для полной Core-приёмки требуется настоящий закреплённый Core пакет
и все условия A0–C1.

## Откат лабораторной среды

При ошибке остановить испытание и сохранить console.log/result/inventory.
Остановить только PID/QMP данной VM; проверить её имя и пути диска.
Восстановить её собственный overlay/UEFI из сохранённого снимка либо создать
новый COW overlay поверх неизменённого base. Никакого отката дисков/сетей/
служб Moscow, Amsterdam, NanoPi или соседних проектов.

Матрица всех текущих результатов:
[CSQTT-DEPLOY-ACCEPTANCE-MATRIX-2026-11-27.md](CSQTT-DEPLOY-ACCEPTANCE-MATRIX-2026-11-27.md).
