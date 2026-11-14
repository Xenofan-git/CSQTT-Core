#!/usr/bin/env python3
"""Deterministic inventory of upstream CSQTT 2.5 server panel vs Core preview.

This is an inventory, not a claim that functionality is implemented.
"""
import argparse
import re
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--upstream', required=True, type=Path)
args = parser.parse_args()
upstream = args.upstream.read_text()
preview = (Path(__file__).parent / 'csqtt-server-panel-preview.html').read_text()

def unique(pattern, source):
    return sorted(set(re.findall(pattern, source)))

routes = unique(r'\.route\("(/api/[^"{}]*)"', upstream)
handlers = unique(r'\b(?:onclick|onchange|oninput)="([a-zA-Z_]\w*)\(', upstream)
dialogs = unique(r'<dialog\s+id="([^"]+)"', upstream)
preview_dialogs = unique(r'<dialog\s+id="([^"]+)"', preview)
upstream_tabs = unique(r'data-tab="([^"]+)"', upstream)
preview_tabs = unique(r'data-tab="([^"]+)"', preview)

lines = [
    '# CSQTT 2.5.0 → Core: проверяемый реестр элементов серверной панели',
    '',
    'Источник: закреплённый оригинальный `rust-server/web_panel.rs` v2.5.0. '
    'Сравнение с `adapt/csqtt-server-panel-preview.html`.',
    '',
    '**Важно:** наличие строки или кнопки не доказывает работающий backend. '
    'Реализация операций и E2E проверяются отдельно.',
    '',
    f'- API-маршрутов без динамических сегментов: **{len(routes)}**',
    f'- Обработчиков событий в HTML оригинала: **{len(handlers)}**',
    f'- Диалогов оригинала: **{len(dialogs)}**; в preview: **{len(preview_dialogs)}**',
    f'- Идентификаторы вкладок оригинала: {", ".join(upstream_tabs)}',
    f'- Идентификаторы вкладок preview: {", ".join(preview_tabs)}',
    '',
    '## Диалоги оригинала', '',
    '| Диалог | В preview по ID |', '|---|---|',
]
for name in dialogs:
    lines.append(f'| `{name}` | {"Да" if name in preview_dialogs else "Нет"} |')
lines += ['', '## Оригинальные обработчики событий', '', '| Обработчик | Присутствует как текст в preview |', '|---|---|']
for name in handlers:
    lines.append(f'| `{name}` | {"Да" if name in preview else "Нет"} |')
lines += ['', '## API-маршруты оригинала (статические)', '', '| Метод/путь | Упоминание в preview |', '|---|---|']
for name in routes:
    lines.append(f'| `{name}` | {"Да" if name in preview else "Нет"} |')
lines += ['', '## Дополнительная проверка', '',
          'Динамические маршруты с `{id}` / `{password}`, HTTP-методы, форматы JSON, '
          'состояние сервера, скрытые условия и полномочия требуют отдельной '
          'семантической сверки и тестов; этот список не заменяет их.', '']
output = Path(__file__).parent / 'CSQTT-250-SERVER-PANEL-ELEMENT-INVENTORY.md'
output.write_text('\n'.join(lines))
print(f'upstream routes={len(routes)} handlers={len(handlers)} dialogs={len(dialogs)} preview_dialogs={len(preview_dialogs)}')
