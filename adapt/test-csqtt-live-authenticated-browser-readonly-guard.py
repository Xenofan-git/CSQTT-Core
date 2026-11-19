#!/usr/bin/env python3
"""Offline refusal contract for opt-in authenticated isolated ARM64 lab browser.

Run on GitHub without any SSH key, lab endpoint connection, or credentials.
"""
from pathlib import Path
import ast
import os
import subprocess
import sys

script=Path(__file__).with_name("test-csqtt-live-authenticated-browser-readonly.py")
source=script.read_text()
ast.parse(source)
for required in (
    "dedicated-arm64-lab-only",
    'url.scheme != "https"',
    '100.64.0.0/10',
    'parts.hostname != urlsplit(base).hostname',
    'method=="POST"',
    'f"/{secret}/api/login"',
    'route.abort()',
    'set(allowed_fixed[:5]).issubset(successful_gets)',
    'logout=context.request.post(url+"api/logout",timeout=6500)',
    'context.close()',
    "ignore_https_errors=True",
    "service_workers=\"block\"",
    'assert not prohibited',
    'print("FAIL read-only live browser acceptance:",type(exc).__name__)',
):
    assert required in source,required

base=[sys.executable,str(script),"--ssh-key","/nonexistent/csqtt-acceptance-key",
      "--panel-origin","https://127.0.0.1:47362"]

def refused(args, optin):
    env=os.environ.copy()
    env.pop("CSQTT_READONLY_LIVE_ACCEPTANCE",None)
    if optin:
        env["CSQTT_READONLY_LIVE_ACCEPTANCE"]="dedicated-arm64-lab-only"
    r=subprocess.run(args,env=env,text=True,capture_output=True,timeout=12)
    assert r.returncode!=0,(args,r.returncode)
    assert "FAIL read-only live browser acceptance: ValueError" in r.stdout
    assert "Traceback" not in r.stdout+r.stderr
    assert "password" not in r.stdout.lower()
    assert "secret" not in r.stdout.lower()
    return True

refused(base,False)           # No opt-in: must stop before SSH/browser.
refused(base,True)            # No key: must stop before SSH/browser.
for forbidden in (
    "http://127.0.0.1:47362",
    "https://example.org:47362",
    "https://127.0.0.1:47362/secret-path",
    "https://user:pass@127.0.0.1:47362",
    "https://127.0.0.1",
):
    refused(base[:-1]+[forbidden],True)
print("PASS live authenticated browser refuses no opt-in, no key and invalid/external origins")
print("PASS no live API access attempted by offline safety checks")
