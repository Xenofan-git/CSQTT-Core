#!/usr/bin/env python3
"""Reverify the already-published CSQTT Core 2.5 WIRE-3 ARM64 RC ZIP.

Offline/read-only. Accepts no production installer target, network endpoint,
secret, systemctl or host networking operation. ZIP and all four ELF hashes
must match pinned hashes established in the validated ARM64 CI evidence.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import stat
import sys
import zipfile

FOLDER = "csqtt-core-250-wire3-arm64-validated-rc"
ARCHIVE_SHA = "49b780ca5bba99ed9641c5bfe1650a23600922b5f341d705c0375bf1cbbf9906"
BINARIES = {
    "csqtt-core-go-supervisor-arm64": ("go-supervisor", "676d4f9de67b5ffc50f5607344e1d477722dac300572ac77c7f378cc7af1da3b"),
    "csqtt-core-original-client-250-arm64": ("original-client", "3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa"),
    "csqtt-core-derived-server-250-arm64": ("derived-server", "a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706"),
    "csqtt-core-sing-box-arm64": ("sing-box", "d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a"),
}
DOCS = ("manifest.json", "EVIDENCE.md", "README-CANDIDATE.txt", "SHA256SUMS")
FILES = {f"{FOLDER}/bin/{name}" for name in BINARIES} | {f"{FOLDER}/{name}" for name in DOCS}
DIRS = {f"{FOLDER}/", f"{FOLDER}/bin/"}
LIMIT_PER_ENTRY = 180 * 1024 * 1024
LIMIT_TOTAL = 500 * 1024 * 1024


def digest_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for buf in iter(lambda: f.read(1024 * 1024), b""):
            h.update(buf)
    return h.hexdigest()


def verify_members(z: zipfile.ZipFile) -> dict[str, zipfile.ZipInfo]:
    entries = z.infolist()
    names = [entry.filename for entry in entries]
    if len(names) != len(set(names)):
        raise ValueError("duplicate ZIP entries")
    members = {}
    total = 0
    for info in entries:
        name = info.filename
        if (not name or name.startswith("/") or "\\" in name
                or PurePosixPath(name).parts != tuple(name.rstrip("/").split("/"))
                or any(part in {"", ".", ".."} for part in name.rstrip("/").split("/"))):
            raise ValueError("unsafe ZIP member name")
        if info.flag_bits & 0x1:
            raise ValueError("encrypted ZIP entry")
        mode = info.external_attr >> 16
        if stat.S_ISLNK(mode):
            raise ValueError("symlink ZIP entry")
        if info.is_dir():
            if name not in DIRS:
                raise ValueError("unexpected ZIP directory")
            continue
        if name not in FILES:
            raise ValueError("unrecognized ZIP payload")
        if (mode & 0o170000) not in (0, stat.S_IFREG):
            raise ValueError("non-regular ZIP payload")
        if info.file_size > LIMIT_PER_ENTRY or info.file_size <= 0:
            raise ValueError("unsafe ZIP entry size")
        if name.startswith(f"{FOLDER}/bin/") and not (mode & 0o111):
            raise ValueError("binary executable bit missing")
        total += info.file_size
        if total > LIMIT_TOTAL:
            raise ValueError("archive decompressed content too large")
        members[name] = info
    if set(members) != FILES:
        raise ValueError("release package file inventory mismatch")
    return members


def file_sha(z: zipfile.ZipFile, name: str) -> tuple[str, bytes]:
    h = hashlib.sha256()
    head = b""
    with z.open(name) as f:
        while chunk := f.read(1024 * 1024):
            if len(head) < 20:
                head = (head + chunk)[:20]
            h.update(chunk)
    return h.hexdigest(), head


def verify_zip(path: Path) -> None:
    if not path.is_file() or path.is_symlink():
        raise ValueError("regular ZIP input required")
    if digest_file(path) != ARCHIVE_SHA:
        raise ValueError("archive SHA-256 does not match immutable validated RC")
    with zipfile.ZipFile(path) as z:
        members = verify_members(z)
        doc = lambda name: z.read(f"{FOLDER}/{name}")
        manifest = json.loads(doc("manifest.json"))
        if (manifest.get("status") != "validated-isolated-release-candidate-not-production"
                or manifest.get("product") != "CSQTT Core"
                or manifest.get("architecture") != "linux-arm64"
                or manifest.get("wireProtocol") != "CSQTT-WIRE-3"
                or manifest.get("globalDefaultRoute") is not False
                or manifest.get("entrypoint") != "csqtt-core-go-supervisor-arm64"):
            raise ValueError("candidate policy/architecture/protocol mismatch")
        actual_files = manifest.get("files")
        if not isinstance(actual_files, dict) or set(actual_files) != set(BINARIES):
            raise ValueError("manifest expected exactly four release components")
        digests = {}
        for name, (role, fixed_sha) in BINARIES.items():
            meta = actual_files[name]
            if not isinstance(meta, dict) or meta.get("role") != role:
                raise ValueError("manifest component role mismatch")
            path_in_zip = f"{FOLDER}/bin/{name}"
            sha, header = file_sha(z, path_in_zip)
            if (sha != fixed_sha or meta.get("sha256") != fixed_sha
                    or meta.get("size") != members[path_in_zip].file_size):
                raise ValueError("release ELF SHA/size mismatch")
            if (header[:6] != b"\x7fELF\x02\x01"
                    or int.from_bytes(header[18:20], "little") != 183):
                raise ValueError("release ELF is not native AArch64")
            digests[f"bin/{name}"] = sha
            print("RC250_VERIFIED_AARCH64_COMPONENT",name,sha,flush=True)
        for name in DOCS[:-1]:
            digests[name] = hashlib.sha256(doc(name)).hexdigest()
        declared = {}
        for line in doc("SHA256SUMS").decode("ascii").splitlines():
            match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9./_-]+)",line)
            if not match or match[2] in declared:
                raise ValueError("invalid or duplicate SHA256SUMS entry")
            declared[match[2]] = match[1]
        if digests != declared:
            raise ValueError("SHA256SUMS integrity failed")
        if b"NOT a production rollout" not in doc("README-CANDIDATE.txt"):
            raise ValueError("production restriction notice missing")
        if "Production VK-authenticated".encode() not in doc("README-CANDIDATE.txt"):
            raise ValueError("real VK TURN disclosure missing")
    print("CSQTT_250_VALIDATED_RC_ARCHIVE_OFFLINE_BYTE_IDENTICAL_AND_NON_PRODUCTION_PASS",flush=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("archive",type=Path)
    args = parser.parse_args()
    try:
        verify_zip(args.archive)
    except (OSError, ValueError, KeyError, TypeError, zipfile.BadZipFile, UnicodeError) as exc:
        print(f"RC250_ARCHIVE_VERIFICATION_FAILED: {type(exc).__name__}: {exc}",file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
