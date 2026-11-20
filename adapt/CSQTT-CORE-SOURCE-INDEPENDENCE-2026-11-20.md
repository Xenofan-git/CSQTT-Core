# CSQTT Core — source independence migration ledger (2026-11-20)

> **ИСТОРИЧЕСКИЙ АУДИТ, НЕ ТЕКУЩИЙ ПЛАН РАБОТ.** Разделы с
> описанием миграций 570e690, 1ffe740, 4aeff5c и 3728052 относятся к
> экспериментальным изменениям, отменённым корректирующим коммитом после
> уточнения пользователя. Не использовать их как перечень функций
> текущей сборки. Цель была заменить видимое название на **CSQTT Core**,
> а не переписывать Go/Linux, Deploy или Flutter. Версия основы — 2.5.0.

**Product name:** CSQTT Core. External repository and compatibility identifiers
below are quoted only when necessary for reproducible provenance, licensing,
wire interoperability or existing installations.

**Target:** CSQTT Core-owned client/server and independent sing-box, retaining
only the automatic VK-token acquisition METHOD from Endlad2/LaLune.
Current code does not meet this target; user-visible renaming is not proof
of source independence.

## Source-dependency matrix

| Boundary | Current implementation / evidence | Status |
| --- | --- | --- |
| Native seven-tab UI | adapt/csqtt-panel-preview.html | Owned source, browser/contract tested |
| Web packaging | adapt/package-csqtt-core-web.py stages only our index.html and api.js into csqtt-core-web-dist/ | Independent of Flutter |
| Linux Go backend | adapt/headless_main.go imports lalune-desktop/Libs; full CI compiles patched upstream-lalune/Desktop/Linux | Still external source dependent |
| Linux TUN/Direct/SOCKS5 | apply-pinned-original-linux-arm64-policy.py patches upstream Linux client; apply-csqtt-core-socks5.py adds owned SOCKS5 | Still external source dependent |
| DeployManager | Integrated build compiles pinned Core/DeployManager and retains csqtt-lalune service identifiers | Not yet independently migrated |
| Rust CSQTT Core | Separate CSQTT 2.5.0 derivations and verified native ARM64 tests | Actual new ELF VM upgrade/forced rollback pending |
| VK token/CAPTCHA | Pinned token fetching and bridge; wire keys remain for compatibility | Intended retained method |
| Legacy identifiers | lalune-desktop/Libs, csqtt-lalune.service, CSQTT_LALUNE_*, old CAPTCHA fragment keys | Do not blindly replace on deployed systems |

## Completed in this step

1. Removed Flutter setup and Flutter web compiler from full Go/Linux ARM64 CI.
   Compiled Flutter index.html had been overwritten by the first-party panel.
2. Added dependency-free packaging from adapt/csqtt-panel-preview.html and
   adapt/nanopi.js into csqtt-core-web-dist/ OUTSIDE the external checkout,
   then copied those same bytes into Go embedded frontend/.
3. Added adapt/test-csqtt-core-web-package.py checking exact source bytes,
   idempotence, refusal of symlinked/mixed Flutter assets and CI boundary.
   Integrated ARM64 CI still needs to confirm actual linked binary behavior.
4. Removed the obsolete third-party updater/release-link operations from
   adapt/nanopi.js. CSQTT Core update handling remains. CAPTCHA legacy
   transport keys are intentionally retained until both ends migrate together.

## Deduplication rule

Past successful CI proves only the implementation and commit SHA it ran.
Native HTML/JS edits require focused browser and packaging tests, not complete
repeat of all VM failure cases. Actual Linux Go workflow edits require one
integrated ARM64 CI. New Rust binaries require the still-unperformed isolated
guest binary install/forced rollback test, not replaying mock rollback tests.

## Next migration step completed in this change

Removed unused Flutter/Dart mutations from the Linux Go adapter and deleted
the obsolete deploy-page patch script. This does NOT remove the external Go
runner, Go Libs or DeployManager. The new static no-Dart test protects the
boundary and checks that the real Go Direct features remain.

## New HTTP/Deploy boundary extracted

The CSQTT Core headless HTTP server now has NO direct Go import from the
external Libs module. Device-ID parsing accepts a narrow settings reader
interface (including typed nil fail-closed), and the three Deploy handlers
call a CSQTT Core-owned Deploy backend interface. A separate temporary
csqtt_core_deploy_bridge_linux.go is the ONLY Go import of that external
Libs package for deployment. It currently delegates exactly to the
previous DeployProtocol, DeployLog and DeployBusy functions; operational
behavior is intentionally unchanged. New Go tests exercise the three
HTTP endpoints against a fixture backend and reject requests from public
IP addresses without invoking deployment. The separate source guard
enforces that boundary in fast CI; Go unit/race tests still require the
integrated Go Linux pipeline.

This does NOT remove the compiled Go/Linux runner/Libs nor DeployManager;
it merely makes the HTTP panel replaceable and confines one category of
external dependency. Do not describe it as a full independent backend.

## Owned CSQTT Core Deploy preflight (new boundary)

The CSQTT Core Deploy adapter now owns request parsing and fail-closed
validation for CSQTT-specific input: endpoint syntax, SSH user, port range
and collisions, Docker mode, installer secrets, web credentials and device
ID. Errors are fixed diagnostics with no submitted SSH or web credentials
in the log. The adapter forwards the **unmodified** valid JSON request to
the previous Deploy runner, which still performs its own target-DNS/IP
checks and remote SSH/SCP/installer logic. Uninstall remains possible
without new web credentials; other protocol behavior is unchanged.

The own Go test exercises a table of invalid/valid requests and proves
invalid input never calls the injected transport. Actual Go compilation
and execution in integrated ARM64 CI must be verified separately. This
does **not** mean the old Go Deploy transport or external Rust
DeployManager have been removed.

## Next work (not yet done)

1. Replace Go Libs and external Linux runner with CSQTT Core-owned module,
   preserving API/VK token behavior and test contracts.
2. Remove unused Flutter/Dart mutation from the Linux adaptation patcher,
   then eliminate the broad external source checkout dependency.
3. Replace the legacy DeployManager and safely migrate owned runtime names
   with explicit service/config/database rollback and conflict detection.
4. Verify real SOCKS5/TUN/Direct/DNS routing, native Rust upgrade and
   fail-closed rollback in isolated ARM64 guest, then hardware acceptance.

No production NanoPi, Keenetic, HydraRoute Core, DNS or global route changes.

## Tests: no repeated full cycle for every change

See [CSQTT Core test evidence and selective impact ledger](CSQTT-CORE-TEST-EVIDENCE-2026-11-21.md).
It records exact successful run IDs and commits, marks incomplete real
guest rollback/live CRUD checks as pending, and defines which changes need
Go/Rust/Deploy/network re-validation. The focused browser CI reads this
ledger and self-tests the decision rules. Documentation changes do not
trigger the full ARM64 build. Full release acceptance remains mandatory
on the final frozen SHA.

## Зафиксированная основа продукта (21 ноября 2026)

CSQTT Core основан на **оригинальном CSQTT 2.5.0** — это принятое ранее
решение после первого этапа на 2.1.9. Остаточные зависимости старого
интегрированного Go/Linux workflow от 2.1.9 — наследие, которое требуется
перевести на 2.5.0, а не параллельная выбранная версия продукта.

В проекте уже используются исходники оригинального сервера 2.5.0 и
адаптация его штатного Deploy. Переделка Deploy с нуля не нужна без
выявленного функционального пробела. Любая замена старых рабочих
бинарников требует проверки совместимости и обратимого отката.
