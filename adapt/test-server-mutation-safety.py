#!/usr/bin/env python3
"""Credential-free safety regression: destructive E2E must refuse unsafe targets."""
import os
import subprocess
import sys
from pathlib import Path
script = Path(__file__).with_name('test-csqtt-server-mutations-e2e.py')
base_env = {k:v for k,v in os.environ.items() if not k.startswith('CSQTT_E2E_')}
for name, url, optin in [
    ('public-host', 'https://example.com/secret', 'isolated-only'),
    ('tailnet-ip', 'https://100.71.202.95:47085/secret', 'isolated-only'),
    ('missing-optin', 'https://127.0.0.1:47002/secret', ''),
]:
    env = dict(base_env, CSQTT_E2E_BASE_URL=url, CSQTT_E2E_ALLOW_MUTATIONS=optin)
    result = subprocess.run([sys.executable, str(script)], env=env, capture_output=True, text=True, timeout=5)
    assert result.returncode != 0 and 'AssertionError' in result.stderr, (name, result.stderr)
    print('PASS reject', name)
