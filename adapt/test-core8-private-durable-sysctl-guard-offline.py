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
from unittest import mock
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

    def test_optional_missing_kernel_parameter_records_only_existing(self):
        optional = self.sysroot/"net/core/default_qdisc"
        optional.unlink()
        original = guard.snapshot(self.sysroot, allow_optional_missing=True)
        self.assertNotIn("net/core/default_qdisc", original)
        self.assertTrue(guard.REQUIRED_PARAMETERS.issubset(original))
        guard.safe_dir(self.state)
        guard.persist(self.state, str(uuid.UUID(self.boot.read_text())), original)
        # Core8 would change only existing kernel parameters.
        for rel, configured in guard.PARAMETERS.items():
            if rel in original:
                (self.sysroot/rel).write_text(configured+"\n")
        r = self.run_guard()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("RESTORED_AND_VERIFIED", r.stdout)
        for rel, before in original.items():
            self.assertEqual((self.sysroot/rel).read_text().strip(), before)
        self.assertFalse(optional.exists())
        self.assertFalse((self.state/guard.JOURNAL).exists())

    def test_required_kernel_parameter_never_optional(self):
        (self.sysroot/"net/ipv6/conf/all/forwarding").unlink()
        with self.assertRaises(FileNotFoundError):
            guard.snapshot(self.sysroot, allow_optional_missing=True)

    def test_partial_journal_missing_required_forwarding_fails_closed(self):
        self.state.mkdir(mode=0o700)
        snapshot = guard.snapshot(self.sysroot)
        snapshot.pop("net/ipv6/conf/all/forwarding")
        guard.persist(self.state, str(uuid.UUID(self.boot.read_text())), snapshot)
        r = self.run_guard()
        self.assertEqual(r.returncode, 75)
        self.assertIn("journal has wrong sysctl inventory", r.stderr)
        self.assertTrue((self.state/guard.JOURNAL).exists())

    def test_missing_sysctl_prevents_child_start(self):
        missing = self.sysroot/"net/ipv4/ip_forward"
        missing.unlink()
        child, ready = self.child_config()
        r = self.run_guard("run", child)
        self.assertEqual(r.returncode, 75)
        self.assertFalse(ready.exists())
        self.assertFalse((self.state/guard.JOURNAL).exists())

    def test_child_descendant_still_holds_lock_no_premature_restore(self):
        # The immediate child exits but a benign subchild keeps the inherited
        # lock FD open. The guard must retain the journal until *all* lock
        # holders have exited; no real sysctls or host services are used.
        cfg = self.root/"descendant-config.json"
        marker = self.root/"descendant-pid"
        cfg.write_text(json.dumps({
            "root": str(self.sysroot), "parameters": guard.PARAMETERS,
            "marker": str(marker)
        }))
        code = r"""
import json, os, pathlib, subprocess, sys
cfg=json.loads(pathlib.Path(sys.argv[1]).read_text())
for key, value in cfg["parameters"].items():
    (pathlib.Path(cfg["root"])/key).write_text(value+"\n")
fds=[]
for part in pathlib.Path("/proc/self/fd").iterdir():
    try:
        if os.readlink(part).endswith("/kernel-owner.lock"):
            fds.append(int(part.name))
    except (FileNotFoundError, OSError):
        continue
if len(fds)!=1:
    raise SystemExit("expected one inherited lock fd")
grand=subprocess.Popen(
    [sys.executable, "-c", "import time;time.sleep(45)"],
    pass_fds=tuple(fds), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
pathlib.Path(cfg["marker"]).write_text(str(grand.pid))
"""
        child = [sys.executable, "-c", code, str(cfg)]
        grandpid = None
        try:
            result = self.run_guard("run", child)
            self.assertTrue(marker.is_file(), result.stdout + result.stderr)
            grandpid = int(marker.read_text())
            self.assertNotEqual(result.returncode, 0, "premature success with live inherited lock")
            self.assertTrue((self.state/guard.JOURNAL).exists(), "journal prematurely deleted")
            self.assertEqual(self.values(), guard.PARAMETERS,
                             "unowned recovery while descendant remains active")
            blocked = self.run_guard()
            self.assertEqual(blocked.returncode, 75)
            self.assertIn("CORE8_OWNER_BUSY", blocked.stderr)
        finally:
            if grandpid is not None:
                try:
                    os.kill(grandpid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        for i in range(50):
            r = self.run_guard()
            if r.returncode == 0:
                break
            self.assertIn("CORE8_OWNER_BUSY", r.stderr)
            time.sleep(0.04)
        else:
            self.fail("recovery remained blocked after synthetic descendant exit")
        self.assertEqual(self.values(), self.previous)

    def test_healthy_long_lived_core8_is_never_timed_out(self):
        # Fake 181 sequential one-second wait polls in zero wall-clock
        # time, representing more than the historical 140-second cap.
        # A normal service must be allowed to live for weeks/months.
        class LongLivingChild:
            pid = 24680
            count = 0
            def poll(self):
                return None
            def wait(self, timeout):
                self.count += 1
                if self.count <= 180:
                    raise subprocess.TimeoutExpired("simulated-healthy-core8", timeout)
                return 0
        child = LongLivingChild()
        fake_lock = os.open(os.devnull, os.O_RDONLY)
        try:
            with mock.patch.object(guard.subprocess,"Popen",return_value=child) as spawn, \
                 mock.patch.object(guard.os,"killpg") as forced_kill:
                result = guard.run_child([sys.executable,"-c","pass"],fake_lock,
                                         poll_seconds=0.001)
            self.assertEqual(result,0)
            self.assertEqual(child.count,181)
            forced_kill.assert_not_called()
            self.assertEqual(spawn.call_args.kwargs["start_new_session"],True)
            self.assertEqual(spawn.call_args.kwargs["pass_fds"],(fake_lock,))
        finally:
            os.close(fake_lock)

    def test_signal_stop_escalates_only_unresponsive_child(self):
        # No sysctl files or host namespaces involved; just isolated
        # short-lived Python processes. Force SIGTERM ignoring child to
        # exercise the finite shutdown budget, not server uptime.
        child_path = self.root/"ignoring-child.py"
        ready = self.root/"ignoring-child-ready"
        child_path.write_text(
            "import os,signal,time,sys\n"
            "from pathlib import Path\n"
            "signal.signal(signal.SIGTERM,signal.SIG_IGN)\n"
            "Path(sys.argv[1]).write_text(str(os.getpid()))\n"
            "while True: time.sleep(.2)\n"
        )
        runner_path=self.root/"guard-signal-runner.py"
        runner_path.write_text(
            "import importlib.util,os,sys\n"
            "from pathlib import Path\n"
            "spec=importlib.util.spec_from_file_location('guard',sys.argv[1])\n"
            "g=importlib.util.module_from_spec(spec)\n"
            "spec.loader.exec_module(g)\n"
            "fd=os.open(os.devnull,os.O_RDONLY)\n"
            "try: sys.exit(g.run_child([sys.executable,sys.argv[2],sys.argv[3]],fd,"
            "stop_grace_seconds=.35,poll_seconds=.05))\n"
            "finally:os.close(fd)\n"
        )
        runner=subprocess.Popen(
            [sys.executable,str(runner_path),str(GUARD),str(child_path),str(ready)],
            stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,
            start_new_session=True)
        self.parents.append(runner)
        child_pid=None
        try:
            for _ in range(100):
                if ready.exists():
                    child_pid=int(ready.read_text())
                    break
                if runner.poll() is not None:
                    self.fail("supervised child failed to initialize")
                time.sleep(.02)
            self.assertIsNotNone(child_pid)
            os.kill(runner.pid,signal.SIGTERM)
            out,err=runner.communicate(timeout=8)
            self.assertEqual(runner.returncode,124,(out,err))
            # The child should be reaped: no surviving orphan
            # may hold the inherited journal owner lock.
            with self.assertRaises(ProcessLookupError):
                os.kill(child_pid,0)
        finally:
            if child_pid:
                try:
                    os.kill(child_pid,signal.SIGKILL)
                except ProcessLookupError:
                    pass

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
