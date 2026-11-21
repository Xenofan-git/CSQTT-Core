#!/usr/bin/env python3
"""Provenance-only packaging of already-tested ARM64 CSQTT Core 2.5 binaries.

No compiling, no running host services, no routers, no global route changes.
GitHub-hosted ARM64 CI only; never promote to production automatically.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat

root=Path("candidate-input")
out=Path("csqtt-core-250-wire3-arm64-validated-rc")
assert os.environ.get("GITHUB_ACTIONS")=="true"
assert os.environ.get("RUNNER_ENVIRONMENT")=="github-hosted"
assert not out.exists()
m=json.loads((root/"manifest.json").read_text(encoding="utf-8"))
assert m["status"]=="experimental-not-approved-for-production"
assert m["wireProtocol"]=="CSQTT-WIRE-3"
assert m["architecture"]=="linux-arm64"
approved={
 "csqtt-core-original-client-250-arm64":"3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa",
 "csqtt-core-derived-server-250-arm64":"a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706",
 "csqtt-core-sing-box-arm64":"d953af885f11d6b547c21a3c0ffe70796963afc802fab959d548311002b4908a",
}
assert len(m["files"])==4
assert set(m["files"])==set(approved)|{"csqtt-core-go-supervisor-arm64"}
out.mkdir()
(out/"bin").mkdir()
for name,meta in m["files"].items():
    binary=root/"bin"/name
    assert binary.is_file() and not binary.is_symlink()
    assert binary.stat().st_mode & stat.S_IXUSR
    with binary.open("rb") as fh:
        assert fh.read(4)==b"\x7fELF"
    sha=hashlib.sha256(binary.read_bytes()).hexdigest()
    assert sha==meta["sha256"]
    if name in approved: assert sha==approved[name]
    shutil.copy2(binary,out/"bin"/name)
    assert hashlib.sha256((out/"bin"/name).read_bytes()).hexdigest()==sha
    print("CSQTT_250_RC_ELF_SHA256_PASS",name,sha,flush=True)
assert m["files"]["csqtt-core-go-supervisor-arm64"]["role"]=="go-supervisor"
m["status"]="validated-isolated-release-candidate-not-production"
m["compiledGoSourceCommit"]="b5d5dc0b50"
m["goTunFix"]="nonblocking unix.Read/unix.Write and unix.Poll; no global route"
m["testEvidence"]={
 "GoUnitLifecycleRaceOriginalClientAndBundle":"44912292825 PASS",
 "SystemdAtomicUpgradeFailureRollback":"44912627426 PASS",
 "FullchainRealTcpAndUdpSameGoTunSourceSyntheticTurn":"44912824079 PASS",
 "FullchainTcpUdpLooseRpFilterPreservedScopedMark":"44912386606 PASS",
 "ExactOriginalRustClientCore8Pair":"44891239516 PASS",
 "OriginalRustClientRealGoLaunch":"44894005565 PASS"
}
m["limitations"]=[
 "Full joined TCP/UDP lab uses original-source Rust 2.5 with test-only local TURN creds, NOT unchanged production ELF",
 "Byte-identical original Rust release ELF passed separate pair/launch, NOT one production VK-authenticated fullchain session",
 "Do NOT deploy to working NanoPi without explicit promotion; legacy WIRE-4 remains unchanged",
 "No host default route, rp_filter, DNS, nftables, or production service changes"
]
(out/"manifest.json").write_text(json.dumps(m,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
shutil.copy2("adapt/CSQTT-CORE-250-WIRE3-RELEASE-CANDIDATE-ACCEPTANCE-2026-11-21.md",out/"EVIDENCE.md")
(out/"README-CANDIDATE.txt").write_text(
 "CSQTT Core 2.5.0 WIRE-3 ARM64 candidate — NOT a production rollout.\n"
 "Includes unchanged original Rust 2.5 ELF, verified derived Core8 WIRE-3, sing-box 1.14.4.\n"
 "Go raw TUN fix passed lifecycle/race, synthetic TURN WIRE-3 TCP/UDP E2E and rollback.\n"
 "Production VK-authenticated fullchain with unchanged Rust ELF was NOT verified.\n"
 "No changes to working NanoPi until explicit approval for deployment.\n"
 "See manifest.json, EVIDENCE.md, SHA256SUMS.\n",encoding="utf-8")
files=sorted(p for p in out.rglob("*") if p.is_file() and p.name!="SHA256SUMS")
assert len(files)==7,len(files)
(out/"SHA256SUMS").write_text("\n".join(
 hashlib.sha256(p.read_bytes()).hexdigest()+"  "+str(p.relative_to(out)) for p in files
)+"\n")
print("CSQTT_250_RC_FOUR_COMPONENT_EVIDENCE_MANIFEST_SHA256_PASS",flush=True)
