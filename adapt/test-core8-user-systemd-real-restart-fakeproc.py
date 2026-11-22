#!/usr/bin/env python3
"""Real, unprivileged user-systemd restart test with ONLY fake sysctl files.

A GitHub-hosted disposable aarch64 runner is mandatory. This code
NEVER writes /proc/sys, never runs real Core8, never starts system units,
and never touches production, routing, VPN, network or firewall settings.
"""
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parent
GUARD = ROOT / "core8-private-durable-sysctl-guard.py"
CHILD_SOURCE = r'''
import json
import os
from pathlib import Path
import signal
import sys
import time
args=json.loads(Path(sys.argv[1]).read_text())
for rel,val in args["targets"].items():
    (Path(args["root"])/rel).write_text(val+"\n")
with Path(args["launches"]).open("a") as file:
    file.write(str(os.getpid())+"\n")
    file.flush()
    os.fsync(file.fileno())
def term(sig,frame):
    sys.exit(0)
signal.signal(signal.SIGTERM,term)
while True:
    time.sleep(.15)
'''

def reject(msg):
    raise RuntimeError("CSQTT_CORE253_USER_SYSTEMD_PRIVATE_TEST_REJECTED: "+msg)

def cmd(*argv,check=True,timeout=12):
    p=subprocess.run(argv,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,
                     timeout=timeout)
    if check and p.returncode:
        raise RuntimeError("COMMAND_FAILED: "+str(argv[:4])+" "+p.stderr[-900:])
    return p.stdout.strip()

def preflight():
    if (os.environ.get("GITHUB_ACTIONS")!="true"
            or os.environ.get("RUNNER_ENVIRONMENT")!="github-hosted"
            or os.environ.get("CSQTT_PRIVATE_USER_SYSTEMD_TEST")!="yes"
            or os.uname().machine!="aarch64"
            or os.geteuid()==0):
        reject("requires unprivileged GitHub-hosted AArch64 disposable runner")
    if not GUARD.is_file() or GUARD.is_symlink():
        reject("guard source is missing or symlink")
    runtime=os.environ.get("XDG_RUNTIME_DIR","")
    if not runtime:
        p=Path("/run/user")/str(os.getuid())
        if p.is_dir() and p.stat().st_uid==os.getuid():
            runtime=str(p)
            os.environ["XDG_RUNTIME_DIR"]=runtime
    if not runtime:
        reject("user manager runtime directory unavailable")
    runtime=Path(runtime).resolve(strict=True)
    if runtime!=Path("/run/user")/str(os.getuid()) or runtime.stat().st_uid!=os.getuid():
        reject("unexpected user manager runtime location or owner")
    if cmd("systemctl","--user","is-system-running",check=False) not in ("running","degraded"):
        reject("real systemd --user manager unavailable; no privilege escalation attempted")
    parent=runtime/"systemd"
    directory=parent/"user"
    for location in (parent,directory):
        if location.exists() and (location.is_symlink() or not location.is_dir()
                                  or location.stat().st_uid!=os.getuid()):
            reject("unsafe user-systemd runtime directory")
        location.mkdir(mode=0o700,exist_ok=True)
        if location.stat().st_uid!=os.getuid():
            reject("unowned user-systemd runtime directory")
    return directory

def values(root,targets):
    return {rel:(root/rel).read_text().strip() for rel in targets}

def wait_until(predicate,timeout=20,what="condition"):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        item=predicate()
        if item:
            return item
        time.sleep(.16)
    raise RuntimeError("TIMED_OUT: "+what)

def read_pids(path):
    if not path.exists():
        return []
    result=[]
    for line in path.read_text().splitlines():
        try: result.append(int(line))
        except ValueError:reject("bad test process marker")
    return result

def alive(pid):
    try:
        os.kill(pid,0)
        return True
    except ProcessLookupError:
        return False

def userprop(unit,property):
    return cmd("systemctl","--user","show",unit,"-p",property,"--value",check=False)

