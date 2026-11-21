#!/usr/bin/env python3
"""Exercise EXACT lab shell snapshot/EXIT trap against disposable fake sysctls.

Never runs the ARM64 ELF, opens sockets, writes /proc or uses credentials.
This is not a test of the real VM or real VK/TURN dataplane.
"""
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("verify-core8-private-guest-sysctl-restore.sh")
GUARD = SCRIPT.read_text(encoding="utf-8")
START = 'paths="$tmp/original-sysctls"'
END = "trap 'exit 130' INT"
assert GUARD.count(START) == 1 and GUARD.count(END) == 1
SNAPSHOT_AND_EXIT_TRAP = GUARD.split(START, 1)[1].split(END, 1)[0]
SNAPSHOT_AND_EXIT_TRAP = START + SNAPSHOT_AND_EXIT_TRAP


class GuestSysctlMockTests(unittest.TestCase):
    def run_fixture(self, mutate: str, exit_code: int = 0):
        with tempfile.TemporaryDirectory(prefix="csqtt-guard-synthetic-") as base:
            root = Path(base) / "fake-proc-sys"
            initial = {
                "net/ipv4/ip_forward": "0",
                "net/ipv6/conf/all/forwarding": "0",
                "net/ipv4/conf/all/rp_filter": "1",
                "net/ipv4/conf/default/rp_filter": "1",
                "net/ipv4/tcp_sack": "1",
            }
            for relative, value in initial.items():
                file = root / relative
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_text(value + "\n")
            private = Path(base) / "snapshot"
            private.mkdir()
            shell_script = (
                "set -euo pipefail\n"
                + 'tmp="' + str(private) + '"\n'
                + SNAPSHOT_AND_EXIT_TRAP.replace("/proc/sys/", str(root) + "/")
                + "\n"
                + mutate.replace("@ROOT@", str(root))
                + "\nexit " + str(exit_code) + "\n"
            )
            result = subprocess.run(
                ["bash", "-c", shell_script],
                capture_output=True, text=True, timeout=12, check=False,
            )
            final = {
                relative: (root / relative).read_text().strip()
                if (root / relative).exists() and not (root / relative).is_symlink()
                else None
                for relative in initial
            }
            return result.returncode, result.stdout, final, initial

    def test_restore_multiple_sysctls(self):
        code, out, final, initial = self.run_fixture(
            "printf '1\\n' > '@ROOT@/net/ipv4/ip_forward'\n"
            "printf '1\\n' > '@ROOT@/net/ipv6/conf/all/forwarding'\n"
            "printf '2\\n' > '@ROOT@/net/ipv4/conf/all/rp_filter'"
        )
        self.assertEqual(code, 0)
        self.assertEqual(final, initial)
        self.assertIn("GUEST_SYSCTL_SNAPSHOT_RESTORE_PASS", out)

    def test_failed_original_command_retains_exit_status(self):
        code, out, final, initial = self.run_fixture(
            "printf '1\\n' > '@ROOT@/net/ipv4/ip_forward'", 7
        )
        self.assertEqual(code, 7)
        self.assertEqual(final, initial)
        self.assertIn("GUEST_SYSCTL_SNAPSHOT_RESTORE_PASS", out)

    def test_missing_snapshot_target_fails_closed(self):
        code, out, final, _ = self.run_fixture(
            "rm '@ROOT@/net/ipv4/ip_forward'"
        )
        self.assertNotEqual(code, 0)
        self.assertIsNone(final["net/ipv4/ip_forward"])
        self.assertIn("GUEST_SYSCTL_SNAPSHOT_RESTORE_FAIL", out)

    def test_symlink_substitution_fails_closed(self):
        code, out, final, _ = self.run_fixture(
            "rm '@ROOT@/net/ipv4/ip_forward'\n"
            "ln -s /dev/null '@ROOT@/net/ipv4/ip_forward'"
        )
        self.assertNotEqual(code, 0)
        self.assertIsNone(final["net/ipv4/ip_forward"])
        self.assertIn("GUEST_SYSCTL_SNAPSHOT_RESTORE_FAIL", out)

    def test_empty_snapshot_has_no_false_failure(self):
        code, out, _, _ = self.run_fixture("true")
        self.assertEqual(code, 0)
        self.assertIn("GUEST_SYSCTL_SNAPSHOT_RESTORE_PASS", out)

    def test_strict_guest_and_private_only_guards(self):
        checks = (
            "WRONG_ISOLATED_GUEST", "GUEST_HAS_DEFAULT_ROUTE",
            "WRONG_CORE8_HASH", "WRONG_WIRE_REVISION",
            "GUEST_UDP_PORT_BUSY", "GUEST_WEB_PORT_BUSY",
            "--listen 127.0.0.1:46871", "GUEST_CORE8_SMOKE_FAILED",
            "GUEST_UDP_STOP_FAILED",
        )
        for check in checks:
            self.assertIn(check, GUARD)
        self.assertNotIn("--listen 0.0.0.0:", GUARD)
        self.assertNotIn("48779", GUARD)


if __name__ == "__main__":
    unittest.main(verbosity=2)
