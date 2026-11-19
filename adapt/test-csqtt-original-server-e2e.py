#!/usr/bin/env python3
"""Destructive CSQTT 2.5.0 API E2E for a dedicated loopback lab ONLY.

Creates one disposable group and client. Runs only with explicit mutation
opt-in and loopback HTTPS; removes only its own objects in a finally clause.
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


def client_identity(rows):
    # Ignore volatile traffic counters and session details.
    return {c["password"]: (c.get("name"), c.get("group_id"), c.get("workers"))
            for c in rows}


def group_identity(rows):
    return {g["id"]: (g.get("name"), g.get("routing_rule_id")) for g in rows}


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
    before_clients = client_identity(body)
    s, body, _ = request("/api/groups")
    check("groups list", s == 200 and isinstance(body, list))
    before_groups = group_identity(body)
    s, before_settings, _ = request("/api/settings")
    check("settings baseline", s == 200 and isinstance(before_settings, dict))

    gid = None
    token = uuid.uuid4().hex[:12]
    name = "E2E test " + token
    edited_name = "E2E edited " + token
    client_name = "E2E client " + token
    edited_client = "E2E updated " + token
    client_password = "E2Etest" + uuid.uuid4().hex
    try:
        # All names and credentials are unique, including after a partial response.
        s, body, _ = request("/api/groups", "POST", {"name": name})
        if (
            s == 200 and isinstance(body, dict) and
            isinstance(body.get("id"), str) and body["id"].startswith("grp_")
        ):
            gid = body["id"]
        check("create group", gid is not None)
        s, body, _ = request("/api/groups")
        check("created group present", s == 200 and isinstance(body, list)
              and any(g.get("id") == gid for g in body))
        group_path = "/api/groups/" + urllib.parse.quote(gid, safe="")
        s, _, _ = request(group_path, "PUT", {"name": edited_name})
        check("update group", s == 200)
        s, body, _ = request("/api/groups")
        check("edited group persisted", s == 200 and isinstance(body, list)
              and any(g.get("id") == gid and g.get("name") == edited_name for g in body))

        client_path = "/api/clients/" + urllib.parse.quote(client_password, safe="")
        s, body, _ = request("/api/clients", "POST",
                             {"name": client_name, "days": 3, "workers": 3,
                              "password": client_password, "group_id": gid})
        check("create isolated client", s == 200 and isinstance(body, dict)
              and body.get("password") == client_password)
        s, body, _ = request("/api/clients")
        check("created client persisted", s == 200 and isinstance(body, list)
              and any(c.get("password") == client_password and c.get("group_id") == gid
                      and c.get("name") == client_name for c in body))
        s, _, _ = request(client_path, "POST",
                          {"name": edited_client, "days": 5, "workers": 4, "group_id": gid})
        check("update isolated client", s == 200)
        s, body, _ = request("/api/clients")
        check("edited client persisted", s == 200 and isinstance(body, list)
              and any(c.get("password") == client_password
                      and c.get("name") == edited_client and c.get("workers") == 4
                      and c.get("group_id") == gid for c in body))
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
        # An HTTP request may have committed before its response was lost.
        # Identify only our unique client password and group name/ID; never
        # delete unrelated objects or cascade-delete a nonempty group.
        s, clients, _ = request("/api/clients")
        check("cleanup clients readable", s == 200 and isinstance(clients, list))
        matches = [c for c in clients if c.get("password") == client_password]
        check("one or zero owned clients", len(matches) <= 1)
        if matches:
            path = "/api/clients/" + urllib.parse.quote(client_password, safe="")
            s, _, _ = request(path, "DELETE")
            check("cleanup test client", s == 200)
        s, clients, _ = request("/api/clients")
        check("client absent after cleanup", s == 200 and isinstance(clients, list)
              and all(c.get("password") != client_password for c in clients))

        s, groups, _ = request("/api/groups")
        check("cleanup groups readable", s == 200 and isinstance(groups, list))
        owned = [g for g in groups if (gid is not None and g.get("id") == gid)
                 or g.get("name") in (name, edited_name)]
        check("one or zero owned groups", len(owned) <= 1)
        if owned:
            group_id = owned[0]["id"]
            check("test group empty before deletion",
                  not any(c.get("group_id") == group_id for c in clients))
            path = "/api/groups/" + urllib.parse.quote(group_id, safe="")
            s, _, _ = request(path, "DELETE")
            check("cleanup test group", s == 200)
            s, groups, _ = request("/api/groups")
            check("test group absent after cleanup", s == 200
                  and isinstance(groups, list)
                  and all(g.get("id") != group_id for g in groups))

        # Always compare baseline objects, not only absence of test markers.
        s, clients, _ = request("/api/clients")
        check("existing clients preserved", s == 200 and isinstance(clients, list)
              and client_identity(clients) == before_clients)
        s, groups, _ = request("/api/groups")
        check("existing groups preserved", s == 200 and isinstance(groups, list)
              and group_identity(groups) == before_groups)
        s, after_settings, _ = request("/api/settings")
        check("global settings unchanged", s == 200 and after_settings == before_settings)
    print("E2E COMPLETE; disposable client and group removed; baseline preserved", flush=True)


if __name__ == "__main__":
    main()
