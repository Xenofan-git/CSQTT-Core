#!/usr/bin/env python3
"""Exercise only candidate preparation; no services, SSH or installer main."""
import hashlib
import os
from pathlib import Path
import platform
import sqlite3
import subprocess
import tempfile
import unittest
from csqtt_deploy_lifecycle_candidate import lifecycle_bytes, LIFECYCLE_SHA

ROOT = Path(__file__).resolve().parent.parent

class LifecycleCandidate(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.baseline = (ROOT/"server-assets/deploy-csqtt-core-250.sh").read_bytes()
        cls.script = lifecycle_bytes(cls.baseline).decode()

    def test_reproduction_and_closed_update_gate(self):
        self.assertEqual(hashlib.sha256(self.script.encode()).hexdigest(), LIFECYCLE_SHA)
        self.assertEqual(self.script.encode(), lifecycle_bytes(self.baseline))
        self.assertEqual((ROOT/"server-assets/deploy-csqtt-core-250.sh").read_bytes(), self.baseline)
        subprocess.run(["bash","-n"],input=self.script,text=True,check=True)
        # Negative gate returns before opening its log or invoking an operation.
        if os.geteuid()==0:
            result=subprocess.run(["bash","-s","update"],input=self.script,text=True,
                                  env={"PATH":"/usr/bin:/bin"},capture_output=True,timeout=5)
            self.assertEqual(result.returncode,42,result.stderr)
            self.assertIn("isolated-lab-only",result.stderr)

    def prepare(self, unsafe=False, missing_db=False):
        def function(name):
            start=self.script.index(name+"() {")
            return self.script[start:self.script.index("\n}\n",start)+3]
        with tempfile.TemporaryDirectory(prefix="csqtt-update-preparation-") as tmp:
            root=Path(tmp); state=root/"state"; state.mkdir()
            env=state/"core.env"
            original=b'CSQTT_WEB_USER="old-admin"\nCSQTT_WEB_PASS="old-secret"\nCSQTT_PUBLIC_IP="192.0.2.77"\n'
            env.write_bytes(original)
            if unsafe:
                env.rename(state/"original.env")
                env.symlink_to(state/"original.env")
            db=state/"csqtt.db"
            if not missing_db:
                with sqlite3.connect(db) as con:
                    con.execute("CREATE TABLE config (value TEXT)")
                    con.execute("INSERT INTO config VALUES ('saved-password')")
            old_db=db.read_bytes() if db.exists() else None
            payload=bytearray(64);payload[:4]=b"\x7fELF"
            payload[18:20]=(183 if platform.machine() in ("aarch64","arm64") else 62).to_bytes(2,"little")
            binary=root/"inert-elf";binary.write_bytes(payload)
            preamble='set -Eeuo pipefail\nlog_info() { :; }\ndie() { exit "$2"; }\n'
            code=preamble+function("validate_candidate_environment")+function("prepare_uploaded_release")+"\nprepare_uploaded_release\n"
            settings=dict(os.environ,PRESERVE_CONFIG="1",CSQTT_CONFIG_DIR=str(state),
                CSQTT_DATABASE_FILE="csqtt.db",CSQTT_ENV_FILE=str(env),
                CSQTT_DEPLOY_OVERRIDES_FILE=str(state/"overrides.json"),
                UPLOAD_BINARY=str(binary),UPLOAD_ENV_FILE=str(root/"upload.env"),
                UPLOAD_OVERRIDES_FILE=str(root/"upload.json"),EXIT_PREFLIGHT_FAILED="20",
                CSQTT_CORE_VERIFIED_BINARY_SHA256=hashlib.sha256(payload).hexdigest())
            result=subprocess.run(["bash","-s"],input=code,text=True,env=settings,capture_output=True,timeout=5)
            if unsafe or missing_db:
                self.assertEqual(result.returncode,20,result.stderr)
                self.assertFalse((root/"upload.env").exists())
            else:
                self.assertEqual(result.returncode,0,result.stderr)
                self.assertEqual((root/"upload.env").read_bytes(),original)
                self.assertEqual((root/"upload.json").read_text(),'{"main_password":""}\n')
                self.assertEqual(db.read_bytes(),old_db)
                self.assertEqual(env.read_bytes(),original)

    def test_update_preserves_credentials_and_database(self):
        self.prepare()

    def test_update_rejects_unsafe_or_missing_state(self):
        self.prepare(unsafe=True)
        self.prepare(missing_db=True)

if __name__=="__main__":
    unittest.main(verbosity=2)
