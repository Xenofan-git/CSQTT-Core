#!/usr/bin/env python3
"""Actual ARM64 manager + stdio SFTP; simulated SSH commands, never an installer.
Usage: python3 adapt/test-csqtt-deploy-lifecycle-process.py CANDIDATE_BUNDLE [CONTROLLER_ARM64_TEST]
Requires existing qemu-aarch64 and OpenSSH sftp-server. No network connection.
"""
import hashlib, json, os, pathlib, re, shutil, subprocess, sys, tempfile

FAKE_SSH = r'''#!/usr/bin/python3
import hashlib,json,os,pathlib,re,sys
state_path=pathlib.Path(os.environ["CSQTT_FIXTURE_STATE"])
state=json.loads(state_path.read_text())
command=sys.argv[-1]
if command=="sftp" and "-s" in sys.argv:
 os.execv("/usr/lib/openssh/sftp-server",["sftp-server"])
if command=="uname -m":
 print("aarch64");raise SystemExit(0)
script=sys.stdin.read()
if command=="python3 -":
 print(json.dumps(state["evidence"]));raise SystemExit(0)
if command=="bash -s inventory":
 assert hashlib.sha256(script.encode()).hexdigest()==os.environ["CSQTT_FIXTURE_INSTALLER_SHA"]
 installed=bool(state["evidence"]["binary"])
 print("CSQTT_INV|server|place=core|runtime=systemd|state="+("running|owner=core_candidate" if installed else "absent|owner=none"))
 print("CSQTT_INV|interface|name=csqtt-core0|state="+("present" if installed else "absent")+"|owner=unverified")
 print("CSQTT_INV|database|state="+("present" if state["evidence"]["database"] else "absent")+"|name=csqtt.db")
 for proto,port in [("udp",47000),("tcp",47002)]:
  print(f"CSQTT_INV|port|proto={proto}|port={port}|state="+("busy|owner=core_candidate" if installed else "free|owner=none"))
 print("CSQTT_INV|end");raise SystemExit(0)
if command!="bash -s":raise SystemExit(99)
match=re.search(r"CSQTT_DEPLOY_WORKSPACE=(/tmp/csqtt-deploy-[a-f0-9]{32}) bash ./deploy-csqtt-core-lifecycle.sh (install|update|uninstall)\n",script)
assert match,"unexpected command"
workspace=pathlib.Path(match[1]);action=match[2]
assert workspace.stat().st_mode & 0o777==0o700
assert hashlib.sha256((workspace/"deploy-csqtt-core-lifecycle.sh").read_bytes()).hexdigest()==os.environ["CSQTT_FIXTURE_INSTALLER_SHA"]
if action!="uninstall":
 assert hashlib.sha256((workspace/"csqtt-core8-linux-arm64").read_bytes()).hexdigest()==os.environ["CSQTT_FIXTURE_SERVER_SHA"]
if action=="install":
 assert (workspace/"csqtt-core.env").is_file() and (workspace/"deploy-overrides.json").is_file()
else:
 assert not (workspace/"csqtt-core.env").exists()
 assert not (workspace/"deploy-overrides.json").exists()
with open(os.environ["CSQTT_FIXTURE_WORKSPACES"],"a") as f:f.write(str(workspace)+"\n")
# This branch deliberately NEVER calls bash or the uploaded installer.
if state.get("rollback"):
 print("CSQTT_DEPLOY_ROLLED_BACK");raise SystemExit(30)
if action=="uninstall":
 state["evidence"]["binary"]=state["evidence"]["unit"]=""
else:
 state["evidence"].update(binary=os.environ["CSQTT_FIXTURE_SERVER_SHA"],unit="4"*64,
  environment="1"*64,database=("5"*64 if state.get("damage") else "2"*64),config="3"*64)
state_path.write_text(json.dumps(state))
print("CSQTT_PROGRESS|0.6|fixture")
print("CSQTT_UNINSTALL_OK" if action=="uninstall" else "CSQTT_DEPLOY_OK")
'''

