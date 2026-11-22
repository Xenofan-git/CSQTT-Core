#!/usr/bin/env python3
"""Create NON-PRODUCTION CSQTT Core 2.5.0 + selective 2.5.3 Linux ARM64 bundle.

This does not install or execute services. It accepts ONLY the separately
checked historical Linux Go/controller bundle and a matching new ARM64 Core8
artifact, with both source provenance and SHA256 independently verified.
"""
import argparse
from hashlib import sha256
import json
import os
from pathlib import Path
import shutil
import stat
import struct
import subprocess

ORIGINAL_SOURCE = "fd936ef087c34cc46f26111dc2361f43b8b03c07"
OLD_GO_BUNDLE_SERVER = "a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706"
PINNED_ORIGINAL_CLIENT = "3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa"
SOURCE_TREE = "2c03df9581e87fe9e1e9ab0baaa7549dadaa8d2c32f5f62cb180193de2cb605a"
PREVIOUS_FILES = {
    "csqtt-core-go-supervisor-arm64": "go-supervisor",
    "csqtt-core-original-client-250-arm64": "original-client",
    "csqtt-core-derived-server-250-arm64": "derived-server",
    "csqtt-core-sing-box-arm64": "sing-box",
}
NEW_SERVER_FILENAME = "csqtt-core-derived-server-250-arm64"

def assert_elf_arm64(p:Path)->bytes:
    if p.is_symlink() or not p.is_file():raise ValueError("nonregular Linux component file")
    if not (p.stat().st_mode & stat.S_IXUSR):raise ValueError("Linux component is not executable")
    data=p.read_bytes()
    if len(data)<20 or data[:4]!=bytes([0x7f,69,76,70]) or data[4:6]!=bytes([2,1]) or struct.unpack_from("<H",data,18)[0]!=183:
        raise ValueError("Linux component is not ELF64 ARM AArch64")
    return data

def actual_sha(data:bytes)->str:return sha256(data).hexdigest()

def read_previous_bundle(source:Path):
    manifest=json.loads((source/"manifest.json").read_text(encoding="utf8"))
    if (manifest.get("status")!="experimental-not-approved-for-production" or
        manifest.get("wireProtocol")!="CSQTT-WIRE-3" or
        manifest.get("originalCsqtt250Commit")!=ORIGINAL_SOURCE or
        manifest.get("derivedCoreCommit")!="91b154011ca16c219f57eef99462d3408dccfeeb" or
        manifest.get("globalDefaultRoute") is not False):
        raise ValueError("previous Linux Go candidate provenance mismatch")
    files=manifest.get("files")
    if not isinstance(files,dict) or set(files)!=set(PREVIOUS_FILES):
        raise ValueError("previous Linux candidate has an unexpected component set")
    validated={}
    for name,role in PREVIOUS_FILES.items():
        info=files[name]
        if info.get("role")!=role:raise ValueError("previous component role mismatch: "+name)
        content=assert_elf_arm64(source/"bin"/name)
        observed=actual_sha(content)
        if observed!=info.get("sha256") or len(content)!=info.get("size"):
            raise ValueError("previous Linux component SHA or size mismatch: "+name)
        validated[name]=content
    if actual_sha(validated["csqtt-core-original-client-250-arm64"])!=PINNED_ORIGINAL_CLIENT:
        raise ValueError("original 2.5.0 client hash mismatch")
    if actual_sha(validated[NEW_SERVER_FILENAME])!=OLD_GO_BUNDLE_SERVER:
        raise ValueError("previous bundle server is not the pinned old Core8")
    return validated,manifest