def main():
    units=preflight()
    sys.path.insert(0,str(ROOT))
    # Import the exact guard definition without a host kernel read/write.
    import importlib.util
    spec=importlib.util.spec_from_file_location("guard",GUARD)
    g=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(g)
    dirname=tempfile.mkdtemp(prefix="csqtt-user-systemd-fake-sysctl-",dir="/tmp")
    scratch=Path(dirname)
    scratch.chmod(0o700)
    root=scratch/"fake-proc-sys"
    root.mkdir(mode=0o700)
    baseline={}
    for relative,expected in g.PARAMETERS.items():
        original={"1":"0","2":"1","0":"1","fq":"fq_codel",
                  "bbr":"cubic","15":"3","3":"1"}[expected]
        target=root/relative
        target.parent.mkdir(parents=True,exist_ok=True)
        target.write_text(original+"\n")
        baseline[relative]=original
    state=scratch/"state"
    boot=scratch/"boot"
    boot.write_text(str(uuid.uuid4()))
    config=scratch/"config.json"
    launches=scratch/"launches.txt"
    config.write_text(json.dumps({"root":str(root),"targets":g.PARAMETERS,
                                   "launches":str(launches)}))
    child=scratch/"fake-core8.py"
    child.write_text(CHILD_SOURCE)
    logfile=scratch/"systemd-user.log"
    unit="csqtt-core253-user-restart-"+secrets.token_hex(4)+".service"
    file=units/unit
    if file.exists() or file.is_symlink():
        reject("unit name collision")
    python=sys.executable
    start=[python,"-B",str(GUARD),"--state-dir",str(state),
           "--sysctl-root",str(root),"--boot-id-file",str(boot),"run",
           "--",python,"-B",str(child),str(config)]
    recover=[python,"-B",str(GUARD),"--state-dir",str(state),
             "--sysctl-root",str(root),"--boot-id-file",str(boot),"recover"]
    # All arguments are paths with no whitespace: no unsafe interpolation
    for part in start+recover:
        if "\n" in part or any(ch.isspace() for ch in part):
            reject("invalid parameter for private unit")
    text=(
       "[Unit]\nDescription=CSQTT Core253 unprivileged fake sysctl auto restart test\n"
       "StartLimitIntervalSec=25\nStartLimitBurst=3\n"
       "[Service]\nType=simple\n"
       "ExecStart="+" ".join(start)+"\n"
       "ExecStopPost="+" ".join(recover)+"\n"
       "StandardOutput=append:"+str(logfile)+"\n"
       "StandardError=append:"+str(logfile)+"\n"
       "Restart=on-failure\nRestartSec=1\n"
       "RestartPreventExitStatus=75\nKillMode=mixed\n"
       "TimeoutStopSec=15\nUMask=0077\nNoNewPrivileges=true\n"
    )
    file.write_text(text)
    file.chmod(0o600)
    try:
        cmd("systemctl","--user","daemon-reload")
        cmd("systemctl","--user","start",unit)
        first=wait_until(lambda:read_pids(launches)[:1],what="first synthetic Core8")
        first_pid=first[0]
        wait_until(lambda:values(root,baseline)==g.PARAMETERS,what="target fake sysctls")
        if not alive(first_pid):
            reject("synthetic Core8 exited before crash")
        print("CSQTT_USER_SYSTEMD_FAKE_FIRST_PROCESS_READY",flush=True)
        os.kill(first_pid,signal.SIGKILL)
        restarts=wait_until(lambda:read_pids(launches) if len(read_pids(launches))>=2 else None,
                            timeout=18,what="systemd automatic child restart")
        second_pid=restarts[1]
        if first_pid==second_pid or not alive(second_pid):
            reject("no new live test child after crash")
        if int(userprop(unit,"NRestarts"))<1:
            reject("systemd did not report automatic restart")
        if "CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED" not in logfile.read_text():
            reject("durable recovery did not finish during service restart")
        print("CSQTT_USER_SYSTEMD_REAL_AUTORESTART_AFTER_SIGKILL_PASS",flush=True)
        cmd("systemctl","--user","stop",unit)
        wait_until(lambda: values(root,baseline)==baseline,timeout=9,what="fake sysctl restoration")
        wait_until(lambda: not alive(second_pid),timeout=9,what="synthetic Core8 stopped")
        if (state/g.JOURNAL).exists():
            reject("durable journal remained after unit stopped")
        logs=logfile.read_text()
        if logs.count("CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED")<2:
            reject("missing clean stop recovery")
        print("CSQTT_USER_SYSTEMD_REAL_STOP_MIXED_AND_EXECSTOPPOST_PASS",flush=True)

        # Deliberately invalid journal: guardian must return 75
        # and user systemd must not silently restart it.
        journal=state/g.JOURNAL
        journal.write_text('{"not-a-valid-journal":true}')
        journal.chmod(0o600)
        with logfile.open("a") as markerfile:
            markerfile.write("\nCSQTT_CORE253_FAILCLOSED_PROBE\n")
        cmd("systemctl","--user","reset-failed",unit,check=False)
        cmd("systemctl","--user","start",unit,check=False)
        wait_until(lambda: "CORE8_GUARD_FAILED:" in logfile.read_text().split(
            "CSQTT_CORE253_FAILCLOSED_PROBE",1)[-1],timeout=8,
            what="guard recovery rejection")
        # One restart delay plus margin to detect unexpected loops.
        time.sleep(2.8)
        if len(read_pids(launches))!=2:
            reject("corrupt durable journal incorrectly launched Core8")
        attempts=logfile.read_text().split("CSQTT_CORE253_FAILCLOSED_PROBE",1)[-1]
        # The main guardian and ExecStopPost may each emit one refusal.
        # More than two means the service entered an unsafe restart loop.
        if attempts.count("CORE8_GUARD_FAILED:") > 2:
            reject("unrecoverable guard exit 75 caused automatic restart")
        if int(userprop(unit,"NRestarts") or "0") > 1:
            reject("systemd kept retrying after unrecoverable exit 75")
        print("CSQTT_USER_SYSTEMD_FAILCLOSED_CORRUPT_JOURNAL_PASS",flush=True)

        # Independently exercise systemd's actual StartLimitBurst=3 /
        # StartLimitIntervalSec=25. The already-corrupt journal is
        # deliberately cleared by this ONLY fake-proc synthetic test.
        # All three child crashes must restore the fake sysctl state;
        # the service manager must refuse a fourth rapid start.
        journal.unlink()
        cmd("systemctl","--user","reset-failed",unit)
        before_count=len(read_pids(launches))
        cmd("systemctl","--user","start",unit)
        launched=wait_until(
            lambda: read_pids(launches) if len(read_pids(launches))>before_count else None,
            timeout=9,what="rate-limit first child")
        if len(launched)!=before_count+1:
            reject("unexpected rate-limit first child count")
        print("CSQTT_STARTLIMIT_CONFIG",
              cmd("systemctl","--user","show",unit,
                  "-p","StartLimitBurst","-p","StartLimitIntervalUSec",
                  "-p","Restart","-p","RestartUSec","-p","NRestarts",
                  check=False).replace("\\n",";"),flush=True)
        for crash in range(3):
            count_before=len(read_pids(launches))
            pid=read_pids(launches)[-1]
            if not alive(pid):
                reject("rate-limit candidate exited before deliberate SIGKILL")
            print("CSQTT_STARTLIMIT_KILL index="+str(crash+1)+" pid="+str(pid),
                  flush=True)
            os.kill(pid,signal.SIGKILL)
            if crash<2:
                wait_until(
                    lambda: read_pids(launches) if len(read_pids(launches))>count_before else None,
                    timeout=9,what="next rate-limit child")
            else:
                try:
                    wait_until(
                        lambda: (userprop(unit,"Result")=="start-limit-hit"
                                 or len(read_pids(launches))>count_before),
                        timeout=10,what="rate limit or unexpected new process")
                finally:
                    print("CSQTT_STARTLIMIT_POST_THIRD",
                          cmd("systemctl","--user","show",unit,
                              "-p","ActiveState","-p","Result","-p","NRestarts",
                              "-p","ExecMainStatus","-p","StartLimitBurst",
                              check=False).replace("\\n",";"),
                          "NEW_PIDS="+str(read_pids(launches)[before_count:]),
                          "LOG_SUFFIX="+logfile.read_text(errors="replace")[-1500:],
                          flush=True)
                if userprop(unit,"Result")!="start-limit-hit":
                    reject("systemd did not prevent a fourth rapid start")
        time.sleep(1.5)
        if len(read_pids(launches))!=before_count+3:
            reject("systemd exceeded expected three starts within rate limit")
        wait_until(lambda: values(root,baseline)==baseline,
                   timeout=8,what="last fake sysctl restoration")
        if (state/g.JOURNAL).exists():
            reject("journal left behind after start-limit hit")
        for pid in read_pids(launches)[before_count:]:
            if alive(pid):
                reject("orphan synthetic Core8 after start-limit")
        print("CSQTT_USER_SYSTEMD_REAL_STARTLIMIT_BURST_3_PASS",flush=True)
    finally:
        cmd("systemctl","--user","stop",unit,check=False)
        cmd("systemctl","--user","reset-failed",unit,check=False)
        file.unlink(missing_ok=True)
        cmd("systemctl","--user","daemon-reload",check=False)
    print("CSQTT_USER_SYSTEMD_UNPRIVILEGED_FAKEPROC_LIFECYCLE_PASS",flush=True)

if __name__=="__main__":
    main()
