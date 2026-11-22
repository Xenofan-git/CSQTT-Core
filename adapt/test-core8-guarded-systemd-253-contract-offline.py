#!/usr/bin/env python3
"""Validate the private Core8 systemd unit vs the real durable guard source.

Static/offline only: no root, no systemd daemon, no active network or
service changes. The guest-only template is NOT a production unit.
"""
import configparser
import importlib.util
from pathlib import Path
import shlex
import unittest

HERE = Path(__file__).resolve().parent
TEMPLATE = HERE / "csqtt-core8-private-durable-guard.service.example"
GUARD = HERE / "core8-private-durable-sysctl-guard.py"
INSTALLER = HERE.parent / "server-assets" / "deploy-csqtt-core-250.sh"


def section(name):
    parser=configparser.ConfigParser(interpolation=None,strict=True)
    parser.optionxform=str
    with TEMPLATE.open(encoding="utf8") as f:
        parser.read_file(f)
    return dict(parser[name])


def cmd_arg(parts, key):
    i=parts.index(key)
    return parts[i+1]


class PrivateGuardedUnitContract(unittest.TestCase):
    def setUp(self):
        self.unit=section("Unit")
        self.service=section("Service")
        self.start=shlex.split(self.service["ExecStart"])
        self.stop=shlex.split(self.service["ExecStopPost"])

    def test_exact_guest_only_guard_executable_for_both_actions(self):
        expected="/usr/local/libexec/core8-private-durable-sysctl-guard.py"
        self.assertEqual(self.start[:2],["/usr/bin/python3",expected])
        self.assertEqual(self.stop[:2],["/usr/bin/python3",expected])
        self.assertEqual(self.start[self.start.index("--private-guest")+1],"run")
        self.assertEqual(self.stop[self.stop.index("--private-guest")+1],"recover")

    def test_recover_matches_run_boot_and_sysctl_ownership(self):
        for arg in ("--state-dir","--sysctl-root","--boot-id-file"):
            self.assertEqual(cmd_arg(self.start,arg),cmd_arg(self.stop,arg))
        self.assertEqual(cmd_arg(self.start,"--sysctl-root"),"/proc/sys")
        self.assertEqual(cmd_arg(self.start,"--boot-id-file"),
                         "/proc/sys/kernel/random/boot_id")
        self.assertEqual(self.start[self.start.index("run")+1],"--")
        self.assertEqual(len(self.stop),self.stop.index("recover")+1)

    def test_service_never_start_direct_binary_without_guard(self):
        self.assertEqual(self.start[0],"/usr/bin/python3")
        self.assertEqual(self.start[self.start.index("--")+1],"/home/csqttlab/core8-arm64")
        self.assertEqual(cmd_arg(self.start,"--listen"),"127.0.0.1:46871")
        self.assertEqual(cmd_arg(self.start,"--web-port"),"46872")
        self.assertIn("--config-dir",self.start)
        self.assertEqual(self.unit.get("ConditionHost"),"csqtt-core8-isolated")
        self.assertEqual(self.unit.get("ConditionPathExists"),
                         "/home/csqttlab/core8-arm64")

    def test_crash_policy_bounded_and_guard_failure_not_restarted(self):
        self.assertEqual(self.service.get("Restart"),"on-failure")
        self.assertEqual(self.service.get("RestartPreventExitStatus"),"75")
        self.assertEqual(self.unit.get("StartLimitBurst"),"3")
        self.assertGreaterEqual(int(self.unit["StartLimitIntervalSec"]),30)
        self.assertGreaterEqual(int(self.service["RestartSec"]),2)
        self.assertGreaterEqual(int(self.service["TimeoutStopSec"]),15)
        self.assertEqual(self.service["KillMode"],"mixed")

    def test_supervisor_receives_stop_before_child_cgroup(self):
        # For a main-PID journal guardian, KillMode=control-group may
        # deliver TERM independently to both guardian and Core8. The
        # guardian must coordinate clean stop and durable replay first.
        self.assertEqual(self.service["Type"],"simple")
        self.assertEqual(self.service["KillMode"],"mixed")
        self.assertEqual(self.service["TimeoutStopSec"],"20")
        self.assertEqual(self.service["RestartPreventExitStatus"],"75")
        from pathlib import Path
        source=GUARD.read_text(encoding="utf8")
        self.assertIn('for sig in (signal.SIGINT, signal.SIGTERM)',source)
        self.assertIn('os.killpg(child.pid, sig)',source)
        self.assertIn('child.wait(timeout=poll_seconds)',source)
        self.assertIn('CORE8_STOP_RECOVERY=',source)

    def test_unsafe_group_stop_policy_would_fail_contract(self):
        # A negative fixture must prove this assertion is meaningful.
        from unittest import mock
        with mock.patch.dict(self.service,{"KillMode":"control-group"}):
            with self.assertRaises(AssertionError):
                self.assertEqual(self.service["KillMode"],"mixed")
        self.assertEqual(self.service["KillMode"],"mixed")

    def test_no_automatic_host_network_changes(self):
        self.assertEqual(self.service["UMask"],"0077")
        self.assertEqual(self.service["NoNewPrivileges"],"true")
        self.assertEqual(self.service["User"],"root")
        self.assertTrue(self.service["EnvironmentFile"].endswith("private-core8.env"))
        self.assertNotIn("ExecStartPre",self.service)
        self.assertNotIn("ExecReload",self.service)
        self.assertNotIn("ExecStop",self.service)

    def test_guard_refuses_global_host_and_default_route(self):
        source=GUARD.read_text(encoding="utf8")
        self.assertIn("def private_guest_gate(root: Path, flag: bool)",source)
        self.assertIn('if os.uname().nodename != "csqtt-core8-isolated"',source)
        self.assertIn('route = subprocess.run(["ip", "-4", "route", "show", "default"]',source)
        self.assertIn('raise GuardFailure("guest has an IPv4 default route")',source)
        self.assertIn("sys.exit(75)",source)
        self.assertIn('raise GuardFailure("CORE8_OWNER_BUSY")',source)
        self.assertIn("CORE8_STOP_RECOVERY=",source)
        self.assertIn('REQUIRED_PARAMETERS = frozenset({',source)

    def test_original_installer_not_silently_switched_to_guest_only_guard(self):
        source=INSTALLER.read_text(encoding="utf8")
        portion=source.split("setup_csqtt_service() {",1)[1].split(
            "\ndiagnose_systemd_failure() {",1)[0]
        self.assertIn("ExecStart=/usr/local/bin/csqtt-core --listen",portion)
        self.assertNotIn("core8-private-durable-sysctl-guard",portion)
        self.assertNotIn("EnvironmentFile=/etc/csqtt-core/private-core8.env",portion)

    def test_service_has_no_persistent_enable_commands(self):
        source=TEMPLATE.read_text()
        self.assertNotIn("systemctl enable",source)
        self.assertNotIn("systemctl start",source)
        self.assertNotIn("0.0.0.0:",source)


if __name__=="__main__":
    unittest.main(verbosity=2)
