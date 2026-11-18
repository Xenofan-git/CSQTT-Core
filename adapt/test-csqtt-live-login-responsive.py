#!/usr/bin/env python3
"""Read-only responsive login smoke against an isolated CSQTT 2.5.0 lab panel.

Requires the dedicated lab URL in CSQTT_LAB_PANEL_BASE; never submits credentials
or performs any authenticated API mutations. Uses system Chromium and chromedriver.
"""
import ipaddress
import os
from urllib.parse import urlsplit

from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.chrome.service import Service

url = os.environ.get("CSQTT_LAB_PANEL_BASE", "").rstrip("/") + "/"
parts = urlsplit(url)
try:
    ip = ipaddress.ip_address(parts.hostname or "")
    approved_host = ip.version == 4 and (
        ip.is_loopback or ip in ipaddress.ip_network("100.64.0.0/10")
    )
except ValueError:
    approved_host = parts.hostname == "localhost"
if not approved_host or parts.scheme != "https" or not parts.path.strip("/"):
    raise SystemExit("Refusing non-local/Tailscale endpoint or URL without secret-path prefix")
if parts.username or parts.password:
    raise SystemExit("Credentials must not be provided in URL")

opts = Options()
opts.binary_location = os.environ.get("CSQTT_TEST_CHROMIUM", "/usr/bin/chromium")
for arg in (
    "--headless=new",
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--ignore-certificate-errors",  # dedicated lab uses self-signed TLS
    "--no-first-run",
):
    opts.add_argument(arg)

driver = webdriver.Chrome(
    service=Service(os.environ.get("CSQTT_TEST_CHROMEDRIVER", "/usr/bin/chromedriver")),
    options=opts,
)
try:
    for width, height in ((1280, 800), (390, 844)):
        driver.set_window_size(width, height)
        driver.get(url)
        result = driver.execute_script("""
            const ids = ['u', 'p', 'submitBtn'];
            const controls = Object.fromEntries(ids.map(id => {
                const el = document.getElementById(id);
                const rect = el?.getBoundingClientRect();
                return [id, {
                    exists: !!el,
                    visible: !!rect && rect.width > 0 && rect.height > 0,
                    inViewport: !!rect && rect.left >= 0 && rect.right <= innerWidth + 1
                }];
            }));
            return {
                title: document.title, path: location.pathname,
                documentWidth: document.documentElement.scrollWidth,
                viewportWidth: innerWidth,
                inputs: [...document.querySelectorAll('input')].map(e => e.type),
                controls
            };
        """)
        assert result["path"] == parts.path, (width, result["path"])
        assert result["title"].startswith("CSQTT"), (width, result["title"])
        assert result["documentWidth"] <= result["viewportWidth"] + 1, (width, result)
        assert "password" in result["inputs"], (width, result)
        assert all(c["exists"] and c["visible"] and c["inViewport"] for c in result["controls"].values()), (
            width, result["controls"]
        )
        print(f"PASS live CSQTT login viewport {width}x{height}, no horizontal overflow, fields visible")
finally:
    driver.quit()
print("PASS live isolated desktop/mobile login smoke, no credentials or mutations")
