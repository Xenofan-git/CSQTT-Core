#!/usr/bin/env python3
"""Fail-closed source patch contract, with optional actual private-source review.

No TUN, sysctl, network, live binaries or external credentials. For --source-root
read the UNPATCHED experimental original sources and only transform strings
in memory; never modify input source files.
"""
import argparse
import hashlib
import importlib.util
from pathlib import Path

BASE_HASHES={
    "tun_device.rs":"10e99e2a8316f82afbbc6cd352e998bdf03e4f59a22fe70ba17752c8f5e8da4a",
    "model.rs":"b4afe4f8b19af1063d06d61eb7900af15475ff0ae2ada744672329ac5d12059b",
    "web_panel.rs":"55777e0864de9f927010cd2fe7312e0d9f89b8af3c1d293ba727cd075c44ff96",
    "protocol.rs":"a9aeee7ea83271eb619e71623cfbde9201f398e1371d36caef69d45f5d61fd1a",
}
PATCHED_HASHES={
    "tun_device.rs":"2e68c3b0b85a868b96179b1c5a443c2763f5ed54e4c8f2a47d6d2aed068b90d2",
    "model.rs":"9e9b59b474f9bc94c7e90ba69c7e70a980ce8980d99c33c5d75d0b6f39f14ffa",
    "web_panel.rs":"71b0bc5fa3208d8dd21b5d0f998e973ac5f96bbb94f50f8caf4ecb7b982970ae",
    "protocol.rs":"351947c3f8ecc77ea77448ca95409fcdc89b45a952a2a877897d672abceff9be",
}

def assert_reject(fn, text):
    try:fn(text)
    except ValueError:return
    raise AssertionError("unexpected acceptance of malformed/duplicate patch")

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--source-root",type=Path)
    args=parser.parse_args()
    file=Path(__file__).with_name("patch-csqttr-253-selective-core8.py")
    spec=importlib.util.spec_from_file_location("selective_patch",file)
    patch=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(patch)
    assert patch.once("start OLD end","OLD","NEW","fixture")=="start NEW end"
    print("SELECTIVE_STRICT_SINGLE_ANCHOR_PASS")
    for sample in ("start END end","OLD OLD",""):
        try:patch.once(sample,"OLD","NEW","negative")
        except ValueError:pass
        else:raise AssertionError("missing/duplicate anchor accepted")
    print("SELECTIVE_MISSING_OR_DUPLICATE_ANCHOR_REJECT_PASS")
    assert_reject(patch.tun,"wrong subnet 10.66.67.0/24")
    assert_reject(patch.model,"wrong owner")
    assert_reject(patch.web,"unrecognized import logic")
    assert_reject(patch.protocol,"unrecognized protocol")
    print("SELECTIVE_WRONG_SOURCE_REJECT_PASS")
    if args.source_root:
        old=args.source_root.resolve()
        methods={"tun_device.rs":patch.tun,"model.rs":patch.model,
                 "web_panel.rs":patch.web,"protocol.rs":patch.protocol}
        for name,fn in methods.items():
            text=(old/name).read_text(encoding="utf8")
            observed=hashlib.sha256(text.encode()).hexdigest()
            assert observed==BASE_HASHES[name],(name,"baseline hash changed",observed)
            new=fn(text)
            derived=hashlib.sha256(new.encode()).hexdigest()
            assert derived==PATCHED_HASHES[name],(name,"unexpected output",derived)
            assert_reject(fn,new)
        print("SELECTIVE_PINNED_REAL_SOURCE_4_OF_4_PASS")
        print("SELECTIVE_REAL_SOURCE_REAPPLY_REJECT_4_OF_4_PASS")
    print("CSQTTR_253_SELECTIVE_OFFLINE_PATCH_CONTRACT_PASS")
if __name__=="__main__":main()
