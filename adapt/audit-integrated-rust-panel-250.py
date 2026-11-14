#!/usr/bin/env python3
"""Compare actual ARM64 Rust panel sources with pinned CSQTT 2.5.0 asset."""
import argparse
from pathlib import Path
import re
p=argparse.ArgumentParser()
p.add_argument('--rust-dir',type=Path,required=True)
a=p.parse_args()
original=Path('adapt/csqtt-server-panel-upstream-250.html').read_text()
actual=(a.rust_dir/'csqtt-core-panel.html').read_text()
rust=(a.rust_dir/'web_panel.rs').read_text()
assert 'const PANEL_HTML: &str = include_str!("csqtt-core-panel.html")' in rust
patterns={'dialogs':r'<dialog\s+id="([^"]+)"','handlers':r'\b(?:onclick|onchange|oninput)="([a-zA-Z_]\w*)\(','tabs':r'data-tab="([^"]+)"'}
lines=['# Аудит встроенной Rust-панели CSQTT 2.5.0','',f'- Файл runtime: `{a.rust_dir}/web_panel.rs`','- Эталон HTML: `adapt/csqtt-server-panel-upstream-250.html`',f'- HTML побайтово совпадает: **{actual==original}**','', '| Элемент | Эталон | Runtime | Отсутствует |','|---|---:|---:|---|']
for label,pat in patterns.items():
    old=set(re.findall(pat,original));new=set(re.findall(pat,actual));lines.append(f'| {label} | {len(old)} | {len(new)} | {", ".join(sorted(old-new)) or "нет"} |')
lines+=['','**Ограничение:** совпадение HTML не подтверждает API-операции или браузерную работоспособность. Ранее созданный `CSQTT-250-SERVER-PANEL-ELEMENT-INVENTORY.md` сравнивает со старым preview и не отражает состояние встроенной Rust-панели.','', '**Следующие тесты:** authenticated API, CRUD клиентов/групп, импорт/экспорт, секретная ссылка, desktop/mobile Playwright на живом Rust runtime.','']
Path('adapt/CSQTT-250-INTEGRATED-RUST-PANEL-AUDIT.md').write_text('\n'.join(lines))
print('\n'.join(lines))
