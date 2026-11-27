#!/usr/bin/env python3
"""Read-only static audit of the three exact 2026-11-27 server build candidates.
Does not execute a binary, copy a payload, approve an asset, or install anything.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess

CANDIDATES = {
    "amd64": (11378352, "56435cc0e443b2e909064cdcf5add390369fa0e317be51d74c60119f336c1f31", "ELF64", "Advanced Micro Devices X86-64", "/lib64/ld-linux-x86-64.so.2"),
    "arm64": (9589352, "8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d", "ELF64", "AArch64", "/lib/ld-linux-aarch64.so.1"),
    "armv7": (7876704, "718fdcd3a899205e9ec66041203939c16e9101ece7134d2c0b65f28e239a1cc4", "ELF32", "ARM", "/lib/ld-linux-armhf.so.3"),
}
LIBRARIES = {"libc.so.6", "libm.so.6", "libgcc_s.so.1", "libstdc++.so.6"}

def require(condition, message):
    if not condition:
        raise ValueError(message)

def readelf(path, flag):
    return subprocess.check_output(["readelf", "--wide", flag, str(path)], text=True,
                                   env={**os.environ, "LC_ALL": "C"}, timeout=20)

def version_needs(text):
    needs = {}
    current = None
    for line in text.splitlines():
        if line.startswith("Version needs section "):
            current = None
        m = re.search(r"File:\s+(\S+)\s+Cnt:", line)
        if m:
            current = m.group(1)
            needs[current] = set()
        m = re.search(r"Name:\s+(\S+)\s+Flags:", line)
        if current and m:
            needs[current].add(m.group(1))
    return needs

def definitions(text):
    active = False
    result = set()
    for line in text.splitlines():
        if line.startswith("Version definition section "):
            active = True
        elif line.startswith("Version needs section "):
            active = False
        if active:
            m = re.search(r"Rev:.*\bName:\s+(\S+)", line)
            if m:
                result.add(m.group(1))
    return result

def audit(arch, path, libroot):
    size, sha, cls, machine, interpreter = CANDIDATES[arch]
    st = path.lstat()
    require(stat.S_ISREG(st.st_mode), "not a regular non-symlink candidate")
    require(st.st_size == size, "candidate size mismatch")
    require(hashlib.sha256(path.read_bytes()).hexdigest() == sha, "candidate SHA mismatch")
    header = readelf(path, "-h")
    require(re.search(r"Class:\s+" + cls + r"\b", header), "ELF class mismatch")
    require(re.search(r"Machine:\s+" + re.escape(machine) + r"\s*$", header, re.M), "machine mismatch")
    require("little endian" in header, "endianness mismatch")
    require(interpreter in readelf(path, "-l"), "interpreter mismatch")
    attrs = {}
    if arch == "armv7":
        require("hard-float ABI" in header, "ARM hard-float ABI missing")
        attributes = readelf(path, "-A")
        for key, value in (("Tag_CPU_arch", "v7"), ("Tag_ABI_VFP_args", "VFP registers"),
                           ("Tag_Advanced_SIMD_arch", "NEONv1")):
            require(re.search(r"^\s*" + key + r":\s*" + re.escape(value) + r"\s*$", attributes, re.M),
                    "ARM attribute mismatch: " + key)
            attrs[key] = value
    dynamic = readelf(path, "-d")
    require(not re.search(r"\((RPATH|RUNPATH|TEXTREL)\)", dynamic), "unexpected dynamic tag")
    libraries = set(re.findall(r"Shared library: \[(.*?)\]", dynamic))
    require(libraries == LIBRARIES, "unexpected needed libraries")
    needs = version_needs(readelf(path, "-V"))
    require(set(needs) == libraries and all(needs.values()), "incomplete version needs")
    counts = {}
    for lib, required in sorted(needs.items()):
        provider = libroot / lib
        require(provider.is_file(), "missing provider " + lib)
        ph = readelf(provider, "-h")
        require(re.search(r"Class:\s+" + cls + r"\b", ph) and
                re.search(r"Machine:\s+" + re.escape(machine) + r"\s*$", ph, re.M),
                "provider architecture mismatch " + lib)
        missing = required - definitions(readelf(provider, "-V"))
        require(not missing, lib + " missing versions " + str(sorted(missing)))
        counts[lib] = len(required)
    return {"architecture": arch, "sha256": sha, "size": size, "interpreter": interpreter,
            "attributes": attrs, "version_requirements_matched": counts, "library_root": str(libroot),
            "scope": "static candidate ABI/version-node audit only; not runtime acceptance"}

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("architecture", choices=CANDIDATES)
    p.add_argument("candidate", type=Path)
    p.add_argument("library_root", type=Path)
    args = p.parse_args()
    print(json.dumps(audit(args.architecture, args.candidate, args.library_root),
                     ensure_ascii=False, sort_keys=True))

if __name__ == "__main__":
    main()
