# CSQTT 2.5.0 → Core: проверяемый реестр элементов серверной панели

Источник: закреплённый оригинальный `rust-server/web_panel.rs` v2.5.0. Сравнение с `adapt/csqtt-server-panel-preview.html`.

**Важно:** наличие строки или кнопки не доказывает работающий backend. Реализация операций и E2E проверяются отдельно.

- API-маршрутов без динамических сегментов: **25**
- Обработчиков событий в HTML оригинала: **81**
- Диалогов оригинала: **21**; в preview: **2**
- Идентификаторы вкладок оригинала: clients, logs, monitoring, settings
- Идентификаторы вкладок preview: clients, logs, monitor, settings

## Диалоги оригинала

| Диалог | В preview по ID |
|---|---|
| `apiDocsDlg` | Да |
| `batchConfirmDlg` | Нет |
| `confirmDlg` | Нет |
| `cryptoDonateDlg` | Нет |
| `editClientModal` | Нет |
| `editDlg` | Нет |
| `editRoutingRuleDlg` | Нет |
| `groupDlg` | Нет |
| `importExportDlg` | Да |
| `linksAndTokensDlg` | Нет |
| `localProxyInfoDlg` | Нет |
| `newClientModal` | Нет |
| `newDlg` | Нет |
| `newGroupDlg` | Нет |
| `profileDlg` | Нет |
| `qrDlg` | Нет |
| `rebootDlg` | Нет |
| `routingRulesDlg` | Нет |
| `templateDomainsDlg` | Нет |
| `vkHashesInfoDlg` | Нет |
| `vkTokenSettingsDlg` | Нет |

## Оригинальные обработчики событий

| Обработчик | Присутствует как текст в preview |
|---|---|
| `activateProxy` | Нет |
| `applyHubVkHashesDirect` | Нет |
| `applyHubVkTokenDirect` | Нет |
| `applyHubWorkersDirect` | Нет |
| `autoSaveHubVkHashes` | Нет |
| `autoSaveHubVkToken` | Нет |
| `changeUpdateInterval` | Нет |
| `clearGroupSearch` | Нет |
| `clearHubSearch` | Нет |
| `clearLogs` | Нет |
| `clearMainSearch` | Нет |
| `clearVkTokenSettings` | Нет |
| `copyCardLink` | Нет |
| `copyHubClientLink` | Нет |
| `copyPasswordText` | Нет |
| `copyQrDlgLink` | Нет |
| `copyQrLink` | Нет |
| `copyText` | Нет |
| `createClient` | Нет |
| `createClientInCurrentGroup` | Нет |
| `createGroup` | Нет |
| `createOrCopyGroupAdminLink` | Нет |
| `deactivateProxy` | Нет |
| `deleteCurrentGroup` | Нет |
| `deleteCurrentRoutingRule` | Нет |
| `deleteGroupAdminLink` | Нет |
| `deleteProxy` | Нет |
| `deleteRoutingRule` | Нет |
| `executeDelete` | Нет |
| `executeDeleteClient` | Нет |
| `executeExportCopy` | Нет |
| `executeExportDownload` | Нет |
| `executeExtend30` | Нет |
| `executeImportFromClipboard` | Нет |
| `executeReboot` | Нет |
| `executeResetTraffic` | Нет |
| `executeToggle` | Нет |
| `executeToggleClient` | Нет |
| `filterTemplateDomains` | Нет |
| `handleGroupSearch` | Нет |
| `handleHubClientCbChange` | Нет |
| `handleHubSearch` | Нет |
| `handleImportFile` | Нет |
| `handleMainSearch` | Нет |
| `handleSearch` | Нет |
| `logout` | Нет |
| `onRuleDomainsModeChange` | Нет |
| `openApiDocsDlg` | Да |
| `openCryptoDonateDlg` | Нет |
| `openEditClientDlgByPassword` | Нет |
| `openEditModal` | Нет |
| `openEditRoutingRuleDlg` | Нет |
| `openGroupDlg` | Нет |
| `openImportExportDlg` | Да |
| `openLinksAndTokensDlg` | Нет |
| `openNewClientDlg` | Нет |
| `openNewClientModal` | Нет |
| `openNewGroupDlg` | Нет |
| `openProfileDlg` | Нет |
| `openQrModalForActiveClient` | Нет |
| `openQrModalForClient` | Нет |
| `openRoutingRulesDlg` | Нет |
| `openTemplateDomainsDlg` | Нет |
| `promptBatchAction` | Нет |
| `saveAutoRestartInterval` | Нет |
| `saveClientChanges` | Нет |
| `saveDnsSettings` | Нет |
| `saveMainPassword` | Нет |
| `saveProfileDlg` | Нет |
| `saveRoutingRule` | Нет |
| `saveVkTokenSettings` | Нет |
| `selectAllRuleTargets` | Нет |
| `submitNewClient` | Нет |
| `switchApiDocsTab` | Нет |
| `switchImportExportTab` | Нет |
| `switchTab` | Нет |
| `toggleGroupInlineEdit` | Нет |
| `toggleLoggingActive` | Нет |
| `toggleRuleTargetItem` | Нет |
| `toggleSelectAllHubClients` | Нет |
| `toggleTheme` | Да |

## API-маршруты оригинала (статические)

| Метод/путь | Упоминание в preview |
|---|---|
| `/api/clients` | Да |
| `/api/clients/batch-delete` | Нет |
| `/api/clients/batch-extend` | Нет |
| `/api/clients/batch-group` | Нет |
| `/api/clients/batch-toggle` | Нет |
| `/api/diagnostics/dpi` | Нет |
| `/api/export` | Да |
| `/api/groups` | Да |
| `/api/import` | Да |
| `/api/links` | Нет |
| `/api/links/batch-vk` | Нет |
| `/api/local-proxy/deactivate` | Нет |
| `/api/login` | Нет |
| `/api/logout` | Нет |
| `/api/logout_all` | Нет |
| `/api/logs` | Да |
| `/api/logs/clear` | Нет |
| `/api/logs/download` | Нет |
| `/api/logs/toggle` | Нет |
| `/api/memory/trim` | Нет |
| `/api/reboot` | Нет |
| `/api/settings` | Да |
| `/api/settings/dns` | Нет |
| `/api/stats` | Да |
| `/api/template-domains` | Нет |

## Дополнительная проверка

Динамические маршруты с `{id}` / `{password}`, HTTP-методы, форматы JSON, состояние сервера, скрытые условия и полномочия требуют отдельной семантической сверки и тестов; этот список не заменяет их.
