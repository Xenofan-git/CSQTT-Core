#!/usr/bin/env python3
"""Destructive E2E: disposable isolated original CSQTT backend ONLY.
Explicit opt-in and loopback host required. Never run against production.
"""
import base64
import copy
import json
import os
import ssl
import urllib.error
import urllib.parse
import urllib.request
from urllib.parse import urlsplit

base = os.environ['CSQTT_E2E_BASE_URL'].rstrip('/')
assert os.environ.get('CSQTT_E2E_ALLOW_MUTATIONS') == 'isolated-only'
assert urlsplit(base).hostname in ('127.0.0.1', 'localhost')
password = os.environ['CSQTT_E2E_WEB_PASS']
username = os.environ['CSQTT_E2E_WEB_USER']
ctx = ssl._create_unverified_context()
cookie = ''

def req(path, method='GET', data=None):
    global cookie
    payload = json.dumps(data).encode() if data is not None else None
    request = urllib.request.Request(base + path, data=payload, method=method,
        headers={'Content-Type': 'application/json', 'Cookie': cookie})
    try:
        response = urllib.request.urlopen(request, context=ctx, timeout=15)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        body = response.read()
        if response.headers.get('Set-Cookie', '').startswith('csqtt_session='):
            cookie = response.headers['Set-Cookie'].split(';')[0]
        try: parsed = json.loads(body)
        except ValueError: parsed = body.decode(errors='replace')[:250]
        return response.status, parsed

def verify(label, ok):
    print(('PASS ' if ok else 'FAIL ') + label, flush=True)
    assert ok, label

encode = lambda v: 'c1:' + base64.b64encode(bytes((b+47)%256 for b in v.encode())).decode()
s, _ = req('/api/login', 'POST', {'user':encode(username), 'pass':encode(password)})
verify('login', s == 200 and bool(cookie))
s, baseline = req('/api/export')
verify('baseline export', s == 200 and baseline['version'] == 1)
s, settings = req('/api/settings')
verify('baseline settings', s == 200 and isinstance(settings, dict))
client_pw = 'E2EClientSecret9001'
import_pw = 'E2EImportedSecret9002'
client_path = '/api/clients/' + urllib.parse.quote(client_pw, safe='')
import_path = '/api/clients/' + urllib.parse.quote(import_pw, safe='')
created = []
try:
    s, created_client = req('/api/clients','POST',{'name':'E2E original client','days':3,'password':client_pw,'workers':3})
    verify('client create', s == 200 and created_client.get('password') == client_pw)
    created.append(client_path)
    s, clients = req('/api/clients')
    verify('client read', s == 200 and any(c['password']==client_pw for c in clients))
    s, updated = req(client_path,'POST',{'name':'E2E edited client','days':5,'workers':4})
    verify('client update', s == 200)
    s, clients = req('/api/clients')
    verify('updated client persisted', s == 200 and any(c['password']==client_pw and c['name']=='E2E edited client' and c['workers']==4 for c in clients))
    s, changed = req('/api/settings','POST',{'auto_restart_interval_hours':12})
    verify('settings mutation', s == 200)
    s, new_settings = req('/api/settings')
    verify('settings persisted', s == 200 and new_settings['auto_restart_interval_hours']==12)
    s, export = req('/api/export')
    verify('export after mutation', s==200 and any(c['password']==client_pw for c in export['clients']))
    imported = copy.deepcopy(export)
    imported_client = copy.deepcopy(next(c for c in imported['clients'] if c['password']==client_pw))
    imported_client['password'] = import_pw
    imported_client['name'] = 'E2E imported client'
    imported_client['sub_uuid'] = ''
    imported['clients'] = [imported_client]
    imported['groups'] = {}
    imported['routing_rules'] = {}
    s, outcome = req('/api/import','POST',{'config':imported,'import_clients':True,'import_settings':False})
    verify('import actual client mutation', s == 200 and outcome.get('added_clients')==1)
    created.append(import_path)
    s, clients = req('/api/clients')
    verify('imported client persisted without removing existing', s == 200 and all(any(c['password']==p for c in clients) for p in (client_pw,import_pw)))
    s, unchanged = req('/api/settings')
    verify('import clients-only preserved settings', s==200 and unchanged['auto_restart_interval_hours']==12)
finally:
    for path in reversed(created):
        s, _ = req(path,'DELETE')
        verify('cleanup client', s == 200)
    s, _ = req('/api/settings','POST',{'auto_restart_interval_hours':settings['auto_restart_interval_hours']})
    verify('restore settings', s==200)
    s, after = req('/api/export')
    print('RESTORE CHECK', {'baseline_interval':baseline['auto_restart_interval_hours'],'after_interval':after['auto_restart_interval_hours'],'baseline_count':len(baseline['clients']),'after_count':len(after['clients'])},flush=True)
    verify('baseline clients and settings restored', s==200 and
        {c['password'] for c in after['clients']} == {c['password'] for c in baseline['clients']} and
        after['auto_restart_interval_hours']==baseline['auto_restart_interval_hours'])
print('MUTATION E2E COMPLETE', flush=True)
