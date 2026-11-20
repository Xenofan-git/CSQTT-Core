#!/usr/bin/env python3
"""Stage the independent CSQTT Core 7-tab web UI without Flutter/Wails.

Only index.html and api.js are copied from first-party tracked sources.
This program never downloads dependencies or executes an upstream toolchain.
"""
import argparse
import hashlib
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
SOURCES = {
    "index.html": ROOT / "adapt/csqtt-panel-preview.html",
    "api.js": ROOT / "adapt/nanopi.js",
}


def stage(destination: Path) -> dict[str, str]:
    if destination.is_symlink() or (destination.exists() and not destination.is_dir()):
        raise ValueError("web output must be a regular directory, not a symlink or file")
    # Prevent quietly embedding an old third-party Flutter application.
    if destination.exists():
        extras = {p.name for p in destination.iterdir()} - set(SOURCES)
        if extras:
            raise ValueError("web output contains unexpected assets (refusing mixed bundle)")
        for name in SOURCES:
            candidate = destination / name
            if candidate.is_symlink() or (candidate.exists() and not candidate.is_file()):
                raise ValueError("web output asset is symlinked or not a regular file")

    contents = {name: source.read_bytes() for name, source in SOURCES.items()}
    html = contents["index.html"].decode("utf-8")
    if html.count('data-page="') != 7:
        raise ValueError("the seven-tab native panel is missing or incomplete")
    if "<title>CSQTT Core · NanoPi</title>" not in html:
        raise ValueError("incorrect native CSQTT Core panel")
    if "<script src=" in html or "flutter.js" in html or "flutter_bootstrap" in html:
        raise ValueError("web panel unexpectedly requires third-party frontend runtime")
    js = contents["api.js"].decode("utf-8")
    if "/updates/lalune" in js or "OpenLaLuneReleases" in js:
        raise ValueError("obsolete unrelated upstream update API is still bundled")

    destination.mkdir(parents=True, exist_ok=True)
    digests = {}
    for name, data in contents.items():
        target = destination / name
        temp = destination / (name + ".csqtt-stage-tmp")
        if temp.exists() or temp.is_symlink():
            raise ValueError("web output staging path already occupied")
        try:
            with temp.open("xb") as fp:
                fp.write(data)
                fp.flush()
            temp.replace(target)
        finally:
            if temp.exists():
                temp.unlink()
        digests[name] = hashlib.sha256(data).hexdigest()
        if target.read_bytes() != data:
            raise RuntimeError("staged first-party asset was corrupted")
    return digests


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    for name, sha in stage(args.output).items():
        print(f"CSQTT Core own web asset {name} SHA256={sha}")
    print("PASS standalone CSQTT Core web packaging, no Flutter compile")


if __name__ == "__main__":
    main()
