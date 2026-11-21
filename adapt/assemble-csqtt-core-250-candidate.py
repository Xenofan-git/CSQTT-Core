#!/usr/bin/env python3
"""Build a NON-PRODUCTION candidate bundle, using only pinned ARM64 artifacts.

No SSH, systemd, firewall, DNS or production service changes.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import struct

APPROVED = {
 "original-client": "3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa",
 "derived-server": "a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706",
}
SRC="fd936ef087c34cc46f26111dc2361f43b8b03c07"
CORE="91b154011ca16c219f57eef99462d3408dccfeeb"

def elf_aarch64(data:bytes)->None:
    assert data[:4]==b"\x7fELF" and data[4]==2 and data[5]==1,"not ELF64 little-endian"
    assert struct.unpack_from("<H",data,18)[0]==183,"not native Linux ARM64"

def digest(data:bytes)->str:
    return hashlib.sha256(data).hexdigest()

def read_executable(path:Path)->bytes:
    stat_result=path.lstat()
    assert stat.S_ISREG(stat_result.st_mode),f"not a regular executable: {path}"
    assert stat_result.st_mode & 0o111,f"file is not executable: {path}"
    data=path.read_bytes()
    elf_aarch64(data)
    return data

def main()->None:
    parser=argparse.ArgumentParser()
    parser.add_argument("--go",type=Path,required=True)
    parser.add_argument("--client",type=Path,required=True)
    parser.add_argument("--server",type=Path,required=True)
    parser.add_argument("--sing-box",type=Path,required=True)
    parser.add_argument("--output",type=Path,required=True)
    args=parser.parse_args()

    assert os.environ.get("CSQTT_CANDIDATE_ASSEMBLY")=="isolated-github-runner"
    assert os.environ.get("GITHUB_ACTIONS")=="true"
    assert os.environ.get("RUNNER_ENVIRONMENT")=="github-hosted"
    output=args.output.resolve()
    assert not output.exists(),"target bundle already exists"
    output.mkdir(parents=True,exist_ok=False)
    dest=output/"bin"
    dest.mkdir()
    entries=(
        ("go-supervisor","csqtt-core-go-supervisor-arm64",args.go),
        ("original-client","csqtt-core-original-client-250-arm64",args.client),
        ("derived-server","csqtt-core-derived-server-250-arm64",args.server),
        ("sing-box","csqtt-core-sing-box-arm64",args.sing_box),
    )
    files={}
    for role,name,path in entries:
        source=read_executable(path)
        sha=digest(source)
        if role in APPROVED:
            assert sha==APPROVED[role],f"refusing unverified {role}: {sha}"
        if role=="go-supervisor":
            # The Go source is built from a candidate-only branch, never a
            # replacement of the deployed NanoPi controller.
            assert len(source)>100_000,"unexpectedly tiny Go controller ELF"
        if role=="sing-box":
            assert len(source)>1_000_000,"unexpectedly tiny sing-box ELF"
        target=dest/name
        target.write_bytes(source)
        target.chmod(0o755)
        files[name]={"sha256":sha,"size":len(source),"role":role}
    assert len(files)==4
    manifest={
        "product":"CSQTT Core",
        "status":"experimental-not-approved-for-production",
        "architecture":"linux-arm64",
        "originalCsqtt250Commit":SRC,
        "derivedCoreCommit":CORE,
        "wireProtocol":"CSQTT-WIRE-3",
        "goSupervisor":"candidate-opt-in-only",
        "entrypoint":"csqtt-core-go-supervisor-arm64",
        "tests":"real Go supervisor start/cleanup; isolated Rust client/server E2E accepted separately; combined VPN/SOCKS/sing-box E2E still pending",
        "globalDefaultRoute":False,
        "files":files,
    }
    (output/"manifest.json").write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+"\n")
    (output/"README-NOT-FOR-PRODUCTION.txt").write_text(
        "CSQTT Core 2.5.0 WIRE-3 candidate. NOT deployable to working NanoPi.\n"
        "Go supervisor opt-in must be isolated-only; all credentials are test-only.\n"
        "Full Go→Rust→SOCKS5→sing-box end-to-end and unified rollback are not yet accepted.\n"
    )
    assert len(list(dest.iterdir()))==4
    print("CSQTT_250_EXPERIMENTAL_ARM64_BUNDLE_FOUR_PINNED_COMPONENTS_PASS")
    print("Original client and Core8 SHA256 verified; no old WIRE-4 server in bundle")

if __name__=="__main__":
    main()
