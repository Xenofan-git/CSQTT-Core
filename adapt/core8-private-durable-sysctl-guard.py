#!/usr/bin/env python3
"""Experimental durable Core8 sysctl supervisor (NO installation).

Only use on an isolated ARM64 guest. On any other machine this program
requires a disposable /tmp sysctl fixture; it does not write host /proc.
- A nonblocking flock protects all cooperative Core8 instances.
- A fsynced boot-scoped journal is written BEFORE launching the child.
- The child inherits the advisory lock fd; it cannot outlive the wrapper
  and simultaneously allow a second cooperative instance to recover.
- On ordinary stop or next same-boot startup, restore only still-owned values.
- After reboot do NOT replay old kernel values (new kernel defaults may differ).

No routes, sockets or firewalls are modified by this supervisor.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import tempfile
import uuid

PARAMETERS = {
    "net/ipv4/ip_forward": "1",
    "net/ipv6/conf/all/forwarding": "1",
    "net/ipv4/conf/all/rp_filter": "2",
    "net/ipv4/conf/default/rp_filter": "2",
    "net/core/default_qdisc": "fq",
    "net/ipv4/tcp_congestion_control": "bbr",
    "net/ipv4/tcp_sack": "1",
    "net/ipv4/tcp_dsack": "1",
    "net/ipv4/tcp_reordering": "15",
    "net/ipv4/tcp_window_scaling": "1",
    "net/ipv4/tcp_fastopen": "3",
    "net/ipv4/tcp_slow_start_after_idle": "0",
}
JOURNAL = "pending-sysctls-v1.json"
LOCK = "kernel-owner.lock"
VALUE = re.compile(r"[A-Za-z0-9_.-]{1,80}\Z")


class GuardFailure(Exception):
    pass


def safe_dir(path: Path) -> Path:
    if not path.is_absolute():
        raise GuardFailure("state and proc paths must be absolute")
    if path.is_symlink():
        raise GuardFailure("state path is a symlink")
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    mode = path.lstat().st_mode
    if not stat.S_ISDIR(mode) or mode & 0o077:
        raise GuardFailure("state directory must be private (mode 0700)")
    return path


def private_guest_gate(root: Path, flag: bool) -> None:
    if root.resolve() == Path("/proc/sys"):
        if not flag:
            raise GuardFailure("real /proc requires explicit --private-guest")
        if os.geteuid() != 0 or os.uname().machine != "aarch64":
            raise GuardFailure("real /proc requires root inside ARM64 guest")
        if os.uname().nodename != "csqtt-core8-isolated":
            raise GuardFailure("wrong isolated guest hostname")
        route = subprocess.run(["ip", "-4", "route", "show", "default"],
                               capture_output=True, text=True, timeout=3, check=True)
        if route.stdout.strip():
            raise GuardFailure("guest has an IPv4 default route")
    else:
        # Test access must be confined to a disposable fixture; never reach
        # an arbitrary alternative procfs mount.
        tmp = Path(tempfile.gettempdir()).resolve()
        if root.resolve() == tmp or tmp not in root.resolve().parents:
            raise GuardFailure("non-guest mode requires a directory under temporary root")
        if str(root.resolve()).startswith("/proc/"):
            raise GuardFailure("alternate procfs path refused")


def read_boot_id(path: Path) -> str:
    if path.is_symlink() or not path.is_file():
        raise GuardFailure("boot-id file missing or symlink")
    try:
        return str(uuid.UUID(path.read_text(encoding="ascii").strip()))
    except (ValueError, UnicodeError, OSError) as exc:
        raise GuardFailure("invalid boot-id") from exc


def safe_target(root: Path, rel: str) -> Path:
    if rel not in PARAMETERS:
        raise GuardFailure("non-allowlisted kernel parameter")
    cur = root
    for part in Path(rel).parts[:-1]:
        cur = cur / part
        mode = cur.lstat().st_mode
        if not stat.S_ISDIR(mode):
            raise GuardFailure("unsafe sysctl parent directory")
    p = root / rel
    metadata = p.lstat()
    if not stat.S_ISREG(metadata.st_mode):
        raise GuardFailure("sysctl not a regular file or is a symlink")
    return p


def read_parameter(root: Path, rel: str) -> str:
    p = safe_target(root, rel)
    fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise GuardFailure("changed sysctl file type")
        value = os.read(fd, 256).decode("ascii").strip()
        if not VALUE.fullmatch(value):
            raise GuardFailure("unexpected kernel parameter value")
        return value
    finally:
        os.close(fd)


def write_parameter(root: Path, rel: str, value: str) -> None:
    if not VALUE.fullmatch(value):
        raise GuardFailure("invalid restoration value")
    p = safe_target(root, rel)
    fd = os.open(p, os.O_WRONLY | os.O_TRUNC | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise GuardFailure("changed sysctl type during restore")
        os.write(fd, (value + "\n").encode("ascii"))
    finally:
        os.close(fd)
    if read_parameter(root, rel) != value:
        raise GuardFailure("restored value did not persist")


def locked_fd(state: Path) -> int:
    p = state / LOCK
    fd = os.open(p, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_mode & 0o077:
            raise GuardFailure("lock file must be private regular file")
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise GuardFailure("CORE8_OWNER_BUSY") from exc
        return fd
    except BaseException:
        os.close(fd)
        raise


def journal_path(state: Path) -> Path:
    return state / JOURNAL


def read_journal(state: Path):
    p = journal_path(state)
    if not p.exists() and not p.is_symlink():
        return None
    if p.is_symlink():
        raise GuardFailure("journal symlink detected")
    fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_mode & 0o077 or st.st_size > 16384:
            raise GuardFailure("untrusted journal metadata")
        raw = os.read(fd, 16385)
        if len(raw) > 16384:
            raise GuardFailure("journal too large")
        obj = json.loads(raw)
    except (ValueError, UnicodeError, TypeError) as exc:
        raise GuardFailure("journal content invalid") from exc
    finally:
        os.close(fd)
    if not isinstance(obj, dict) or set(obj) != {"v", "boot_id", "values"} or obj["v"] != 1:
        raise GuardFailure("unknown journal format")
    try:
        if str(uuid.UUID(obj["boot_id"])) != obj["boot_id"]:
            raise ValueError("noncanonical boot-id")
    except (ValueError, AttributeError, TypeError) as exc:
        raise GuardFailure("journal boot-id invalid") from exc
    vals = obj["values"]
    if not isinstance(vals, dict) or set(vals) != set(PARAMETERS):
        raise GuardFailure("journal has wrong sysctl inventory")
    for rel, original in vals.items():
        if not isinstance(original, str) or not VALUE.fullmatch(original):
            raise GuardFailure("journal has invalid value")
    return obj


def persist(state: Path, boot: str, values: dict[str, str]) -> None:
    p = journal_path(state)
    if p.exists() or p.is_symlink():
        raise GuardFailure("unresolved previous journal")
    payload = json.dumps({"v": 1, "boot_id": boot, "values": values},
                         sort_keys=True).encode("ascii")
    fd, scratch = tempfile.mkstemp(prefix=".core8-txn-", dir=state)
    try:
        os.fchmod(fd, 0o600)
        os.write(fd, payload)
        os.fsync(fd)
        os.close(fd)
        fd = -1
        os.replace(scratch, p)
        sync_state_dir(state)
    finally:
        if fd >= 0:
            os.close(fd)
        if os.path.exists(scratch):
            os.unlink(scratch)


def sync_state_dir(state: Path) -> None:
    fd = os.open(state, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def clear_journal(state: Path) -> None:
    journal_path(state).unlink()
    sync_state_dir(state)


def recover(state: Path, root: Path, boot: str) -> str:
    j = read_journal(state)
    if j is None:
        return "NO_PENDING_JOURNAL"
    if j["boot_id"] != boot:
        # After reboot the kernel independently initializes sysctls; old
        # settings are historical, not safe values to apply to a new boot.
        archived = state / ("stale-boot-" + j["boot_id"] + ".json")
        if archived.exists() or archived.is_symlink():
            raise GuardFailure("stale boot audit file already exists")
        os.replace(journal_path(state), archived)
        sync_state_dir(state)
        return "STALE_BOOT_ARCHIVED_WITHOUT_RESTORING"
    bad = []
    for rel, original in reversed(tuple(j["values"].items())):
        try:
            current = read_parameter(root, rel)
            if current == original:
                continue
            if current != PARAMETERS[rel]:
                bad.append(rel)
                continue
            write_parameter(root, rel, original)
        except (OSError, GuardFailure) as exc:
            bad.append(rel)
    if bad:
        # Fail closed, keep durable ownership journal. No silent success.
        raise GuardFailure("RECOVERY_UNRESOLVED:" + ",".join(sorted(bad)))
    clear_journal(state)
    return "RESTORED_AND_VERIFIED"


def snapshot(root: Path) -> dict[str, str]:
    return {rel: read_parameter(root, rel) for rel in PARAMETERS}


def run_child(cmd: list[str], lock: int) -> int:
    if not cmd:
        raise GuardFailure("missing child command")
    if not Path(cmd[0]).is_absolute():
        raise GuardFailure("child executable must be an absolute path")
    # Lock descriptor is intentionally inherited: if guard parent is killed,
    # the Core8 child still holds the lock; no new instance may restore while
    # it could still modify global sysctls.
    child = subprocess.Popen(cmd, pass_fds=(lock,), start_new_session=True)
    previous = {}
    def forward(sig, frame):
        if child.poll() is None:
            try:
                os.killpg(child.pid, sig)
            except ProcessLookupError:
                pass
    for sig in (signal.SIGINT, signal.SIGTERM):
        previous[sig] = signal.signal(sig, forward)
    try:
        try:
            return child.wait(timeout=140)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGTERM)
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait(timeout=5)
            return 124
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("run", "recover"))
    parser.add_argument("--state-dir", type=Path, required=True)
    parser.add_argument("--sysctl-root", type=Path, required=True)
    parser.add_argument("--boot-id-file", type=Path, required=True)
    parser.add_argument("--private-guest", action="store_true")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    a = parser.parse_args()
    cmd = a.command[1:] if a.command and a.command[0] == "--" else a.command
    private_guest_gate(a.sysctl_root, a.private_guest)
    boot = read_boot_id(a.boot_id_file)
    state = safe_dir(a.state_dir)
    fd = locked_fd(state)
    try:
        status = recover(state, a.sysctl_root, boot)
        print("CORE8_RECOVERY=" + status, flush=True)
        if a.action == "recover":
            return 0
        prior = snapshot(a.sysctl_root)
        persist(state, boot, prior)
        code = 1
        try:
            code = run_child(cmd, fd)
        finally:
            # flock() is attached to an open-file description, not a PID.
            # The child can pass the lock FD to one of its descendants.
            # Closing this parent's copy keeps the lock HELD for as long
            # as any descendant copy survives. A new independent open
            # description MUST obtain the lock before sysctl recovery.
            # This avoids restoring while a still-running descendant
            # can continue to change global network sysctls.
            os.close(fd)
            fd = -1
            fd = locked_fd(state)
            result = recover(state, a.sysctl_root, boot)
            print("CORE8_STOP_RECOVERY=" + result, flush=True)
        return code
    finally:
        if fd >= 0:
            os.close(fd)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (GuardFailure, OSError, subprocess.SubprocessError) as exc:
        print("CORE8_GUARD_FAILED:" + str(exc), file=sys.stderr)
        sys.exit(75)
