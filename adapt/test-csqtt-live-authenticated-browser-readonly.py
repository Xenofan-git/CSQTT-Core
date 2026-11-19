#!/usr/bin/env python3
"""Opt-in, read-only authenticated browser acceptance for dedicated ARM64 lab.

The guest credentials are read through authenticated local VM SSH into memory,
never written to logs or disk, included in URLs, or sent to GitHub CI.
The browser blocks business API writes. Login/logout manage only test sessions.
No screenshots or response bodies are recorded. Live CRUD is NOT tested.
"""
import argparse
import ipaddress
import json
import os
import shlex
from pathlib import Path
import subprocess
from urllib.parse import quote, urlsplit

from playwright.sync_api import sync_playwright


def validate_origin(origin: str) -> str:
    url = urlsplit(origin.rstrip("/"))
    try:
        ip = ipaddress.ip_address(url.hostname or "")
    except ValueError as exc:
        raise ValueError("panel origin must use a numeric loopback/Tailscale IP") from exc
    if (url.scheme != "https" or url.username or url.password or
        url.path not in ("", "/") or url.query or url.fragment or
        ip.version != 4 or not (ip.is_loopback or ip in ipaddress.ip_network("100.64.0.0/10")) or
        url.port is None):
        raise ValueError("panel origin must be HTTPS on an explicit local/Tailscale IP port")
    return origin.rstrip("/")


def guest_auth(key: Path) -> tuple[str, str, str]:
    if not key.is_file():
        raise ValueError("dedicated VM SSH key missing")
    guest_script = r"""
from pathlib import Path
import json,shlex
keys=('CSQTT_WEB_USER','CSQTT_WEB_PASS','CSQTT_WEB_SECRET_PATH')
p=Path('/etc/csqtt-core/csqtt-core.env')
pairs={}
for line in p.read_text().splitlines():
    item=line.strip()
    if item.startswith('export '):
        item=item[7:]
    if not item or item.startswith('#') or '=' not in item:
        continue
    name,value=item.split('=',1)
    if name not in keys: continue
    try: tokens=shlex.split(value,comments=False,posix=True)
    except ValueError: raise SystemExit(3)
    pairs[name]=tokens[0] if len(tokens)==1 else ''
if any(not pairs.get(x) for x in keys): raise SystemExit(4)
print(json.dumps([pairs[x] for x in keys]))
"""
    result = subprocess.run([
        "ssh", "-i", str(key), "-p", "22239", "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=6", "-o", "StrictHostKeyChecking=accept-new",
        "csqttlab@127.0.0.1", "sudo python3 -c " + shlex.quote(guest_script)
    ], capture_output=True, text=True, timeout=18, check=False)
    if result.returncode:
        raise RuntimeError("dedicated ARM64 guest credentials could not be read")
    values = json.loads(result.stdout)
    if len(values)!=3 or any(not isinstance(v,str) or not v for v in values):
        raise ValueError("dedicated guest has incomplete web credentials")
    user,password,path = values
    path = path.strip("/")
    if not path or ".." in path or any(not (c.isalnum() or c in "_-/~.") for c in path):
        raise ValueError("guest secret-path format unsupported")
    return user, password, path


