#!/usr/bin/env python3
"""Destructive CSQTT 2.5.0 API E2E for a dedicated loopback lab ONLY.

Creates one disposable group. Runs only with explicit mutation opt-in and a
loopback HTTPS URL; always tries to delete the group in a finally clause.
Never run against the production panel or a Tailscale endpoint.
"""
import base64
import http.client
import json
import os
import ssl
import urllib.error
import urllib.parse
import urllib.request
import uuid

base = os.environ.get("CSQTT_E2E_BASE_URL", "").rstrip("/")
parts = urllib.parse.urlsplit(base)
if os.environ.get("CSQTT_E2E_ALLOW_MUTATIONS") != "isolated-only":
    raise SystemExit("Refusing destructive E2E without explicit isolated-only opt-in")
if (
    parts.scheme != "https"
    or parts.hostname not in ("localhost", "127.0.0.1", "::1")
    or not parts.path.strip("/")
    or parts.username is not None
    or parts.password is not None
    or parts.query
    or parts.fragment
):
    raise SystemExit("Refusing destructive E2E: dedicated HTTPS loopback secret-path URL required")

user = os.environ["CSQTT_E2E_WEB_USER"]
password = os.environ["CSQTT_E2E_WEB_PASS"]
opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({}),
    urllib.request.HTTPSHandler(context=ssl._create_unverified_context()),
)
cookie = ""


def request(path, method="GET", obj=None, auth=True):
    global cookie
    assert path.startswith("/")
    data = json.dumps(obj).encode() if obj is not None else None
    headers = {"Content-Type": "application/json"}
    if auth and cookie:
        headers["Cookie"] = cookie
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        response = opener.open(req, timeout=10)
        with response:
            status, body, h = response.status, response.read(), response.headers
    except urllib.error.HTTPError as exc:
        status, body, h = exc.code, exc.read(), exc.headers
    except (http.client.RemoteDisconnected, ConnectionResetError, BrokenPipeError):
        return 0, "stealth connection closed", {}
    set_cookie = h.get("Set-Cookie", "")
    if "csqtt_session=" in set_cookie:
        cookie = set_cookie.split(";", 1)[0]
    try:
        result = json.loads(body)
    except (ValueError, TypeError):
        result = body.decode("utf-8", "replace")
    return status, result, h


def check(name, ok):
    print(("PASS" if ok else "FAIL") + " " + name, flush=True)
    if not ok:
        raise RuntimeError(name)


def encode(value):
    data = bytes((byte + 47) % 256 for byte in value.encode())
    return "c1:" + base64.b64encode(data).decode()


def main():
    s, body, _ = request("/api/stats", auth=False)
    check("anonymous stats denied", s != 200 or not isinstance(body, dict))
    s, body, _ = request("/")
    check("login HTML", s == 200 and isinstance(body, str) and "<!DOCTYPE html>" in body)
    s, body, headers = request("/api/login", "POST", {"user": encode(user), "pass": encode(password)})
    set_cookie = headers.get("Set-Cookie", "")
    check("login HttpOnly Secure cookie", s == 200 and cookie.startswith("csqtt_session=")
          and "HttpOnly" in set_cookie and "Secure" in set_cookie)
    s, body, _ = request("/")
    check("original authenticated dashboard", s == 200 and isinstance(body, str)
          and "apiDocsDlg" in body and "cryptoDonateDlg" in body)
    s, body, _ = request("/api/stats")
    check("authorized stats", s == 200 and isinstance(body, dict))
    s, body, _ = request("/api/clients")
    check("clients list", s == 200 and isinstance(body, list))
    s, body, _ = request("/api/groups")
    check("groups list", s == 200 and isinstance(body, list))

    gid = None
    try:
        # Pick a distinct group to avoid interfering with existing lab data.
        name = "E2E test " + uuid.uuid4().hex[:12]
        s, body, _ = request("/api/groups", "POST", {"name": name})
        if (
            s == 200 and isinstance(body, dict) and
            isinstance(body.get("id"), str) and body["id"].startswith("grp_")
        ):
            gid = body["id"]
        check("create group", gid is not None)
        s, body, _ = request("/api/groups")
        check("created group present", s == 200 and any(g.get("id") == gid for g in body))
        s, body, _ = request("/api/export")
        check("original-format export", s == 200 and isinstance(body, dict)
              and body.get("version") == 1 and "groups" in body and "clients" in body)
        s, _, _ = request("/api/import", "POST",
                          {"import_clients": False, "import_settings": False, "config": body})
        check("no-op import flags", s == 200)
        for path in ("/api/logs", "/api/settings"):
            s, _, _ = request(path)
            check("protected " + path, s == 200)
    finally:
        if gid is not None:
            path = "/api/groups/" + urllib.parse.quote(gid, safe="")
            s, _, _ = request(path, "DELETE")
            check("cleanup test group", s == 200)
            s, body, _ = request("/api/groups")
            check("test group absent after cleanup", s == 200
                  and isinstance(body, list) and all(g.get("id") != gid for g in body))
    print("E2E COMPLETE; isolated group removed", flush=True)


if __name__ == "__main__":
    main()
