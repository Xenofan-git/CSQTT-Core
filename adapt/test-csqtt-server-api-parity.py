#!/usr/bin/env python3
"""Read-only contract audit of pinned CSQTT 2.5.0 server panel and Core preview.

Requires CSQTT_UPSTREAM_WEB_PANEL to point to a locally downloaded original source.
Does not contact a server or exercise privileged API actions.
"""
import os
import re
from pathlib import Path

upstream_path = os.environ.get('CSQTT_UPSTREAM_WEB_PANEL')
if not upstream_path:
    raise SystemExit('Set CSQTT_UPSTREAM_WEB_PANEL to the pinned original web_panel.rs')
upstream = Path(upstream_path).read_text()
preview = (Path(__file__).parent / 'csqtt-server-panel-preview.html').read_text()
required = {
    'authentication': ('/api/login', '/api/logout'),
    'monitoring': ('/api/stats',),
    'clients': ('/api/clients', '/api/groups'),
    'logs': ('/api/logs', '/api/logs/toggle', '/api/logs/clear', '/api/logs/download'),
    'settings': ('/api/settings', '/api/settings/dns', '/api/settings/restart-interval', '/api/settings/master-password'),
    'local proxy': ('/api/local-proxy', '/api/local-proxy/deactivate'),
}
routes = set(re.findall(r'\.route\(\s*"([^"]+)"', upstream))
missing = [(section, path) for section, paths in required.items() for path in paths if path not in routes]
assert not missing, f'Original CSQTT 2.5.0 missing expected routes: {missing}'
assert 'csrf' in upstream.lower() or 'SameSite=Lax' in upstream
assert 'HttpOnly' in upstream and 'Secure' in upstream
assert 'auth_middleware' in upstream
assert 'x-csqtt-stealth-rst' in upstream
assert "get('connect') === '1'" in preview
assert "credentials: 'same-origin'" in preview
assert "method: 'GET'" in preview
assert "statsBase + 'api/stats'" in preview
assert "statsBase + 'api/logs'" in preview
assert "fetchList('api/clients')" in preview
assert "fetchList('api/groups')" in preview
assert 'async fn clients_get' in upstream
assert 'async fn groups_get' in upstream
assert 'async fn logs_get' in upstream
assert "statsBase + 'api/login'" in preview
assert 'caesarEncode' in preview
assert 'x-csqtt-stealth-rst' in preview
assert 'const authorized = await refreshStats()' in preview
assert 'Предварительный интерфейс' in preview
assert preview.count('disabled') >= 8
print('Original server API contract: OK; sections:', ', '.join(required))
print('Core preview integration: opt-in read-only monitoring; login and read-only stats integrated in opt-in preview; privileged actions NOT IMPLEMENTED')
