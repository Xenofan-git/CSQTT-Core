#!/usr/bin/env python3
"""Execute unmodified CSQTT v2.5 Rust ELF against real VK Calls.

Never prints a VK Calls hash, response body, access token, TURN credentials,
private profile, or request URI. Ephemeral GitHub ARM64 CI only.
"""
from __future__ import annotations
import hashlib
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

assert os.environ.get("GITHUB_ACTIONS")=="true"
assert os.environ.get("RUNNER_ENVIRONMENT")=="github-hosted"
client=Path("candidate/bin/csqtt-core-original-client-250-arm64")
assert client.is_file() and not client.is_symlink()
sha=hashlib.sha256(client.read_bytes()).hexdigest()
assert sha=="3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa"
client.chmod(0o755)
h=os.environ.pop("VK_CALL_HACH_1","")
if not h:
    print("REAL_VK_ORIGINAL_250_BLOCKED_NO_REPOSITORY_SECRET",flush=True)
    sys.exit(22)
hashes=[x.strip() for x in re.split(r'[\s,;]+',h) if x.strip()]
# Single preexisting hash only, never generate or guess private call IDs.
h=""
assert len(hashes)<=6
print("REAL_VK_ORIGINAL_250_PINNED_ELF_SHA256_OK",flush=True)
print("REAL_VK_ORIGINAL_250_HASH_COUNT",len(hashes),flush=True)
env=os.environ.copy()
with tempfile.TemporaryDirectory(prefix="csqtt-real-vk-validation-") as temp:
    os.chmod(temp,0o700)
    output=Path(temp)/"private-rust-validation.log"
    with output.open("xb") as f:
        os.chmod(output,0o600)
        # The Rust validator prints usable hash in its structured output; never
        # emit raw stdout/stderr, including on timeout/error.
        result=subprocess.run(
            [str(client.absolute()),"--vk",",".join(hashes),
             "--vk-hash-mode","manual","--validate-vk-hashes"],
            stdout=f,stderr=subprocess.STDOUT,env=env,timeout=115)
    # Only interpret structured statuses, never print messages or hashes.
    import json
    statuses=[]
    for line in output.read_text(errors="replace").splitlines():
        if "HASH_CHECK:" not in line:continue
        try: obj=json.loads(line.split("HASH_CHECK:",1)[1])
        except (ValueError,TypeError):continue
        st=obj.get("status")
        if st in ("valid","invalid","unavailable"):statuses.append(st)
    print("REAL_VK_ORIGINAL_250_API_RESULT_COUNT",len(statuses),flush=True)
    print("REAL_VK_ORIGINAL_250_STATUSES",",".join(statuses) if statuses else "no-result",flush=True)
    print("REAL_VK_ORIGINAL_250_VALIDATOR_EXIT",result.returncode,flush=True)
    if result.returncode or len(statuses)!=len(hashes) or any(st!="valid" for st in statuses):
        print("REAL_VK_ORIGINAL_250_HASH_PREFLIGHT_NOT_PASSED",flush=True)
        sys.exit(1)
    print("REAL_VK_ORIGINAL_250_HASH_PREFLIGHT_PASSED",flush=True)
