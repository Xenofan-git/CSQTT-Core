#!/usr/bin/env python3
"""Exercise the real destructive API E2E script using in-process mock responses."""
import json
import io
from contextlib import redirect_stdout
import os
import runpy
from pathlib import Path
from unittest import mock
import urllib.parse
import urllib.request

SCRIPT = Path(__file__).with_name("test-csqtt-original-server-e2e.py")


class FakeResponse:
    def __init__(self, status=200, body=None, headers=None):
        self.status = status
        self.headers = headers or {}
        if isinstance(body, (dict, list)):
            self.body = json.dumps(body).encode()
        elif body is None:
            self.body = b"{}"
        elif isinstance(body, str):
            self.body = body.encode()
        else:
            self.body = body

    def read(self):
        return self.body

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False


class FakeOpener:
    def __init__(self, export_fails=False):
        self.export_fails = export_fails
        self.group = None
        self.creates = 0
        self.deletes = 0
        self.cookie_seen = False

    def open(self, req, timeout=None):
        assert timeout == 10
        path = urllib.parse.urlsplit(req.full_url).path.removeprefix("/secret")
        method = req.get_method()
        if method != "POST" or path != "/api/login":
            if req.get_header("Cookie") == "csqtt_session=test":
                self.cookie_seen = True
        if (method, path) == ("GET", "/api/stats"):
            if not self.cookie_seen:
                return FakeResponse(401, {"error": "unauthorized"})
            return FakeResponse(200, {"status": "Active"})
        if (method, path) == ("GET", "/"):
            if not self.cookie_seen:
                return FakeResponse(200, "<!DOCTYPE html><title>Login</title>")
            return FakeResponse(200, "<!DOCTYPE html>apiDocsDlg cryptoDonateDlg")
        if (method, path) == ("POST", "/api/login"):
            return FakeResponse(200, {"ok": True}, {"Set-Cookie": "csqtt_session=test; HttpOnly; Secure"})
        if (method, path) == ("GET", "/api/clients"):
            return FakeResponse(200, [])
        if (method, path) == ("GET", "/api/groups"):
            return FakeResponse(200, [self.group] if self.group else [])
        if (method, path) == ("POST", "/api/groups"):
            self.creates += 1
            data = json.loads(req.data)
            assert data["name"].startswith("E2E test ")
            self.group = {"id": "grp_mock_test", "name": data["name"]}
            return FakeResponse(200, {"id": self.group["id"]})
        if (method, path) == ("DELETE", "/api/groups/grp_mock_test"):
            self.deletes += 1
            self.group = None
            return FakeResponse(200, {"ok": True})
        if (method, path) == ("GET", "/api/export"):
            if self.export_fails:
                return FakeResponse(503, {"error": "injected"})
            return FakeResponse(200, {"version": 1, "groups": {}, "clients": []})
        if (method, path) == ("POST", "/api/import"):
            data = json.loads(req.data)
            assert data["import_clients"] is False and data["import_settings"] is False
            return FakeResponse(200, {"ok": True})
        if (method, path) in (("GET", "/api/logs"), ("GET", "/api/settings")):
            return FakeResponse(200, {})
        raise AssertionError(f"Unexpected mock API call: {method} {path}")


def run_script(env, opener):
    with mock.patch.dict(os.environ, env, clear=False):
        with mock.patch.object(urllib.request, "build_opener", return_value=opener):
            runpy.run_path(str(SCRIPT), run_name="__main__")


BASE = {
    "CSQTT_E2E_BASE_URL": "https://127.0.0.1:47002/secret",
    "CSQTT_E2E_WEB_USER": "testonly",
    "CSQTT_E2E_WEB_PASS": "testonly",
    "CSQTT_E2E_ALLOW_MUTATIONS": "isolated-only",
}
for bad in (
    {"CSQTT_E2E_ALLOW_MUTATIONS": ""},
    {"CSQTT_E2E_BASE_URL": "https://100.71.202.95:47082/secret"},
    {"CSQTT_E2E_BASE_URL": "https://example.com/secret"},
    {"CSQTT_E2E_BASE_URL": "http://127.0.0.1:47002/secret"},
    {"CSQTT_E2E_BASE_URL": "https://127.0.0.1:47002/"},
    {"CSQTT_E2E_BASE_URL": "https://user:pass@127.0.0.1:47002/secret"},
):
    try:
        run_script({**BASE, **bad}, mock.Mock())
    except SystemExit:
        print("PASS unsafe target or missing opt-in rejected")
    else:
        raise AssertionError("destructive E2E accepted an unsafe target: " + repr(bad))

success = FakeOpener()
run_script(BASE, success)
assert success.creates == success.deletes == 1 and success.group is None
assert success.cookie_seen
print("PASS successful API E2E removes exactly one mock group")

failure = FakeOpener(export_fails=True)
observed_output = io.StringIO()
with redirect_stdout(observed_output):
    try:
        run_script(BASE, failure)
    except RuntimeError as exc:
        assert "original-format export" in str(exc)
    else:
        raise AssertionError("injected export error was not reported")
assert "FAIL original-format export" in observed_output.getvalue()
assert "PASS cleanup test group" in observed_output.getvalue()
assert failure.creates == failure.deletes == 1 and failure.group is None
print("PASS injected API failure reported and mock group cleaned in finally")
print("PASS destructive E2E guard and cleanup tests, no network or real credentials")
