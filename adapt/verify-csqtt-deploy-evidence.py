#!/usr/bin/env python3
"""Offline release-evidence checker. Reads JSON only; never contacts or changes a host.

A marker, a successful shell exit, or a restored service alone is not sufficient.
Missing or contradictory observations are reported explicitly, never inferred.
The caller is responsible for independently collecting authentic observations.
"""
import argparse
import json
import re
from pathlib import Path

PINNED_INSTALLER = "18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68"
PINNED_SERVER = "8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d"
RUNTIME = ("binary", "unit", "environment", "network_helper", "tun_helper")
DIGEST = re.compile(r"[0-9a-f]{64}")

def verify(report):
    errors = []
    def require(condition, reason):
        if not condition:
            errors.append(reason)
    def obj(parent, key):
        value = parent.get(key)
        return value if isinstance(value, dict) else {}
    def digest(value):
        return isinstance(value, str) and DIGEST.fullmatch(value) is not None
    def integer(value):
        return type(value) is int
    if not isinstance(report, dict):
        return ["report must be a JSON object"]
    action = report.get("action")
    if action not in ("install", "uninstall", "rollback"):
        return ["unknown operation"]
    before = obj(report, "before")
    after = obj(report, "after")
    process = obj(report, "process")
    provenance = obj(report, "provenance")
    require(report.get("schema") == 1 and type(report.get("schema")) is int, "unsupported evidence schema")
    require(report.get("scope") == "isolated-lab", "isolated lab scope not established")
    require(provenance.get("installer_sha256") == PINNED_INSTALLER, "base installer identity not established")
    executed = provenance.get("executed_installer_sha256")
    if action == "rollback":
        require(digest(executed) and executed != PINNED_INSTALLER
                and provenance.get("fault_fixture_reviewed") is True,
                "separate reviewed fault-injection fixture identity not established")
    else:
        require(executed == PINNED_INSTALLER, "executed installer identity not established")
    require(provenance.get("candidate_sha256") == PINNED_SERVER, "candidate identity not established")
    require(provenance.get("controller_to_manager") is True, "controller-to-manager execution not observed")
    require(provenance.get("actual_ssh") is True, "real SSH execution not observed")
    require(provenance.get("independent_postcheck") is True, "independent postcheck not observed")
    require(before.get("architecture") == after.get("architecture") == "aarch64", "ARM64 not established")
    require(isinstance(before.get("default_route"), str) and bool(before.get("default_route", "").strip())
            and before.get("default_route") == after.get("default_route"), "default route missing or changed")
    require(after.get("ssh_reachable") is True, "SSH not verified after operation")
    require(after.get("legacy_android_unchanged") is True, "legacy Android runtime isolation not verified")
    code = process.get("exit_code")
    markers = process.get("markers")
    if not isinstance(markers, list) or any(not isinstance(x, str) for x in markers):
        markers = []
        errors.append("structured process markers missing or malformed")
    require(process.get("timed_out") is False, "process completion not established")
    require(process.get("terminated_by_signal") is False, "process may have been interrupted")
    fail = any(x.startswith("CSQTT_DEPLOY_ERROR|") for x in markers)
    files = obj(after, "runtime_hashes")
    old = obj(before, "runtime_hashes")
    if action == "install":
        require(integer(code) and code == 0, "installer exit code 0 not observed")
        require(markers.count("CSQTT_DEPLOY_OK") == 1 and not fail, "unique install success marker missing or contradicted")
        require(files.get("binary") == PINNED_SERVER, "installed candidate SHA mismatch")
    elif action == "uninstall":
        require(integer(code) and code == 0, "uninstall exit code 0 not observed")
        require(not fail and "CSQTT_DEPLOY_OK" not in markers, "uninstall result contradicts process output")
        require(after.get("service_state") == "absent", "systemd service not absent")
        require(after.get("udp47000") == after.get("tcp47002") == "free", "server ports not verified free")
        require(after.get("tun_present") is False, "TUN removal not verified")
        require(after.get("runtime_absent") is True, "runtime removal not verified")
    else:
        require(integer(code) and 1 <= code <= 255 and code not in (2, 20, 42, 73), "failed candidate exit code not observed")
        require(any(x.startswith(("CSQTT_DEPLOY_ERROR|cutover|", "CSQTT_DEPLOY_ERROR|activation|")) for x in markers) and "CSQTT_DEPLOY_OK" not in markers, "explicit post-cutover candidate failure not established")
        require(process.get("post_cutover_fault") is True, "post-cutover failure not observed")
        require(process.get("automatic_rollback") is True, "automatic rollback not established")
        for key in RUNTIME:
            require(digest(old.get(key)) and old.get(key) == files.get(key), "restored "+key+" hash not verified")
    if action in ("install", "rollback"):
        require(after.get("service_state") == "running", "running service not verified")
        require(after.get("udp47000") == after.get("tcp47002") == "owned", "owned server ports not verified")
        require(after.get("tun_present") is True, "TUN not verified")
        require(type(after.get("panel_http")) is int and after.get("panel_http") == 200, "panel HTTP 200 not verified")
    require(after.get("db_integrity") == "ok", "SQLite integrity not verified")
    if action in ("uninstall", "rollback"):
        require(after.get("persistent_state_preserved") is True, "persistent state preservation not verified")
    return errors

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    args = parser.parse_args()
    try:
        with args.evidence.open(encoding="utf-8") as f:
            report = json.load(f)
    except (OSError, ValueError):
        print("NOT_ACCEPTED: evidence unreadable")
        return 2
    errors = verify(report)
    if errors:
        print("NOT_ACCEPTED")
        for error in errors:
            print("- " + error)
        return 1
    print("EVIDENCE_CONSISTENT: " + report["action"])
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
