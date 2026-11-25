#!/usr/bin/env python3
"""Verify baseline/candidate separation without running either installer."""
from hashlib import sha256
from pathlib import Path
import unittest
from csqtt_deploy_candidate import BASE_SHA, CANDIDATE_SHA, candidate_bytes

ROOT = Path(__file__).resolve().parent.parent
BASE = ROOT / "server-assets/deploy-csqtt-core-250.sh"

class Reproduction(unittest.TestCase):
    def test_reproducible_and_separate(self):
        data = BASE.read_bytes()
        self.assertEqual(sha256(data).hexdigest(), BASE_SHA)
        first = candidate_bytes(data)
        self.assertEqual(sha256(first).hexdigest(), CANDIDATE_SHA)
        self.assertEqual(first, candidate_bytes(data))
        self.assertEqual(BASE.read_bytes(), data)
        self.assertIn(b'CSQTT_UNINSTALL_OK', first)
        self.assertNotIn(b'CSQTT_UNINSTALL_OK', data)
        for script in (data, first):
            self.assertIn(b'CSQTT_CORE_ALLOW_MUTATION:-', script)
            self.assertIn(b'isolated-lab-only', script)
    def test_drift_rejected(self):
        data = BASE.read_bytes()
        for bad in (b'', data+b'\n', candidate_bytes(data)):
            with self.assertRaises(ValueError):
                candidate_bytes(bad)

if __name__ == '__main__':
    unittest.main(verbosity=2)
