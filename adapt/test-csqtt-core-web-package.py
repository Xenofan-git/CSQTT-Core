#!/usr/bin/env python3
"""Check that first-party CSQTT Core web packaging needs no upstream Flutter."""
from importlib.machinery import SourceFileLoader
from pathlib import Path
import hashlib
import tempfile

module = SourceFileLoader("csqtt_core_web", str(
    Path(__file__).with_name("package-csqtt-core-web.py"))).load_module()
with tempfile.TemporaryDirectory(prefix="csqtt-core-firstparty-web-") as tmp:
    output = Path(tmp) / "web"
    hashes = module.stage(output)
    assert set(hashes) == {"index.html", "api.js"}
    assert {p.name for p in output.iterdir()} == set(hashes)
    for name in hashes:
        source = module.SOURCES[name]
        assert output.joinpath(name).read_bytes() == source.read_bytes()
        assert hashlib.sha256(source.read_bytes()).hexdigest() == hashes[name]
    # Idempotent and contamination-free.
    assert module.stage(output) == hashes
    (output/"flutter.js").write_text("stale third-party bundle\n")
    try:
        module.stage(output)
    except ValueError:
        pass
    else:
        raise AssertionError("mixed Flutter/CSQTT Core web bundle accepted")
    (output/"flutter.js").unlink()
    fake = output/"api.js"
    fake.unlink()
    fake.symlink_to(Path(tmp)/"missing-foreign-js")
    try:
        module.stage(output)
    except ValueError:
        pass
    else:
        raise AssertionError("symlinked web asset accepted")

# Prevent future CI edits from reintroducing Flutter or copying web bytes from
# the pinned third-party repository instead of the CSQTT Core native source.
heavy = Path(".github/workflows/build-original-linux-arm64.yml").read_text()
assert "flutter build web" not in heavy
assert "subosito/flutter-action" not in heavy
assert "upstream-lalune/Frontend/Core/build/web" not in heavy
assert "python3 adapt/package-csqtt-core-web.py --output csqtt-core-web-dist" in heavy
assert "cp -a csqtt-core-web-dist/. upstream-lalune/Desktop/Linux/frontend/" in heavy
assert 'test -s upstream-lalune/Desktop/Linux/frontend/api.js' in heavy
assert "python3 adapt/test-csqtt-core-web-package.py" in heavy
print("PASS standalone CSQTT Core web packaging, exactly two first-party assets")
print("PASS no Flutter asset pollution, symlink attack, or upstream compilation required")
