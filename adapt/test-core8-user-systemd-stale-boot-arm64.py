#!/usr/bin/env python3
"""Real nonroot ARM64 user-systemd stale-boot recovery using fake /tmp sysctls.

No sudo, no production Core8, no /proc/sys access, no network effects.
Run only on disposable GitHub-hosted ARM64 where a user manager exists.
"""
import importlib.util
import json
import os
from pathlib import Path
import secrets
import signal
import sys
import tempfile
import time
import uuid

HERE=Path(__file__).resolve().parent
LAB=HERE/"test-core8-user-systemd-real-restart-fakeproc.py"
GUARD=HERE/"core8-private-durable-sysctl-guard.py"

def load(filename,name):
    if not filename.is_file() or filename.is_symlink():
        raise RuntimeError("required repository fixture unavailable")
    spec=importlib.util.spec_from_file_location(name,filename)
    if spec is None or spec.loader is None:
        raise RuntimeError("failed to import local verified fixture")
    module=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

lab=load(LAB,"csqtt_user_systemd_fakeproc")
guard=load(GUARD,"csqtt_private_durable_guard")

def require(cond,message):
    if not cond:
        lab.reject(message)

def run_unit(unitfile,unit):
    lab.cmd("systemctl","--user","daemon-reload")
    lab.cmd("systemctl","--user","start",unit)

