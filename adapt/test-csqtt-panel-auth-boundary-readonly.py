#!/usr/bin/env python3
"""Non-mutating smoke of the dedicated CSQTT 2.5 ARM64 lab panel.

Only GET requests, no credentials, cookies, POST/PUT/DELETE or client data.
The URL must point to a loopback or Tailscale IPv4 test endpoint and include
the secret-path prefix. Run manually from a connected lab host; not CI.
"""
import ipaddress
import os
import subprocess
import tempfile
from pathlib import Path
from urllib.parse import urlsplit

raw = os.environ.get("CSQTT_LAB_PANEL_BASE", "").rstrip("/")
if not raw:
    raise SystemExit("Set CSQTT_LAB_PANEL_BASE to the dedicated LAB secret URL")
parts = urlsplit(raw)
try:
    ip = ipaddress.ip_address(parts.hostname or "")
except ValueError:
    if parts.hostname != "localhost":
        raise SystemExit("Refusing non-local / non-Tailscale panel target")
else:
    if ip.version != 4 or not (
        ip.is_loopback or ip in ipaddress.ip_network("100.64.0.0/10")
    ):
        raise SystemExit("Refusing non-local / non-Tailscale panel target")
if parts.scheme != "https" or not parts.path.strip("/") or parts.username or parts.password:
    raise SystemExit("Expected an HTTPS lab URL with secret-path prefix and no credentials")

def query(path: str, *, invalid_key: bool = False, capture: bool = False):
    assert path.startswith("/")
    with tempfile.TemporaryDirectory(prefix="csqtt-lab-auth-smoke-") as temp:
        out = Path(temp) / "response"
        cmd = [
            "curl", "--silent", "--show-error", "--insecure", "--noproxy", "*",
            "--request", "GET", "--connect-timeout", "3", "--max-time", "8",
            "--header", "Accept: application/json", "--output", str(out),
            "--write-out", "%{http_code}",
        ]
        if invalid_key:
            cmd += ["--header", "x-api-key: deliberately-invalid-auth-boundary-test"]
        cmd.append(raw + path)
        run = subprocess.run(cmd, capture_output=True, text=True, timeout=12)
        return run.returncode, run.stdout.strip(), out.read_bytes() if capture and out.exists() else b""

def check(name, ok):
    print(("PASS" if ok else "FAIL") + " " + name, flush=True)
    assert ok, name

exit_code, status, body = query("/", capture=True)
check("secret-path login HTML reachable", exit_code == 0 and status == "200" and b"<!doctype html" in body.lower())
for api in ("/api/clients", "/api/groups", "/api/settings", "/api/export", "/api/stats"):
    exit_code, status, _ = query(api)
    check("anonymous rejected " + api, not (exit_code == 0 and status == "200"))
for api in ("/api/clients", "/api/groups", "/api/import"):
    exit_code, status, _ = query(api, invalid_key=True)
    check("invalid key HTTP 401 " + api, exit_code == 0 and status == "401")
# Go above the secret path; a direct call to the unprefixed API should not bypass it.
root = parts.path.rstrip("/")
prefix = raw[: -len(root)]
with tempfile.TemporaryDirectory(prefix="csqtt-lab-auth-root-") as temp:
    # Separate direct URL to avoid normalizing away the secret path accidentally.
    cmd = ["curl", "-k", "-sS", "--noproxy", "*", "-m", "8",
           "-o", str(Path(temp) / "root-api"), "-w", "%{http_code}",
           prefix + "/api/clients"]
    run = subprocess.run(cmd, capture_output=True, text=True, timeout=12)
    check("unprefixed API rejected", not (run.returncode == 0 and run.stdout.strip() == "200"))
print("PASS isolated lab unauthenticated API boundary (GET-only), no mutations")
