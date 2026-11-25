"""Reproduce the unapproved installer candidate without changing the pinned asset."""
import hashlib
from pathlib import Path
import subprocess
import tempfile

BASE_SHA = "18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68"
CANDIDATE_SHA = "b0475a474b4e6f94d7b1999a015f021ca472f575a7fac03d72286b7883e3d867"
PATCH = Path(__file__).resolve().with_name("csqtt-core-uninstall-candidate.patch")

def candidate_bytes(baseline: bytes) -> bytes:
    if hashlib.sha256(baseline).hexdigest() != BASE_SHA:
        raise ValueError("unexpected baseline installer digest")
    with tempfile.TemporaryDirectory(prefix="csqtt-deploy-candidate-source-") as tmp:
        path = Path(tmp) / "server-assets/deploy-csqtt-core-250.sh"
        path.parent.mkdir()
        path.write_bytes(baseline)
        subprocess.run(["patch", "--batch", "--fuzz=0", "--forward",
                        "--no-backup-if-mismatch", "-p1", "-i", str(PATCH)],
                       cwd=tmp, check=True, capture_output=True, timeout=12)
        result = path.read_bytes()
        if hashlib.sha256(result).hexdigest() != CANDIDATE_SHA:
            raise ValueError("unexpected candidate installer digest")
        subprocess.run(["bash", "-n", str(path)], check=True, timeout=12)
        return result
