#!/usr/bin/env python3
"""Deterministically derive the selected CSQTT Core Linux server from pinned 2.5.0.

No installation, network, route or systemd commands are executed.
Old 2.5.0 and existing Core8 integration are SHA-pinned; apply only the
previously audited Linux Core8 overlay and selective 2.5.3 Rust improvements.
A different source or partial patch fails closed inside a private temporary tree.
"""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent
ORIGINAL = {
    "rust-server": (19, "62fa91ab090debdf27bed93a7991f02ba596da24d32d8e6f6fed32ce84a81bbd"),
    "shared": (3, "8d2d1e01f62570e7f63422aa0ea55deb745ad334122f585404bdddd1d41d37df"),
    "app/src/main/assets": (5, "297eda3bb480364d39756ec300948b078c960bfeca5aee0d265f62153acbff0d"),
}
AFTER = {
    "rust-server": (20, "f623d3705b8c821a8bf513e25d7b874461d919ad7631351f128f3ed3f070df3e"),
    "shared": (3, "8d2d1e01f62570e7f63422aa0ea55deb745ad334122f585404bdddd1d41d37df"),
    "app/src/main/assets": (3, "0b24150c8e0e5a5e4d8e7664e972d0b3d0e4657181ba8ae47efe3f5309fed92d"),
}
# A strict additional isolation layer after the already-audited selective
# 2.5.3 source: legacy 46000/46002 defaults and "csqtt" systemd targets
# must never be retained in a separate Debian/Ubuntu CSQTT Core install.
FINAL = {
    "rust-server": (20, "2c03df9581e87fe9e1e9ab0baaa7549dadaa8d2c32f5f62cb180193de2cb605a"),
    "shared": (3, "8d2d1e01f62570e7f63422aa0ea55deb745ad334122f585404bdddd1d41d37df"),
    "app/src/main/assets": (3, "0b24150c8e0e5a5e4d8e7664e972d0b3d0e4657181ba8ae47efe3f5309fed92d"),
}
CORE8_BEFORE = {
    "main.rs": "bab3a9cbac387e520043851251f42025a67b7522dc5b108cd05ed7c0c1d0939a",
    "dataplane.rs": "f35eab6e89943c449d65d29d61b0d17d0adc7b9eedddf150852fba3a2fae334d",
    "proxy_route.rs": "e0d7efd5545cdc250d6d5f9c98fbc33f211a61913d78f479e632b17439d09ad9",
    "tokio_io.rs": "a7f936c7f027be55c729ccbb138903bfc68c69cb4060f7297424e461c85041fc",
    "tproxy.rs": "0bd8859797cefc3b4421c827c27ab3f8f8fa99ffb8d847f3994758d1b3d9da07",
    "tun_device.rs": "2e68c3b0b85a868b96179b1c5a443c2763f5ed54e4c8f2a47d6d2aed068b90d2",
    "model.rs": "9e9b59b474f9bc94c7e90ba69c7e70a980ce8980d99c33c5d75d0b6f39f14ffa",
    "web_panel.rs": "71b0bc5fa3208d8dd21b5d0f998e973ac5f96bbb94f50f8caf4ecb7b982970ae",
    "protocol.rs": "351947c3f8ecc77ea77448ca95409fcdc89b45a952a2a877897d672abceff9be",
    "csqtt-core-panel.html": "08effb52dccb5de35f29b7605363c5f7d4aafdb87271e057bbbbb673cf9b6b5d",
}
# Exact pinned overlay: changes prior ARM64-tested integration sources only.
OVERLAY_SHA = "97af750fef18cb3f984b8d8348c3f1eadaf1cfb315a69920a9f363b8dccdfe20"

def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()

def tree_digest(root: Path) -> tuple[int, str]:
    if not root.is_dir() or root.is_symlink():
        raise ValueError(f"not a regular source directory: {root}")
    files = sorted(p for p in root.rglob("*") if p.is_file() or p.is_symlink())
    h = hashlib.sha256()
    for f in files:
        if f.is_symlink() or not f.is_file():
            raise ValueError(f"source file is not regular: {f}")
        b = f.read_bytes()
        h.update(f.relative_to(root).as_posix().encode() + b"\0" + str(len(b)).encode() + b"\0")
        h.update(b + b"\0")
    return len(files), h.hexdigest()

def run(*args: str, cwd: Path | None = None) -> None:
    subprocess.run(args, cwd=str(cwd) if cwd is not None else None, check=True, timeout=150)

def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--upstream-root", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--check-only", action="store_true")
    args = p.parse_args()
    upstream = args.upstream_root.resolve(strict=True)
    output = args.output.absolute()
    if output.exists() or output.is_symlink():
        raise ValueError("refusing to overwrite an existing output path")
    if not output.parent.is_dir() or output.parent.is_symlink():
        raise ValueError("output parent must be an existing regular directory")

    for folder, expected in ORIGINAL.items():
        actual = tree_digest(upstream / folder)
        if actual != expected:
            raise ValueError(f"pinned CSQTT 2.5.0 source mismatch: {folder}: {actual}")

    overlay = ROOT / "csqtt-core8-existing-source-preservation.patch"
    if digest(overlay) != OVERLAY_SHA:
        raise ValueError("audited Core8 overlay changed; refusing derivation")

    with tempfile.TemporaryDirectory(prefix=".csqtt-linux-unified-staging-", dir=output.parent) as scratch:
        work = Path(scratch) / "derived"
        run(sys.executable, "-B", str(ROOT / "derive-csqtt-core-rust-250.py"),
            "--upstream-root", str(upstream), "--output", str(work))
        run(sys.executable, "-B", str(ROOT / "integrate-original-server-panel.py"),
            "--upstream", str(work / "rust-server/web_panel.rs"),
            "--output-dir", str(work / "rust-server"))
        # Zero fuzz: old Linux Core8 fixes and our approved panel must match
        # the exact reviewed baseline, not an approximate upstream codebase.
        run("patch", "--batch", "--fuzz=0", "--forward", "--no-backup-if-mismatch",
            "-p1", "-i", str(overlay), cwd=work)
        run(sys.executable, "-B", str(ROOT / "patch-csqttr-253-selective-core8.py"),
            "--source-root", str(work / "rust-server"))
        for filename, expected in CORE8_BEFORE.items():
            actual = digest(work / "rust-server" / filename)
            if actual != expected:
                raise ValueError(f"derived selected Core8 file drift: {filename}: {actual}")
        for folder, expected in AFTER.items():
            actual = tree_digest(work / folder)
            if actual != expected:
                raise ValueError(f"derived Linux source tree drift: {folder}: {actual}")
        # The 2.5.3 backport was independently reviewed; only now apply
        # the narrowly scoped CSQTT Core service/port isolation fix.
        run(sys.executable, "-B", str(ROOT / "patch-csqtt-core8-linux-runtime-isolation.py"),
            "--source-root", str(work / "rust-server"))
        for folder, expected in FINAL.items():
            actual = tree_digest(work / folder)
            if actual != expected:
                raise ValueError(f"isolated Linux source tree drift: {folder}: {actual}")
        if not args.check_only:
            os.replace(work, output)
            print(f"CSQTT_CORE_LINUX_250_253_UNIFIED_SOURCE_REPRODUCED: {output}")
        else:
            print("CSQTT_CORE_LINUX_250_253_UNIFIED_PINNED_SOURCE_CHECK_PASS")
        print("SELECTED_FINAL_RUST_TREE_SHA256", FINAL["rust-server"][1])

if __name__ == "__main__":
    main()
