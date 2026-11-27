#!/usr/bin/env python3
"""Neutral dpkg/SQLite checks inside an offline QEMU guest, NOT DeployManager acceptance."""
import hashlib, json, os, pathlib, platform, shutil, socket, sqlite3, subprocess, tempfile

def run(args, check=True):
    result = subprocess.run(args, text=True, capture_output=True, timeout=90)
    if check and result.returncode:
        raise RuntimeError("command failed: " + " ".join(args) + "\n" + result.stdout + result.stderr)
    return result

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def validate_guest():
    # Reject before even writing a result file; remain safe under python -O.
    if os.geteuid() != 0 or not socket.gethostname().startswith("csqtt-neutral-"):
        raise SystemExit("requires a dedicated neutral guest")
    if run(["systemd-detect-virt", "--vm"]).stdout.strip() not in ("qemu", "kvm"):
        raise SystemExit("requires QEMU/KVM")
    if sorted(p.name for p in pathlib.Path("/sys/class/net").iterdir()) != ["lo"]:
        raise SystemExit("requires a guest without a network adapter")
    if pathlib.Path("/usr/local/bin/csqtt-core").exists() or pathlib.Path("/etc/csqtt-core").exists():
        raise SystemExit("refuses an existing Core installation")

def main():
    arch = run(["dpkg", "--print-architecture"]).stdout.strip()
    assert arch in ("amd64", "arm64", "armhf")
    name = "csqtt-neutral-lab-fixture"
    assert run(["dpkg-query", "-W", name], False).returncode != 0
    checks = []
    with tempfile.TemporaryDirectory(prefix="neutral-package-") as directory:
        root = pathlib.Path(directory)
        packages = []
        for version in ("1.0", "2.0"):
            tree = root / version
            (tree / "DEBIAN").mkdir(parents=True)
            (tree / "DEBIAN/control").write_text(
                f"Package: {name}\nVersion: {version}\nArchitecture: {arch}\n"
                "Maintainer: Local fixture <fixture@example.invalid>\n"
                "Description: inert offline laboratory data only\n")
            (tree / "DEBIAN/conffiles").write_text("/etc/csqtt-neutral-lab-fixture.conf\n")
            (tree / "etc").mkdir()
            (tree / "etc/csqtt-neutral-lab-fixture.conf").write_text("factory="+version+"\n")
            (tree / "usr/share" / name).mkdir(parents=True)
            (tree / "usr/share" / name / "version").write_text(version+"\n")
            target = root / (name+"_"+version+"_"+arch+".deb")
            run(["dpkg-deb", "--root-owner-group", "--build", str(tree), str(target)])
            packages.append(target)
        config = pathlib.Path("/etc/csqtt-neutral-lab-fixture.conf")
        runtime = pathlib.Path("/usr/share") / name / "version"
        run(["dpkg", "--install", str(packages[0])])
        assert runtime.read_text() == "1.0\n"
        checks.append({"case": "neutral_dpkg_install", "status": "PASS"})
        config.write_text("fixture_password=synthetic-only\nuser_setting=keep\n")
        db = root / "persistent.sqlite"
        with sqlite3.connect(db) as conn:
            conn.execute("CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT)")
            conn.execute("INSERT INTO settings VALUES ('fixture','preserve')")
        before = (sha(config), sha(db))
        run(["dpkg", "--force-confold", "--install", str(packages[1])])
        assert runtime.read_text() == "2.0\n"
        assert (sha(config), sha(db)) == before
        with sqlite3.connect(db) as conn:
            assert conn.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
            assert conn.execute("SELECT value FROM settings").fetchone()[0] == "preserve"
        checks.append({"case": "neutral_dpkg_update_config_sqlite", "status": "PASS"})
        damaged = root / "damaged.deb"
        damaged.write_bytes(packages[0].read_bytes()[:32])
        failure = run(["dpkg", "--install", str(damaged)], False)
        assert failure.returncode != 0
        assert runtime.read_text() == "2.0\n" and (sha(config), sha(db)) == before
        checks.append({"case": "neutral_dpkg_corrupt_package_refused", "status": "PASS"})
        run(["dpkg", "--remove", name])
        assert not runtime.exists() and (sha(config), sha(db)) == before
        checks.append({"case": "neutral_dpkg_remove_keeps_config_sqlite", "status": "PASS"})
        run(["dpkg", "--purge", name])
        assert not config.exists()
        checks.append({"case": "neutral_dpkg_purge", "status": "PASS"})
        package_hashes = {x.name: sha(x) for x in packages}
    assert not pathlib.Path(directory).exists()
    checks.append({"case": "neutral_fixture_temporary_cleanup", "status": "PASS"})
    return {"architecture": arch, "machine": platform.machine(), "hostname": socket.gethostname(),
            "machine_id": pathlib.Path("/etc/machine-id").read_text().strip(),
            "scope": "real offline QEMU guest dpkg/SQLite only; not CSQTT or DeployManager",
            "network_interfaces": ["lo"], "checks": checks, "package_sha256": package_hashes,
            "core_install_update_remove": "NOT TESTED", "automatic_core_rollback": "NOT TESTED",
            "ssh_interruption": "NOT TESTED"}

if __name__ == "__main__":
    validate_guest()
    try:
        result = main()
    except Exception as error:
        result = {"scope": "neutral package guest", "status": "FAIL", "error": str(error)}
    target = pathlib.Path("/var/lib/csqtt-neutral-lab")
    target.mkdir(mode=0o700, exist_ok=True)
    (target / "result.json").write_text(json.dumps(result, sort_keys=True)+"\n")
    print("CSQTT_NEUTRAL_RESULT "+json.dumps(result, sort_keys=True), flush=True)
