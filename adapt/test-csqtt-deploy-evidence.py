#!/usr/bin/env python3
"""Only synthetic evidence fixtures; no subprocess or network access."""
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("checker", Path(__file__).with_name("verify-csqtt-deploy-evidence.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)

def fixture(action):
    old = {key: str(i+1)*64 for i,key in enumerate(checker.RUNTIME)}
    r = {
        "schema":1, "scope":"isolated-lab", "action":action,
        "provenance":{"installer_sha256":checker.PINNED_INSTALLER,
            "candidate_sha256":checker.PINNED_SERVER, "executed_installer_sha256":checker.PINNED_INSTALLER, "controller_to_manager":True,
            "actual_ssh":True, "independent_postcheck":True},
        "process":{"exit_code":0, "timed_out":False, "terminated_by_signal":False,
            "markers":["CSQTT_DEPLOY_OK"]},
        "before":{"architecture":"aarch64", "default_route":"test-route", "runtime_hashes":old},
        "after":{"architecture":"aarch64", "default_route":"test-route", "ssh_reachable":True,
            "legacy_android_unchanged":True, "service_state":"running", "udp47000":"owned",
            "tcp47002":"owned", "tun_present":True, "panel_http":200, "db_integrity":"ok",
            "runtime_hashes":dict(old, binary=checker.PINNED_SERVER)}
    }
    if action == "uninstall":
        r["process"]["markers"]=[]
        r["after"].update(service_state="absent", udp47000="free", tcp47002="free",
                          tun_present=False, runtime_absent=True, persistent_state_preserved=True)
    if action == "rollback":
        r["provenance"].update(executed_installer_sha256="e"*64, fault_fixture_reviewed=True)
        r["process"].update(exit_code=30, markers=["CSQTT_DEPLOY_ERROR|cutover|fixture"],
                            automatic_rollback=True,post_cutover_fault=True)
        r["after"].update(runtime_hashes=copy.deepcopy(old), persistent_state_preserved=True)
    return r

class EvidenceTests(unittest.TestCase):
    def test_valid_synthetic_reports(self):
        for action in ("install","uninstall","rollback"):
            with self.subTest(action=action): self.assertEqual(checker.verify(fixture(action)), [])
    def test_all_critical_observations(self):
        fields = {
            "provenance":["installer_sha256","executed_installer_sha256","candidate_sha256","controller_to_manager","actual_ssh","independent_postcheck"],
            "process":["exit_code","timed_out","terminated_by_signal","markers"],
            "after":["architecture","default_route","ssh_reachable","legacy_android_unchanged","service_state","udp47000","tcp47002","tun_present","db_integrity"],
            "before":["architecture","default_route"],
        }
        for action in ("install","uninstall","rollback"):
            for section, keys in fields.items():
                for key in keys:
                    with self.subTest(action=action, section=section,key=key):
                        r=fixture(action);del r[section][key]
                        self.assertTrue(checker.verify(r))
    def test_failed_uninstall_wrapper(self):
        r=fixture("uninstall");r["process"]["exit_code"]=1
        self.assertIn("uninstall exit code 0 not observed", checker.verify(r))
    def test_rollback_missing_original_exit(self):
        r=fixture("rollback");del r["process"]["exit_code"]
        self.assertIn("failed candidate exit code not observed", checker.verify(r))
    def test_restore_all_hashes(self):
        for key in checker.RUNTIME:
            with self.subTest(key=key):
                r=fixture("rollback");r["after"]["runtime_hashes"][key]="f"*64
                self.assertTrue(checker.verify(r))
    def test_failure_cannot_be_success(self):
        r=fixture("install");r["process"]["markers"].append("CSQTT_DEPLOY_ERROR|activation|fixture")
        self.assertTrue(checker.verify(r))
    def test_bool_is_not_exit_code(self):
        for action,value in (("install",False),("uninstall",False),("rollback",True)):
            r=fixture(action);r["process"]["exit_code"]=value
            self.assertTrue(checker.verify(r))
    def test_refusal_is_not_rollback(self):
        for code in (2,20,42,73,-9,256):
            with self.subTest(code=code):
                r=fixture("rollback");r["process"]["exit_code"]=code
                self.assertTrue(checker.verify(r))
    def test_preflight_failure_is_not_postcutover_rollback(self):
        r=fixture("rollback");r["process"]["markers"]=["CSQTT_DEPLOY_ERROR|preflight|fixture"]
        self.assertTrue(checker.verify(r))
    def test_fault_fixture_must_be_distinct_and_reviewed(self):
        r=fixture("rollback");r["provenance"]["executed_installer_sha256"]=checker.PINNED_INSTALLER
        self.assertTrue(checker.verify(r))
        r=fixture("rollback");del r["provenance"]["fault_fixture_reviewed"]
        self.assertTrue(checker.verify(r))
    def test_route_change_and_false_health(self):
        r=fixture("install");r["after"]["default_route"]="changed";r["after"]["panel_http"]=401
        self.assertEqual(len(checker.verify(r)),2)
    def test_inventory_only_never_accepted(self):
        r=fixture("install");r["process"].update(exit_code=73,markers=["CSQTT_INV|end","CSQTT_ISOLATION_REQUIRED"])
        r["provenance"]["controller_to_manager"]=False
        self.assertTrue(checker.verify(r))
    def test_duplicate_success_marker(self):
        r=fixture("install");r["process"]["markers"]*=2
        self.assertTrue(checker.verify(r))
    def test_unknown_or_non_object(self):
        for item in ([],None,{},{"action":"unknown"}):
            self.assertTrue(checker.verify(item))
    def test_persistent_data_missing(self):
        for action in ("uninstall","rollback"):
            r=fixture(action);del r["after"]["persistent_state_preserved"]
            self.assertTrue(checker.verify(r))

if __name__ == "__main__":
    unittest.main(verbosity=2)