def main():
    units=lab.preflight()
    require(os.geteuid()!=0,"this fake reboot must be nonroot")
    temp=Path(tempfile.mkdtemp(prefix="csqtt-staleboot-user-systemd-",dir="/tmp"))
    temp.chmod(0o700)
    root=temp/"fake-proc-sys"
    root.mkdir(mode=0o700)
    state=temp/"durable-journal-state"
    state.mkdir(mode=0o700)
    config=temp/"child-config.json"
    child=temp/"synthetic-core8.py"
    child.write_text(lab.CHILD_SOURCE)
    launches=temp/"launches.txt"
    boot_path=temp/"boot-id"
    old_boot=str(uuid.uuid4())
    new_boot=str(uuid.uuid4())
    require(old_boot!=new_boot,"expected two different boot IDs")
    boot_path.write_text(new_boot+"\n")
    old_sysctl={rel:"9" for rel in guard.PARAMETERS}
    new_sysctl={rel:"7" for rel in guard.PARAMETERS}
    for rel in guard.PARAMETERS:
        path=root/rel
        path.parent.mkdir(parents=True,exist_ok=True)
        # "7" is a distinct synthetic value on every fake regular file.
        path.write_text(new_sysctl[rel]+"\n")
    # Simulated previous boot had been interrupted with its journal
    # durably persisted, but kernel values are now reset to NEW values.
    guard.persist(state,old_boot,old_sysctl)
    source=state/guard.JOURNAL
    old_bytes=source.read_bytes()
    require(json.loads(old_bytes)["boot_id"]==old_boot,"old journal boot mismatch")
    archive=state/("stale-boot-"+old_boot+".json")
    config.write_text(json.dumps({"root":str(root),
                                   "targets":guard.PARAMETERS,
                                   "launches":str(launches)}))
    logfile=temp/"user-systemd-stale-boot.log"
    unit="csqtt-core253-staleboot-"+secrets.token_hex(4)+".service"
    file=units/unit
    require(not file.exists() and not file.is_symlink(),"unit name collision")
    python=sys.executable
    start=[python,"-B",str(GUARD),"--state-dir",str(state),
           "--sysctl-root",str(root),"--boot-id-file",str(boot_path),
           "run","--",python,"-B",str(child),str(config)]
    post=[python,"-B",str(GUARD),"--state-dir",str(state),
          "--sysctl-root",str(root),"--boot-id-file",str(boot_path),
          "recover"]
    for arg in start+post:
        require(not any(ch.isspace() for ch in arg) and "%" not in arg,
                "unexpected systemd unit argument formatting")
    text=(
        "[Unit]\nDescription=CSQTT Core253 synthetic reboot-only journal recovery\n"
        "StartLimitIntervalSec=20\nStartLimitBurst=3\n"
        "[Service]\nType=simple\n"
        "ExecStart="+" ".join(start)+"\n"
        "ExecStopPost="+" ".join(post)+"\n"
        "StandardOutput=append:"+str(logfile)+"\n"
        "StandardError=append:"+str(logfile)+"\n"
        "Restart=on-failure\nRestartSec=1\n"
        "RestartPreventExitStatus=75\nKillMode=mixed\n"
        "TimeoutStopSec=15\nUMask=0077\nNoNewPrivileges=true\n"
    )
    file.write_text(text)
    file.chmod(0o600)
    try:
        run_unit(file,unit)
        first=lab.wait_until(lambda:lab.read_pids(launches)[:1],
                             timeout=14,what="new-boot synthetic child")
        pid=first[0]
        lab.wait_until(lambda:archive.is_file() and source.is_file(),
                       timeout=10,what="stale old journal archived and new one persisted")
        newjournal=guard.read_journal(state)
        require(newjournal is not None,"new journal missing")
        require(newjournal["boot_id"]==new_boot,
                "new journal unexpectedly retains old boot")
        require(newjournal["values"]==new_sysctl,
                "new journal incorrectly saved previous boot baseline")
        require(archive.read_bytes()==old_bytes,
                "stale journal archive was not preserved unchanged")
        require(archive.stat().st_mode & 0o077==0,
                "archived private journal exposed to other users")
        require(lab.values(root,new_sysctl)==guard.PARAMETERS,
                "new-boot synthetic child did not set target values")
        require(lab.alive(pid),"new-boot child not running")
        logs=logfile.read_text()
        require("CORE8_RECOVERY=STALE_BOOT_ARCHIVED_WITHOUT_RESTORING" in logs,
                "real user systemd did not execute stale-boot recovery")
        print("CSQTT_USER_SYSTEMD_REAL_STALE_BOOT_ARCHIVED_PASS",flush=True)
        print("CSQTT_USER_SYSTEMD_NEW_BOOT_JOURNAL_BASELINE_PASS",flush=True)

        lab.cmd("systemctl","--user","stop",unit)
        lab.wait_until(lambda: lab.values(root,new_sysctl)==new_sysctl,
                       timeout=8,what="new-boot baseline restore")
        lab.wait_until(lambda: not lab.alive(pid),timeout=8,
                       what="new-boot process stopped")
        require(not source.exists(),
                "same-boot journal still exists after clean stop")
        require(archive.read_bytes()==old_bytes,
                "historical old-boot evidence changed after stop")
        print("CSQTT_USER_SYSTEMD_REAL_NEW_BOOT_STOP_RESTORED_PASS",flush=True)

        # Re-introduce an old journal while its unique archive exists.
        # The guard must reject the archived-path collision before
        # launching another child or overwriting preserved evidence.
        source.write_bytes(old_bytes)
        source.chmod(0o600)
        marker="CSQTT_FAKE_STALE_ARCHIVE_COLLISION_"+secrets.token_hex(4)
        with logfile.open("a") as stream:
            stream.write("\n"+marker+"\n")
        lab.cmd("systemctl","--user","reset-failed",unit,check=False)
        lab.cmd("systemctl","--user","start",unit,check=False)
        lab.wait_until(
            lambda: "CORE8_GUARD_FAILED:stale boot audit file already exists" in
                    logfile.read_text().split(marker,1)[-1],
            timeout=8,what="stale archive name collision refused")
        time.sleep(2.6)
        require(len(lab.read_pids(launches))==1,
                "new child started despite historical journal collision")
        require(source.read_bytes()==old_bytes and archive.read_bytes()==old_bytes,
                "refused journal/archive was modified")
        require(lab.values(root,new_sysctl)==new_sysctl,
                "old-boot failed recovery changed new-boot fake kernel state")
        require(not lab.alive(pid),"old synthetic child orphaned")
        records=logfile.read_text().split(marker,1)[-1]
        require(records.count("CORE8_GUARD_FAILED:")<=2,
                "archive collision created a repeating restart loop")
        print("CSQTT_USER_SYSTEMD_STALE_ARCHIVE_COLLISION_FAILCLOSED_PASS",
              flush=True)

        # Valid SAME-boot journal with one later-detected ownership conflict:
        # a service upgrade/restart MUST NOT partially restore unrelated
        # fake sysctl values before refusing the startup.
        source.unlink()
        guard.persist(state,new_boot,new_sysctl)
        conflict=sorted(guard.PARAMETERS)[0]
        for rel,target in guard.PARAMETERS.items():
            (root/rel).write_text(target+"\n")
        (root/conflict).write_text("9\n")
        before_conflict=lab.values(root,guard.PARAMETERS)
        require(before_conflict[conflict]=="9","invalid conflict fixture")
        marker2="CSQTT_VALID_JOURNAL_CONFLICT_"+secrets.token_hex(4)
        with logfile.open("a") as stream:
            stream.write("\n"+marker2+"\n")
        lab.cmd("systemctl","--user","reset-failed",unit,check=False)
        lab.cmd("systemctl","--user","start",unit,check=False)
        lab.wait_until(
            lambda: "CORE8_GUARD_FAILED:RECOVERY_UNRESOLVED:" in
                    logfile.read_text().split(marker2,1)[-1],
            timeout=9,what="valid journal external sysctl ownership conflict")
        time.sleep(2.4)
        require(lab.values(root,guard.PARAMETERS)==before_conflict,
                "sysctls partially overwritten despite an existing conflict")
        require(len(lab.read_pids(launches))==1,
                "systemd launched a new child with an unresolved conflict")
        require(source.exists() and guard.read_journal(state)["boot_id"]==new_boot,
                "unresolved valid journal not retained")
        require(archive.read_bytes()==old_bytes,"previous boot archive was modified")
        require(logfile.read_text().split(marker2,1)[-1].count("CORE8_GUARD_FAILED:")<=2,
                "a valid-but-conflicted journal caused a restart loop")
        print("CSQTT_USER_SYSTEMD_VALID_CONFLICT_ZERO_PARTIAL_RESTORE_PASS",
              flush=True)

        # An authorized operator fixes ONLY the externally conflicting
        # test value; the very next systemd start must recover the prior
        # journal, snapshot fresh current baseline and run successfully.
        (root/conflict).write_text(guard.PARAMETERS[conflict]+"\n")
        lab.cmd("systemctl","--user","reset-failed",unit,check=False)
        lab.cmd("systemctl","--user","start",unit)
        second=lab.wait_until(
            lambda: lab.read_pids(launches)[1:2],
            timeout=12,what="synthetic child after resolving sysctl conflict")
        second_pid=second[0]
        require(second_pid!=pid and lab.alive(second_pid),
                "new synthetic Core8 not started after conflict repair")
        lab.wait_until(lambda: lab.values(root,guard.PARAMETERS)==guard.PARAMETERS,
                       timeout=8,what="new child applies fake target parameters")
        new_after_repair=guard.read_journal(state)
        require(new_after_repair is not None
                and new_after_repair["boot_id"]==new_boot
                and new_after_repair["values"]==new_sysctl,
                "recovered journal failed to capture new boot baseline")
        lab.cmd("systemctl","--user","stop",unit)
        lab.wait_until(lambda: lab.values(root,new_sysctl)==new_sysctl,
                       timeout=8,what="restored values after repaired startup")
        lab.wait_until(lambda: not lab.alive(second_pid),
                       timeout=8,what="repaired run child reaped")
        require(not source.exists() and archive.read_bytes()==old_bytes,
                "repaired startup did not clear journal or damaged old audit")
        print("CSQTT_USER_SYSTEMD_CONFLICT_RESOLVED_RESTART_RESTORES_PASS",
              flush=True)
    finally:
        lab.cmd("systemctl","--user","stop",unit,check=False)
        lab.cmd("systemctl","--user","reset-failed",unit,check=False)
        file.unlink(missing_ok=True)
        lab.cmd("systemctl","--user","daemon-reload",check=False)
    print("CSQTT_USER_SYSTEMD_SYNTHETIC_BOOT_TRANSITION_ALL_PASS",flush=True)

if __name__=="__main__":
    main()
