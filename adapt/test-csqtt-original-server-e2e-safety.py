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
    def __init__(self, export_fails=False, lost_group_reply=False, lost_client_reply=False):
        self.export_fails = export_fails
        self.lost_group_reply = lost_group_reply
        self.lost_client_reply = lost_client_reply
        self.group = None
        self.client = None
        # Nonempty preexisting records ensure the E2E cannot pass merely by
        # deleting the entire database or wiping unrelated clients/groups.
        self.existing_group = {"id": "grp_preexisting", "name": "Do not touch",
                               "routing_rule_id": ""}
        self.existing_client = {"password": "baseline-only-placeholder",
                                "name": "Existing account", "group_id": "grp_preexisting",
                                "workers": 3}
        self.creates = 0
        self.deletes = 0
        self.client_creates = 0
        self.client_deletes = 0
        self.group_updates = 0
        self.client_updates = 0
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
            return FakeResponse(200, [self.existing_client] +
                                ([self.client] if self.client else []))
        if (method, path) == ("GET", "/api/groups"):
            return FakeResponse(200, [self.existing_group] +
                                ([self.group] if self.group else []))
        if (method, path) == ("POST", "/api/groups"):
            self.creates += 1
            data = json.loads(req.data)
            assert data["name"].startswith("E2E test ")
            self.group = {"id": "grp_mock_test", "name": data["name"]}
            if self.lost_group_reply:
                raise ConnectionResetError("injected lost group response")
            return FakeResponse(200, {"id": self.group["id"]})
        if (method, path) == ("PUT", "/api/groups/grp_mock_test"):
            data = json.loads(req.data)
            assert data["name"].startswith("E2E edited ")
            self.group["name"] = data["name"]
            self.group_updates += 1
            return FakeResponse(200, {"ok": True})
        if (method, path) == ("POST", "/api/clients"):
            data = json.loads(req.data)
            assert data["group_id"] == "grp_mock_test"
            assert data["workers"] == 3 and data["days"] == 3
            self.client = {
                "password": data["password"], "name": data["name"],
                "group_id": data["group_id"], "workers": data["workers"],
            }
            self.client_creates += 1
            if self.lost_client_reply:
                raise ConnectionResetError("injected lost client response")
            return FakeResponse(200, {"password": self.client["password"]})
        if method == "POST" and path.startswith("/api/clients/"):
            data = json.loads(req.data)
            assert self.client and path == "/api/clients/" + self.client["password"]
            assert data["days"] == 5 and data["workers"] == 4
            self.client.update({k: data[k] for k in ("name", "group_id", "workers")})
            self.client_updates += 1
            return FakeResponse(200, {"ok": True})
        if method == "DELETE" and path.startswith("/api/clients/"):
            assert self.client and path == "/api/clients/" + self.client["password"]
            self.client_deletes += 1
            self.client = None
            return FakeResponse(200, {"ok": True})
        if (method, path) == ("DELETE", "/api/groups/grp_mock_test"):
            assert self.client is None, "group must not cascade-delete its test client"
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
assert success.client_creates == success.client_deletes == 1 and success.client is None
assert success.group_updates == success.client_updates == 1
assert success.existing_group["name"] == "Do not touch"
assert success.existing_client["name"] == "Existing account"
assert success.cookie_seen
print("PASS mocked create/update/read/delete client and group; both fully cleaned")

for reason, failure, expected, expected_clients in (
    ("export failure", FakeOpener(export_fails=True), "original-format export", 1),
    ("lost group reply", FakeOpener(lost_group_reply=True), "create group", 0),
    ("lost client reply", FakeOpener(lost_client_reply=True), "create isolated client", 1),
):
    observed_output = io.StringIO()
    with redirect_stdout(observed_output):
        try:
            run_script(BASE, failure)
        except RuntimeError as exc:
            assert expected in str(exc), (reason, str(exc))
        else:
            raise AssertionError("injected failure was not reported: " + reason)
    assert "PASS cleanup test group" in observed_output.getvalue()
    assert failure.creates == failure.deletes == 1 and failure.group is None
    assert failure.client_creates == failure.client_deletes == expected_clients
    assert failure.client is None
    assert failure.existing_group["name"] == "Do not touch"
    assert failure.existing_client["name"] == "Existing account"
    print("PASS " + reason + " cleans all owned objects, including after committed lost response")
print("PASS destructive E2E guard, CRUD and cleanup tests, no network or real credentials")
