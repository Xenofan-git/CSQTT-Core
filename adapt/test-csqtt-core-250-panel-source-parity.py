#!/usr/bin/env python3
"""Confirm derived 2.5.0 server web API is original except for Core tunnel namespace.

No live system or network changes; git archive and generated sources are kept
in a throwaway directory. Requires a local checkout containing the pinned tag.
"""
import argparse
from io import BytesIO
from pathlib import Path
import hashlib
import re
import subprocess
import sys
import tarfile
import tempfile

PINNED = "fd936ef087c34cc46f26111dc2361f43b8b03c07"
PATH = Path(__file__).resolve().parent
CHECKED_API_PATHS = (
    "/api/login", "/api/clients", "/api/clients/{password}",
    "/api/groups", "/api/groups/{id}", "/api/export",
    "/api/import", "/api/settings", "/api/stats",
)
parser = argparse.ArgumentParser()
parser.add_argument("--upstream-root", type=Path, required=True)
args = parser.parse_args()
upstream = args.upstream_root.resolve(strict=True)

def call(*cmd):
    return subprocess.run(cmd, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

assert call("git", "-C", str(upstream), "rev-parse", "HEAD").stdout.decode().strip() == PINNED, (
    "CSQTT source commit differs from pinned original v2.5.0"
)
data = call("git", "-C", str(upstream), "archive", "--format=tar", PINNED).stdout
with tempfile.TemporaryDirectory(prefix="csqtt-original-250-api-parity-") as folder:
    root = Path(folder)
    original, derived = root / "official", root / "derived"
    original.mkdir()
    with tarfile.open(fileobj=BytesIO(data), mode="r:") as tf:
        tf.extractall(original, filter="data")
    call(
        sys.executable,
        str(PATH / "derive-csqtt-core-rust-250.py"),
        "--upstream-root", str(original),
        "--output", str(derived),
    )
    old = (original / "rust-server" / "web_panel.rs").read_text()
    new = (derived / "rust-server" / "web_panel.rs").read_text()
    expected = (old.replace("10.66.67.", "10.67.69.")
                .replace("Ipv4Addr::from([10, 66, 67,", "Ipv4Addr::from([10, 67, 69,")
                .replace("csqtt1", "csqtt-core0")
                .replace("CSQTT_TPROXY", "CSQTT_CORE_TPROXY"))
    assert new == expected, "Derived 2.5 Core web panel has unexplained source mutations"
    old_routes = re.findall(r'\.route\(\s*"([^"]+)"', old)
    new_routes = re.findall(r'\.route\(\s*"([^"]+)"', new)
    assert old_routes == new_routes, "Derived 2.5 Core web routes changed"
    for path in CHECKED_API_PATHS:
        assert path in old_routes, "Original API path not located: " + path
        assert path in new_routes, "Derived API path not located: " + path
    print("CSQTT_250_DERIVED_WEB_PANEL_PARITY_PASS")
    print("verified_api_routes", len(CHECKED_API_PATHS))
    print("all_registered_routes", len(old_routes))
    print("allowed_ip_prefix_edits", old.count("10.66.67."))
    print("original_sha256", hashlib.sha256(old.encode()).hexdigest())
    print("derived_sha256", hashlib.sha256(new.encode()).hexdigest())
