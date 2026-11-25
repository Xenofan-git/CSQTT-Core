#!/usr/bin/env python3
"""Extract only read-only checks; no installer, SSH, service or network mutation."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

root = Path(__file__).resolve().parent.parent
source = (root / "server-assets/deploy-csqtt-core-250.sh").read_text()
def fn(name):
    start = source.index(name + "() {")
    return source[start:source.index("\n}\n", start)+2]

functions = "\n".join(fn(n) for n in (
    "inventory_emit", "inventory_port", "uninstall_precheck",
    "uninstall_runtime_paths_absent", "uninstall_postcheck"))
harness = r'''
set -euo pipefail
PEER_PORT=47000; WEB_PORT=47002; CSQTT_IFACE=csqtt-core0
CSQTT_CONFIG_DIR="$FIXTURE"; CSQTT_DATABASE_FILE=csqtt.db
systemctl(){
    [ "$*" = "show --value -p LoadState csqtt-core.service" ] || return 99
    [ "$UNIT_FAIL" = 0 ] || return 79
    printf '%s\n' "$UNIT"
}
ss(){
    case "$*" in
        "-H -lunp sport = :47000") [ "$UDP_FAIL" = 0 ] || return 79; printf '%s\n' "$UDP";;
        "-H -ltnp sport = :47002") [ "$TCP_FAIL" = 0 ] || return 79; printf '%s\n' "$TCP";;
        *) return 99;;
    esac
}
ip(){
    [ "$*" = "-o link show" ] || return 99
    [ "$IP_FAIL" = 0 ] || return 79
    printf '%s\n' "$LINKS"
}
csqtt_systemd_unit_is_managed(){ [ "$MANAGED" = 1 ]; }
csqtt_process_is_owned(){ [ "$1" = 1001 ]; }
kill(){ echo UNSAFE >&2; return 99; }
rm(){ echo UNSAFE >&2; return 99; }
'''
owned_udp = 'UNCONN 0 0 0.0.0.0:47000 0.0.0.0:* users:(("core",pid=1001,fd=3))'
unknown_udp = 'UNCONN 0 0 0.0.0.0:47000 0.0.0.0:*'
owned_tcp = 'LISTEN 0 128 0.0.0.0:47002 0.0.0.0:* users:(("core",pid=1001,fd=3))'
unknown_tcp = 'LISTEN 0 128 0.0.0.0:47002 0.0.0.0:*'
foreign_tcp = owned_tcp.replace("1001", "1002")

class Checks(unittest.TestCase):
    def invoke(self, action, expected, database=False, leftover=False, symlink=False, **kwargs):
        with tempfile.TemporaryDirectory(prefix="csqtt-uninstall-check-") as tmp:
            directory = Path(tmp)
            if database:
                (directory/"csqtt.db").write_bytes(b"fixture-db")
            if symlink:
                (directory/"csqtt.db").symlink_to(directory/"missing-db")
            if leftover:
                (directory/"leftover").symlink_to(directory/"missing")
            # Redirect only the inspected runtime path list to a temporary
            # fixture. No production paths are read or modified.
            f = functions.replace(
                'uninstall_runtime_paths_absent /usr/local/bin/csqtt-core /usr/local/bin/csqtt-core-cascade \\\n        /etc/systemd/system/csqtt-core.service /run/csqtt-core /usr/local/lib/csqtt-core /var/lib/csqtt-core',
                'uninstall_runtime_paths_absent "$FIXTURE/leftover"')
            self.assertNotIn("uninstall_runtime_paths_absent /usr/local/bin", f)
            env = {"PATH":os.environ["PATH"], "FIXTURE":tmp, "UNIT_FAIL":"0",
                   "UDP_FAIL":"0","TCP_FAIL":"0","IP_FAIL":"0","UNIT":"not-found",
                   "UDP":"","TCP":"","MANAGED":"0","LINKS":"1: lo: <LOOPBACK> mtu 65536",
                   **kwargs}
            r=subprocess.run(["bash","-c",harness+"\n"+f+"\n"+action],env=env,
                capture_output=True,text=True,timeout=5)
            self.assertNotIn("UNSAFE",r.stderr)
            self.assertEqual(r.returncode==0,expected,(action,kwargs,r.returncode,r.stdout,r.stderr))
            return r.stdout
    def test_precheck(self):
        for name, settings, expected in (
            ("empty",{},True),
            ("owned",{"UNIT":"loaded","MANAGED":"1","UDP":owned_udp,"TCP":owned_tcp},True),
            ("foreign unit",{"UNIT":"loaded"},False),
            ("unknown unit",{"UNIT":"unknown"},False),
            ("systemctl error",{"UNIT_FAIL":"1"},False),
            ("UDP query failed",{"UDP_FAIL":"1"},False),
            ("TCP query failed",{"TCP_FAIL":"1"},False),
            ("foreign TCP",{"TCP":foreign_tcp},False),
            ("anonymous UDP",{"UDP":unknown_udp},False),
            ("mixed anonymous UDP",{"UDP":owned_udp+"\n"+unknown_udp},False),
            ("mixed anonymous TCP",{"TCP":owned_tcp+"\n"+unknown_tcp},False)):
            with self.subTest(name=name):
                self.invoke("uninstall_precheck",expected,**settings)
    def test_postcheck(self):
        for name, settings, expected in (
            ("absent",{},True),
            ("retained service",{"UNIT":"loaded"},False),
            ("failed unit query",{"UNIT_FAIL":"1"},False),
            ("retained UDP",{"UDP":owned_udp},False),
            ("retained TCP",{"TCP":owned_tcp},False),
            ("foreign TCP",{"TCP":foreign_tcp},False),
            ("failed UDP query",{"UDP_FAIL":"1"},False),
            ("failed TCP query",{"TCP_FAIL":"1"},False),
            ("failed link query",{"IP_FAIL":"1"},False),
            ("empty links",{"LINKS":""},False),
            ("malformed links",{"LINKS":"invalid"},False),
            ("retained TUN",{"LINKS":"1: lo: <LOOPBACK>\n2: csqtt-core0: <UP>"},False),
            ("retained TUN peer",{"LINKS":"2: csqtt-core0@if5: <UP>"},False),
            ("different interface",{"LINKS":"2: csqtt-core00: <UP>"},True),
            ("dangling runtime symlink",{"leftover":True},False)):
            with self.subTest(name=name):
                self.invoke("uninstall_postcheck 0",expected,**settings)
    def test_database(self):
        self.invoke("uninstall_postcheck 1",True,database=True)
        self.invoke("uninstall_postcheck 1",False)
        self.invoke("uninstall_postcheck 1",False,symlink=True)
    def test_inventory_mixed_anonymous(self):
        for proto, port, rows in (("udp",47000,{"UDP":owned_udp+"\n"+unknown_udp}),
                                  ("tcp",47002,{"TCP":owned_tcp+"\n"+unknown_tcp})):
            self.assertIn("state=busy|owner=unknown",self.invoke(f"inventory_port {proto} {port}",True,**rows))
    def test_source_order(self):
        body=fn("do_uninstall")
        self.assertLess(body.index("uninstall_precheck"), body.index("remove_all_csqtt_docker_containers"))
        self.assertLess(body.index("uninstall_postcheck"),body.index("CSQTT_UNINSTALL_OK"))
        self.assertIn('exit "$EXIT_PREFLIGHT_FAILED"',body)
        self.assertIn('exit "$EXIT_CUTOVER_FAILED"',body)
        self.assertIn('CSQTT_CORE_ALLOW_MUTATION:-}" = "isolated-lab-only"',source)
if __name__=="__main__":
    unittest.main(verbosity=2)
