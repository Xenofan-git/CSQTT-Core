#!/usr/bin/env python3
"""Guard visible product names and test/workflow titles.

Do not change pinned upstream repository URLs, legacy runtime resources,
artifact paths, or compatibility protocol fields with this check.
"""
from pathlib import Path
import json
import re

legacy_brand = "La" + "Lune"
product_name = "CSQTT Core"
ui = Path("adapt/csqtt-panel-preview.html").read_text()
manifest = json.loads(Path("captcha-bridge/manifest.json").read_text())
for filename in ("captcha-bridge/options.html", "captcha-bridge/README.md"):
    assert legacy_brand not in Path(filename).read_text(), filename
assert legacy_brand not in ui
assert product_name in ui
assert "/updates/lalune" not in ui, "old upstream updater must not be shown as a product update"
assert 'id="laluneUpdate"' not in ui
assert all(legacy_brand not in manifest[field] for field in ("name","description"))
assert product_name in manifest["name"]
for path in Path(".github/workflows").glob("*.yml"):
    for line in path.read_text().splitlines():
        # Artifact names are backwards-compatible IDs, not public job titles.
        if re.match(r"^(?:name:|\s+- name:)", line):
            assert legacy_brand not in line, f"workflow visible title: {path}"
for path in Path("adapt").glob("test-csqtt*"):
    assert legacy_brand.lower() not in path.name.lower(), f"old brand in test name: {path}"
print("PASS CSQTT Core UI, CAPTCHA extension, test names and workflow titles")
