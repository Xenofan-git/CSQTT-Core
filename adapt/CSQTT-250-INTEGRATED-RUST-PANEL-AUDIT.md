# Аудит встроенной Rust-панели CSQTT 2.5.0

- Файл runtime: `/srv/development/isolated-checkouts/csqtt-arm64-build/core-250/rust-server/web_panel.rs`
- Эталон HTML: `adapt/csqtt-server-panel-upstream-250.html`
- HTML побайтово совпадает: **True**

| Элемент | Эталон | Runtime | Отсутствует |
|---|---:|---:|---|
| dialogs | 19 | 19 | нет |
| handlers | 71 | 71 | нет |
| tabs | 4 | 4 | нет |

**Ограничение:** совпадение HTML не подтверждает API-операции или браузерную работоспособность. Ранее созданный `CSQTT-250-SERVER-PANEL-ELEMENT-INVENTORY.md` сравнивает со старым preview и не отражает состояние встроенной Rust-панели.

**Следующие тесты:** authenticated API, CRUD клиентов/групп, импорт/экспорт, секретная ссылка, desktop/mobile Playwright на живом Rust runtime.