def read_new_server(source:Path,expected_commit:str):
    info=(source/"BUILD-INFO.txt").read_text(encoding="utf8").splitlines()
    properties={}
    for line in info:
        if "=" not in line:raise ValueError("malformed new ARM64 Core8 build metadata")
        k,v=line.split("=",1)
        if k in properties:raise ValueError("duplicate Core8 metadata key")
        properties[k]=v
    if properties != {
        "source_commit":expected_commit,
        "upstream_commit":ORIGINAL_SOURCE,
        "architecture":"linux-arm64",
        "status":"EXPERIMENTAL-not-deployed",
    }:
        raise ValueError("new Core8 source commit/architecture/build provenance mismatch")
    checksum=(source/"SHA256SUMS").read_text(encoding="utf8").splitlines()
    if len(checksum)!=1:raise ValueError("expected one new Core8 checksum")
    words=checksum[0].split()
    if len(words)!=2 or words[1]!="csqtt-core8-linux-arm64":
        raise ValueError("unexpected new Core8 SHA file name")
    binary=source/"csqtt-core8-linux-arm64"
    content=assert_elf_arm64(binary)
    if words[0]!=actual_sha(content):raise ValueError("new Core8 binary SHA256 mismatch")
    run=subprocess.run([str(binary),"--protocol-revision"],capture_output=True,text=True,timeout=8,check=True)
    if run.stdout.strip()!="CSQTT-WIRE-3":
        raise ValueError("new Core8 binary has wrong protocol")
    help_text=subprocess.run([str(binary),"help"],capture_output=True,text=True,timeout=8,check=True).stdout
    for expected in ("csqtt-core start","47000","47002","/etc/csqtt-core"):
        if expected not in help_text:raise ValueError("new Core8 runtime isolation mismatch "+expected)
    if any(x in help_text for x in ("46000","46002")):
        raise ValueError("new Core8 help refers to legacy ports")
    return content

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--previous",type=Path,required=True)
    parser.add_argument("--new-server",type=Path,required=True)
    parser.add_argument("--server-commit",required=True)
    parser.add_argument("--output",type=Path,required=True)
    args=parser.parse_args()
    if (os.environ.get("CSQTT_UNIFIED_BUNDLE_BUILD")!="isolated-github-runner" or
        os.environ.get("GITHUB_ACTIONS")!="true" or
        os.environ.get("RUNNER_ENVIRONMENT")!="github-hosted"):
        raise ValueError("experiment bundle build allowed only in isolated GitHub runner")
    if not (len(args.server_commit)==40 and all(c in "0123456789abcdef" for c in args.server_commit)):
        raise ValueError("full hexadecimal server source SHA required")
    target=args.output.absolute()
    if target.exists() or target.is_symlink():
        raise ValueError("refusing existing bundle output")
    prior,prior_manifest=read_previous_bundle(args.previous)
    final_server=read_new_server(args.new_server,args.server_commit)
    if actual_sha(final_server)==OLD_GO_BUNDLE_SERVER:
        raise ValueError("new server is still previous 2.5.0 artifact")
    if not target.parent.is_dir():raise ValueError("invalid bundle parent")
    target.mkdir(mode=0o700)
    b=target/"bin"
    b.mkdir(mode=0o700)
    files={}
    for name,role in PREVIOUS_FILES.items():
        content=final_server if name==NEW_SERVER_FILENAME else prior[name]
        dest=b/name
        dest.write_bytes(content)
        dest.chmod(0o755)
        files[name]={"sha256":actual_sha(content),"size":len(content),"role":role}
    # Explicitly record that the Go controller is from an older, separately
    # tested candidate, NOT falsely claim this is a full end-to-end bundle.
    result={
        "product":"CSQTT Core",
        "status":"experimental-not-approved-for-production",
        "platform":"linux-arm64",
        "originalCsqtt250Commit":ORIGINAL_SOURCE,
        "selective253Core8Commit":args.server_commit,
        "finalRustTreeSHA256":SOURCE_TREE,
        "previousVerifiedGoCandidateCore8":prior_manifest["derivedCoreCommit"],
        "wireProtocol":"CSQTT-WIRE-3",
        "globalDefaultRoute":False,
        "fullCombinedRuntimeAcceptance":False,
        "files":files,
    }
    (target/"manifest.json").write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n",encoding="utf8")
    (target/"README-NOT-FOR-PRODUCTION.txt").write_text(
      "CSQTT Core Debian/Ubuntu ARM64 experimental four-component integration.\n"
      "NO LIVE E2E ACCEPTANCE. Do not deploy on working NanoPi.\n"
      "Go supervisor comes from previously tested 2.5.0 Linux branch;\n"
      "Core8 server was replaced by selective 2.5.3 version with isolated ports.\n"
      "No changes to default host route are allowed.\n",encoding="utf8")
    print("CSQTT_CORE_LINUX_UNIFIED_253_ARM64_FOUR_BINARIES_PROVENANCE_AND_SHA_PASS")
    print("NEW_CORE8_SHA256",actual_sha(final_server))
    print("EXPERIMENTAL_NO_PRODUCTION_RELEASE")

if __name__=="__main__":main()
