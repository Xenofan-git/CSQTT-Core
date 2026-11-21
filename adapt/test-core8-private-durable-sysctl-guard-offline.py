#!/usr/bin/env python3
"""End-to-end local filesystem and subprocess tests: fake kernel files only.

No QEMU, no /proc/sys writes, no real Core8, no sockets, no firewall or VK.
All processes/lock files are owned by this test in a private TemporaryDirectory.
"""
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
import uuid

GUARD = Path(__file__).with_name("core8-private-durable-sysctl-guard.py")
spec = importlib.util.spec_from_file_location("durable_guard", GUARD)
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)

CHILD = r'''
import json, os, signal, sys, time
from pathlib import Path
cfg=json.loads(Path(sys.argv[1]).read_text())
for relative,value in cfg["parameters"].items():
    p=Path(cfg["root"])/relative
    p.write_text(value+"\n")
if cfg.get("external"):
    (Path(cfg["root"])/cfg["external"]).write_text("9\n")
Path(cfg["ready"]).write_text(str(os.getpid()))
if cfg["mode"] == "sigkill":
    os.kill(os.getpid(),signal.SIGKILL)
if cfg["mode"] == "sleep":
    time.sleep(60)
'''

def start_args(kind, state, root, boot, child=None):
    argv = [sys.executable, "-B", str(GUARD),
            "--state-dir", str(state), "--sysctl-root", str(root),
            "--boot-id-file", str(boot), kind]
    if child:
        argv.extend(["--", *child])
    return argv

class DurableGuardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="csqtt-durable-offline-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.sysroot = self.root / "fake-proc-sys"
        self.sysroot.mkdir()
        self.state = self.root / "state"
        self.boot = self.root / "boot-id"
        self.boot.write_text(str(uuid.uuid4()))
        self.previous = {}
        for relative, expected in guard.PARAMETERS.items():
            prev = {"1": "0", "2": "1", "0": "1",
                    "fq": "fq_codel", "bbr": "cubic",
                    "15": "3", "3": "1"}[expected]
            target = self.sysroot / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(prev + "\n")
            self.previous[relative] = prev
        self.children = []
        self.parents = []
        self.addCleanup(self.cleanup_processes)

    def cleanup_processes(self):
        for proc in self.children:
            if proc.poll() is None:
                os.kill(proc.pid, signal.SIGKILL)
        for proc in self.parents:
            if proc.poll() is None:
                os.kill(proc.pid, signal.SIGKILL)
        for proc in self.children + self.parents:
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                pass
            if proc.stdout:
                proc.stdout.close()
            if proc.stderr:
                proc.stderr.close()

    def values(self):
        return {key: (self.sysroot/key).read_text().strip()
                for key in self.previous}

    def run_guard(self, action="recover", child=None, timeout=12):
        return subprocess.run(start_args(action, self.state, self.sysroot,
                                         self.boot, child),
                              capture_output=True, text=True, timeout=timeout)

    def child_config(self, mode="exit", external=None):
        config = self.root / ("config-" + str(uuid.uuid4()) + ".json")
        ready = self.root / ("ready-" + str(uuid.uuid4()))
        config.write_text(json.dumps({
            "parameters": guard.PARAMETERS, "root": str(self.sysroot),
            "mode": mode, "ready": str(ready),
            "external": external
        }))
        return [sys.executable, "-c", CHILD, str(config)], ready

    def spawn_guard(self, mode="sleep"):
        child, ready = self.child_config(mode)
        parent = subprocess.Popen(
            start_args("run", self.state, self.sysroot, self.boot, child),
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.parents.append(parent)
        for _ in range(100):
            if ready.exists():
                pid = int(ready.read_text())
                return parent, pid
            if parent.poll() is not None:
                raise AssertionError(
                    "guard failed before child: " +
                    (parent.stderr.read() if parent.stderr else "")
                )
            time.sleep(0.03)
        raise AssertionError("child fixture did not start")

    def test_no_pending_journal(self):
        r = self.run_guard()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("CORE8_RECOVERY=NO_PENDING_JOURNAL", r.stdout)

    def test_normal_run_restores_all_changed_knobs(self):
        child, ready = self.child_config()
        r = self.run_guard("run", child)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertTrue(ready.is_file())
        self.assertEqual(self.values(), self.previous)
        self.assertFalse((self.state/guard.JOURNAL).exists())
        self.assertIn("CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED", r.stdout)

    def test_child_sigkill_parent_recovers_all(self):
        child, _ = self.child_config("sigkill")
        r = self.run_guard("run", child)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED", r.stdout)
        self.assertEqual(self.values(), self.previous)
        self.assertFalse((self.state/guard.JOURNAL).exists())

    def test_parent_sigkill_child_holds_lock_until_exits(self):
        parent, childpid = self.spawn_guard()
        self.assertEqual(self.values(), guard.PARAMETERS)
        os.kill(parent.pid, signal.SIGKILL)
        parent.wait(timeout=3)
        blocked = self.run_guard()
        self.assertEqual(blocked.returncode, 75)
        self.assertIn("CORE8_OWNER_BUSY", blocked.stderr)
        self.assertTrue((self.state/guard.JOURNAL).exists())
        os.kill(childpid, signal.SIGKILL)
        # Ensure QEMU-like child releases inherited lock.
        for _ in range(100):
            result = self.run_guard()
            if result.returncode == 0:
                break
            self.assertIn("CORE8_OWNER_BUSY", result.stderr)
            time.sleep(0.03)
        else:
            self.fail("recovery remained locked after child termination")
        self.assertEqual(self.values(), self.previous)
        self.assertFalse((self.state/guard.JOURNAL).exists())

    def test_simultaneous_run_fails_closed(self):
        parent, childpid = self.spawn_guard()
        child, _ = self.child_config("exit")
        second = self.run_guard("run", child)
        self.assertEqual(second.returncode, 75)
        self.assertIn("CORE8_OWNER_BUSY", second.stderr)
        self.assertEqual(self.values(), guard.PARAMETERS)
        os.kill(childpid, signal.SIGTERM)
        parent.wait(timeout=5)
        self.assertEqual(self.values(), self.previous)

    def test_external_change_retains_journal_fail_closed(self):
        rel = "net/ipv4/ip_forward"
        child, _ = self.child_config("exit", external=rel)
        r = self.run_guard("run", child)
        self.assertEqual(r.returncode, 75)
        self.assertIn("RECOVERY_UNRESOLVED", r.stderr)
        self.assertEqual((self.sysroot/rel).read_text().strip(), "9")
        self.assertTrue((self.state/guard.JOURNAL).exists())
        blocked = self.run_guard()
        self.assertEqual(blocked.returncode, 75)
        # An authorized operator or separate system component can later set
        # the expected intermediate value. Only then can guarded restore run.
        (self.sysroot/rel).write_text(guard.PARAMETERS[rel]+"\n")
        recovered = self.run_guard()
        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertEqual(self.values(), self.previous)

    def test_old_boot_journal_archived_no_cross_boot_writes(self):
        _, childpid = self.spawn_guard()
        parent = self.parents[-1]
        os.kill(parent.pid, signal.SIGKILL)
        parent.wait(timeout=3)
        os.kill(childpid, signal.SIGKILL)
        # Simulate reboot: values must NEVER be rolled back to the old boot.
        new_boot = str(uuid.uuid4())
        self.boot.write_text(new_boot)
        for rel in guard.PARAMETERS:
            (self.sysroot/rel).write_text("9\n")
        for _ in range(100):
            r = self.run_guard()
            if r.returncode == 0:
                break
            self.assertIn("CORE8_OWNER_BUSY", r.stderr)
            time.sleep(0.03)
        else:
            self.fail("lock persisted after killed child")
        self.assertIn("STALE_BOOT_ARCHIVED_WITHOUT_RESTORING", r.stdout)
        self.assertEqual(self.values(), dict.fromkeys(guard.PARAMETERS, "9"))
        self.assertEqual(len(list(self.state.glob("stale-boot-*.json"))), 1)
        self.assertFalse((self.state/guard.JOURNAL).exists())

    def test_corrupt_journal_fails_closed(self):
        self.state.mkdir(mode=0o700)
        p = self.state / guard.JOURNAL
        p.write_text('{"v":1,"values": {"../../bad":"0"}}')
        p.chmod(0o600)
        r = self.run_guard()
        self.assertEqual(r.returncode, 75)
        self.assertIn("journal", r.stderr)
        self.assertEqual(self.values(), self.previous)

    def test_proc_gate_rejects_real_host_without_private_guest(self):
        r = subprocess.run([
            sys.executable, "-B", str(GUARD),
            "--state-dir", str(self.state),
            "--sysctl-root", "/proc/sys",
            "--boot-id-file", str(self.boot), "recover"
        ], capture_output=True, text=True, timeout=8)
        self.assertEqual(r.returncode, 75)
        self.assertIn("real /proc requires", r.stderr)
        self.assertEqual(self.values(), self.previous)

    def test_rejects_symlink_substitution(self):
        rel = "net/ipv4/ip_forward"
        target = self.sysroot/rel
        outside = self.root/"outside"
        outside.write_text("5\n")
        target.unlink()
        target.symlink_to(outside)
        r = self.run_guard("run", [sys.executable, "-c", "pass"])
        self.assertEqual(r.returncode, 75)
        self.assertEqual(outside.read_text(), "5\n")
        self.assertFalse((self.state/guard.JOURNAL).exists())


    def test_saved_journal_recovers_even_if_no_child_was_spawned(self):
        # Durable record exists while launch is interrupted before exec.
        guard.safe_dir(self.state)
        guard.persist(self.state, str(uuid.UUID(self.boot.read_text())),
                      guard.snapshot(self.sysroot))
        for rel, value in guard.PARAMETERS.items():
            (self.sysroot/rel).write_text(value+"\n")
        r = self.run_guard()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("RESTORED_AND_VERIFIED", r.stdout)
        self.assertEqual(self.values(), self.previous)

    def test_symlink_journal_is_rejected_and_outside_not_touched(self):
        self.state.mkdir(mode=0o700)
        outside = self.root/"outside-journal"
        outside.write_text('{"marker":"untrusted"}')
        (self.state/guard.JOURNAL).symlink_to(outside)
        r = self.run_guard()
        self.assertEqual(r.returncode, 75)
        self.assertIn("journal symlink", r.stderr)
        self.assertEqual(outside.read_text(), '{"marker":"untrusted"}')

    def test_world_readable_journal_is_rejected(self):
        self.state.mkdir(mode=0o700)
        journal = self.state/guard.JOURNAL
        journal.write_text("{}")
        journal.chmod(0o644)
        r = self.run_guard()
        self.assertEqual(r.returncode, 75)
        self.assertIn("untrusted journal metadata", r.stderr)

    def test_symlink_lock_file_is_rejected(self):
        self.state.mkdir(mode=0o700)
        outside = self.root/"outside-lock"
        outside.write_text("do-not-touch")
        (self.state/guard.LOCK).symlink_to(outside)
        r = self.run_guard()
        self.assertEqual(r.returncode, 75)
        self.assertEqual(outside.read_text(), "do-not-touch")

    def test_world_readable_state_directory_fails_closed(self):
        self.state.mkdir(mode=0o755)
        r = self.run_guard()
        self.assertEqual(r.returncode, 75)
        self.assertIn("must be private", r.stderr)

    def test_missing_sysctl_prevents_child_start(self):
        missing = self.sysroot/"net/ipv4/ip_forward"
        missing.unlink()
        child, ready = self.child_config()
        r = self.run_guard("run", child)
        self.assertEqual(r.returncode, 75)
        self.assertFalse(ready.exists())
        self.assertFalse((self.state/guard.JOURNAL).exists())

    def test_parent_sigterm_propagates_child_stop_then_restores(self):
        parent, childpid = self.spawn_guard()
        os.kill(parent.pid, signal.SIGTERM)
        parent.wait(timeout=8)
        self.assertEqual(self.values(), self.previous)
        self.assertFalse((self.state/guard.JOURNAL).exists())
        # The synthetic child has exited when the parent finished recovery.
        with self.assertRaises(ProcessLookupError):
            os.kill(childpid, 0)



if __name__ == "__main__":
    unittest.main(verbosity=2)