def main():
    bundle=pathlib.Path(sys.argv[1]).resolve()
    assert (bundle/"deploy-manager").is_file()
    assert pathlib.Path("/usr/lib/openssh/sftp-server").is_file()
    with tempfile.TemporaryDirectory(prefix="csqtt-lifecycle-process-fixture-") as temp:
        root=pathlib.Path(temp)
        ssh=root/"ssh";ssh.write_text(FAKE_SSH);ssh.chmod(0o700)
        states=root/"state.json";workspaces=root/"workspaces.txt"
        evidence=dict(machineID="a"*32,virtual="qemu",os="debian",
                      binary="",unit="",environment="",database="",config="")
        states.write_text(json.dumps(dict(evidence=evidence)))
        env=dict(os.environ,PATH=str(root)+":/usr/bin:/bin",
            CSQTT_CORE_ALLOW_MUTATION="isolated-lab-only",
            CSQTT_CORE_DEPLOY_LAB_TARGET="192.0.2.77:22",
            CSQTT_CORE_DEPLOY_LAB_MACHINE_ID="a"*32,
            CSQTT_FIXTURE_STATE=str(states),CSQTT_FIXTURE_WORKSPACES=str(workspaces),
            CSQTT_FIXTURE_INSTALLER_SHA=hashlib.sha256((bundle/"server-assets/deploy-csqtt-core-lifecycle.sh").read_bytes()).hexdigest(),
            CSQTT_FIXTURE_SERVER_SHA="8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d")
        request=dict(protocol="CSQTT",host="192.0.2.77",user="root",keyPath="/inert-fixture-key",
                     mainPassword="fixturemain",webUser="fixtureuser",webPassword="fixtureweb",deviceId="fixture")
        def run(action,success=True,rollback=False):
            data=dict(request,action=action)
            api=len(sys.argv)>2
            command=["qemu-aarch64",str(bundle/"deploy-manager"),"--isolated-lifecycle","--assets",str(bundle/"server-assets")]
            if api:
                command=["qemu-aarch64",sys.argv[2],"-test.v","-test.run","^TestDeployActualLifecycleProcessContract$"]
                env.update(CSQTT_DEPLOY_LIFECYCLE_PROCESS_FIXTURE="1",CSQTT_LIFECYCLE_REQUEST=json.dumps(data),
                    CSQTT_LIFECYCLE_EXPECTED_RESULT=("uninstalled" if action=="uninstall" else "success") if success else "error",
                    CSQTT_LIFECYCLE_EXPECT_ROLLBACK="1" if rollback else "")
            result=subprocess.run(command,
                input=json.dumps(data),text=True,env=env,capture_output=True,timeout=90)
            marker="CSQTT_UNINSTALL_OK" if action=="uninstall" else "CSQTT_DEPLOY_OK"
            if api:
                assert result.returncode==0,(result.stdout,result.stderr)
            elif success:
                assert result.returncode==0,(action,result.stdout,result.stderr)
                assert result.stdout.splitlines().count(marker)==1
            else:
                assert result.returncode!=0 and marker not in result.stdout,(result.stdout,result.stderr)
                assert ("CSQTT_DEPLOY_ROLLED_BACK" in result.stdout)==rollback
            for item in workspaces.read_text().splitlines():
                assert not pathlib.Path(item).exists(),"staging leak: "+item
            print("SIMULATED_LIFECYCLE_PASS",action,"success" if success else "verified rollback" if rollback else "preservation failure rejected")
        run("install")
        installed=json.loads(states.read_text())
        run("update")
        broken=dict(installed,damage=True);states.write_text(json.dumps(broken))
        run("update",success=False)
        states.write_text(json.dumps(dict(installed,rollback=True)))
        run("update",success=False,rollback=True)
        states.write_text(json.dumps(installed))
        run("uninstall")
        print("No real SSH, service installation or network mutation was performed.")

if __name__=="__main__":
    main()
