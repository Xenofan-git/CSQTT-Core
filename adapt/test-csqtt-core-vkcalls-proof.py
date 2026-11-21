#!/usr/bin/env python3
"""Synthetic credential-laden log tests; NO real secrets, requests, or network."""
import importlib.util
import io
import os
from pathlib import Path
import tempfile
import unittest
from contextlib import redirect_stdout

loader = importlib.util.spec_from_file_location(
    "csqtt_core_vkcalls_proof",
    Path(__file__).with_name("csqtt_core_vkcalls_proof.py"),
)
mod = importlib.util.module_from_spec(loader)
loader.loader.exec_module(mod)


def log_sample(stream=1, count=2):
    # Fake secret/sensitive strings MUST never leak to proof output.
    return "\n".join([
        "error URL https://api.vk.example/token?access_token=FAKE_PRIVATE_SECRET",
        f"[STREAM {stream}] [VKCalls] step1 OK, anonymous_token (99 chars)",
        f"[STREAM {stream}] [VKCalls] step2 OK, user_id=1522, secret (99 chars)",
        f"[STREAM {stream}] [VKCalls] step3 OK, OK anonymToken (99 chars)",
        f"[STREAM {stream}] [VKCalls] step4 OK, OK session_key (99 chars)",
        f"[STREAM {stream}] [VKCalls] SUCCESS, TURN urls={count}",
        "FAKE_PRIVATE_SECRET",
    ])


class SafeProofTests(unittest.TestCase):
    def test_valid_fixture_returns_status_only(self):
        result = mod.proof_status(log_sample())
        self.assertEqual(result, (
            "VK_CALLS_STEPS_1_TO_4_CONFIRMED",
            "VK_CALLS_TURN_URLS_PRESENT",
        ))
        self.assertNotIn("FAKE_PRIVATE_SECRET", "\n".join(result))
        self.assertNotIn("https", "\n".join(result))

    def test_no_turn_urls_rejects_completion(self):
        self.assertNotIn("VK_CALLS_TURN_URLS_PRESENT", mod.proof_status(log_sample(count=0)))

    def test_unrelated_and_interleaved_streams_not_combined(self):
        a = "\n".join(log_sample(stream=1).splitlines()[1:3])
        b = "\n".join(log_sample(stream=2).splitlines()[3:6])
        self.assertNotIn("VK_CALLS_TURN_URLS_PRESENT", mod.proof_status(a + "\n" + b))

    def test_out_of_order_not_claimed(self):
        sample = log_sample()
        lines = sample.splitlines()
        self.assertNotIn("VK_CALLS_TURN_URLS_PRESENT", mod.proof_status(
            "\n".join([*lines[:2], lines[-2], *lines[2:-2]])
        ))

    def test_untrusted_error_echo_is_ignored(self):
        self.assertFalse(mod.proof_status(
            "VK API error: response=[STREAM 1] [VKCalls] SUCCESS, TURN urls=12"
        ))

    def test_private_log_required_and_symlink_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            a = Path(directory) / "private.log"
            a.write_text(log_sample())
            a.chmod(0o600)
            self.assertIn("VK_CALLS_TURN_URLS_PRESENT", mod.proof_status(mod.load_private_log(a)))
            a.chmod(0o644)
            with self.assertRaises(ValueError):
                mod.load_private_log(a)
            a.chmod(0o600)
            b = Path(directory) / "link.log"
            b.symlink_to(a)
            with self.assertRaises(ValueError):
                mod.load_private_log(b)

    def test_bounded_log(self):
        with tempfile.TemporaryDirectory() as directory:
            a = Path(directory) / "private.log"
            a.write_bytes(b"x" * (mod.MAX_BYTES + 1))
            a.chmod(0o600)
            with self.assertRaises(ValueError):
                mod.load_private_log(a)

    def test_no_secret_in_cli_output(self):
        with tempfile.TemporaryDirectory() as directory:
            a = Path(directory) / "private.log"
            a.write_text(log_sample())
            a.chmod(0o600)
            stdout = io.StringIO()
            original = os.sys.argv
            try:
                os.sys.argv = ["proof", str(a)]
                with redirect_stdout(stdout):
                    rc = mod.main()
            finally:
                os.sys.argv = original
            self.assertEqual(rc, 0)
            self.assertNotIn("FAKE_PRIVATE_SECRET",stdout.getvalue())
            self.assertNotIn("api.vk.example",stdout.getvalue())

if __name__ == "__main__":
    unittest.main(verbosity=2)