def main() -> None:
    parser=argparse.ArgumentParser()
    parser.add_argument("--ssh-key",type=Path,required=True)
    parser.add_argument("--panel-origin",required=True)
    parser.add_argument("--chromium",default="/usr/bin/chromium")
    args=parser.parse_args()
    if os.environ.get("CSQTT_READONLY_LIVE_ACCEPTANCE")!="dedicated-arm64-lab-only":
        raise ValueError("opt-in lab-only acceptance guard missing")
    base=validate_origin(args.panel_origin)
    user,password,secret=guest_auth(args.ssh_key)
    # Never expose credentials in the URL, console, browser logs, or snapshots.
    url=f"{base}/{quote(secret,safe='-/~._')}/"
    phases=[]
    with sync_playwright() as p:
        browser=p.chromium.launch(headless=True,executable_path=args.chromium,args=[
            "--no-sandbox","--disable-dev-shm-usage","--no-first-run",
            "--disable-background-networking",
        ])
        try:
            for width,height in ((1440,900),(390,844),(360,780)):
                context=browser.new_context(viewport={"width":width,"height":height},
                                            ignore_https_errors=True,
                                            service_workers="block")
                prohibited=[]
                requests=[]
                def guard(route):
                    req=route.request
                    method=req.method.upper()
                    parts=urlsplit(req.url)
                    if (parts.hostname != urlsplit(base).hostname or
                        parts.port != urlsplit(base).port):
                        route.abort()
                        return
                    if method not in ("GET","HEAD","OPTIONS"):
                        if (method=="POST" and parts.path.rstrip("/")==
                                f"/{secret}/api/login"):
                            route.continue_()
                        else:
                            prohibited.append(method)
                            route.abort()
                        return
                    route.continue_()
                context.route("**/*",guard)
                page=context.new_page()
                page.on("response",lambda r: requests.append((
                    r.request.method, urlsplit(r.url).path.rsplit("/",1)[-1],r.status))
                    if "/api/" in urlsplit(r.url).path else None)
                try:
                    page.goto(url,wait_until="domcontentloaded",timeout=20000)
                    page.locator("#u").fill(user)
                    page.locator("#p").fill(password)
                    page.locator("#submitBtn").click()
                    page.locator("button.tab-btn[data-tab='monitoring']").wait_for(
                        state="visible",timeout=16000)
                    assert page.locator(".tabs button.tab-btn").count()==4
                    expected=("monitoring","clients","logs","settings")
                    tabs=page.locator(".tabs button.tab-btn").evaluate_all(
                        "(nodes)=>nodes.map(n=>n.dataset.tab)")
                    assert tabs==list(expected)
                    for tab in expected:
                        page.locator(f".tabs button.tab-btn[data-tab='{tab}']").click()
                        assert page.locator(f"#{tab}-section").is_visible(),tab
                        assert page.locator(".tabs button.tab-btn.active").count()==1
                        assert page.locator(".tabs button.tab-btn.active").get_attribute(
                            "data-tab")==tab
                        for other in expected:
                            if tab!=other:
                                assert page.locator(f"#{other}-section").is_hidden()
                        assert page.evaluate(
                            "document.documentElement.scrollWidth <= innerWidth+1"),(width,tab)
                    page.wait_for_timeout(350)
                    allowed_fixed=("stats","clients","groups","logs","settings","export")
                    successful_gets=sorted({n for m,n,status in requests
                                            if m=="GET" and status==200 and n in allowed_fixed})
                    print(f"READONLY_GET_ENDPOINTS width={width}: {','.join(successful_gets) or 'none'}")
                    assert set(allowed_fixed[:5]).issubset(successful_gets), (
                        "missing successful authenticated GET endpoints", width)
                    assert not prohibited,("blocked writes detected",width)
                    assert any(n=="login" and s==200 for m,n,s in requests),width
                    print(f"PASS authenticated live lab browser {width}x{height}: "
                          "4 tabs visible, no overflow, login HTTP 200, no API writes")
                finally:
                    # Delete the session created solely for this UI test.
                    # This endpoint revokes only this context's cookie, never
                    # other users' sessions or application records.
                    try:
                        logout=context.request.post(url+"api/logout",timeout=6500)
                        assert logout.status==200, "test-only logout failed"
                    finally:
                        context.close()
                phases.append(width)
        finally:
            browser.close()
    assert phases==[1440,390,360]
    print("PASS authenticated isolated ARM64 web UI, read-only interaction only")


if __name__=="__main__":
    try:
        main()
    except Exception as exc:
        # Do not print full tracebacks: they could contain the secret lab URL.
        print("FAIL read-only live browser acceptance:",type(exc).__name__)
        raise SystemExit(1)
