#!/usr/bin/env python3
"""Negative inventory/unsafe member tests independent of the large real ZIP."""
import importlib.util
import io
from pathlib import Path
import stat
import unittest
import zipfile

path = Path(__file__).with_name("verify-csqtt-core-250-rc-archive.py")
spec = importlib.util.spec_from_file_location("csqtt_rc_archive", path)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)


def make(entries):
    memory = io.BytesIO()
    with zipfile.ZipFile(memory, "w") as z:
        for name, typ in entries:
            info = zipfile.ZipInfo(name)
            info.external_attr = (typ | 0o755) << 16
            z.writestr(info, b"test")
    memory.seek(0)
    return zipfile.ZipFile(memory)


class ArchiveSafety(unittest.TestCase):
    def test_reject_parent_traversal(self):
        with make([("candidate/../secret", stat.S_IFREG)]) as z:
            with self.assertRaisesRegex(ValueError,"unsafe"):
                mod.verify_members(z)

    def test_reject_absolute_member(self):
        with make([("/root/secret", stat.S_IFREG)]) as z:
            with self.assertRaisesRegex(ValueError,"unsafe"):
                mod.verify_members(z)

    def test_reject_unknown_payload(self):
        with make([("malicious.env", stat.S_IFREG)]) as z:
            with self.assertRaisesRegex(ValueError,"unrecognized"):
                mod.verify_members(z)

    def test_reject_symlink(self):
        with make([(f"{mod.FOLDER}/bin/csqtt-core-sing-box-arm64", stat.S_IFLNK)]) as z:
            with self.assertRaisesRegex(ValueError,"symlink"):
                mod.verify_members(z)

    def test_reject_duplicate_names(self):
        with make([("same", stat.S_IFREG),("same", stat.S_IFREG)]) as z:
            with self.assertRaisesRegex(ValueError,"duplicate"):
                mod.verify_members(z)

    def test_reject_wrong_member_set(self):
        with make([(f"{mod.FOLDER}/manifest.json",stat.S_IFREG)]) as z:
            with self.assertRaisesRegex(ValueError,"inventory"):
                mod.verify_members(z)


if __name__=="__main__":
    unittest.main(verbosity=2)
