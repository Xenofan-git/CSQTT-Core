#!/usr/bin/env python3
"""Run production rollback functions with fake systemctl/ip in a temporary tree."""
from pathlib import Path
import os
import subprocess
import tempfile

source = Path("server-assets/deploy-csqtt-core-250.sh").read_text()
start = source.index("discard_unarmed_systemd_snapshot() {")
end = source.index("\nvalidate_port() {", start)
production = source[start:end]

systemctl = """#!/bin/sh
set -eu
case "$1" in
 is-active) [ "$(cat "$MOCK_ROOT/active")" = yes ] ;;
 is-enabled) [ "$(cat "$MOCK_ROOT/enabled")" = yes ] ;;
 daemon-reload) : ;;
 stop) echo no > "$MOCK_ROOT/active" ;;
 enable|disable|restart)
   [ "$MOCK_FAIL_ACTION" != "$1" ] || exit 41
   if [ "$1" = enable ]; then echo yes > "$MOCK_ROOT/enabled"
   elif [ "$1" = disable ]; then echo no > "$MOCK_ROOT/enabled"
   else [ -f "$MOCK_ROOT/bin/csqtt-core" ] || exit 42
        if [ "$MOCK_FAIL_ACTION" != restart-no-activate ]; then
          echo yes > "$MOCK_ROOT/active"
        fi
   fi ;;
 *) echo "unexpected systemctl $1" >&2; exit 43 ;;
esac
"""
ip = """#!/bin/sh
set -eu
[ "$1" = link ] && [ "$2" = show ] && [ "$3" = csqtt-core0 ] &&
[ "$(cat "$MOCK_ROOT/active")" = yes ] &&
[ "$MOCK_FAIL_ACTION" != tun-missing ]
"""

# The production rollback also needs to survive disk / permission failures
# while restoring the old ELF and systemd unit, not only systemctl failures.
install = """#!/bin/sh
set -eu
[ "$MOCK_FAIL_ACTION" != install ] || exit 41
/usr/bin/install "$@"
if [ "$MOCK_FAIL_ACTION" = install-corrupt ]; then
  for arg in "$@"; do dest=$arg; done
  printf 'CORRUPTED AFTER INSTALL\n' >> "$dest"
fi
"""
cp = """#!/bin/sh
set -eu
case "$2" in
 "$MOCK_ROOT"/rollback.*/unit)
   [ "$MOCK_FAIL_ACTION" != copy-unit ] || exit 41
   /usr/bin/cp "$@"
   if [ "$MOCK_FAIL_ACTION" = copy-unit-corrupt ]; then
     for arg in "$@"; do dest=$arg; done
     printf 'CORRUPTED AFTER COPY\n' >> "$dest"
   fi
   exit 0 ;;
esac
exec /usr/bin/cp "$@"
"""

# The real rollback polls a restored TUN up to ten times. Eliminate wait
# time in fault injection, not in the production installer.
sleep = """#!/bin/sh
set -eu
case "$1" in 1) exit 0 ;; *) exit 44 ;; esac
"""

curl = """#!/bin/sh
set -eu
case "$MOCK_FAIL_ACTION" in
 web-unreachable) exit 7 ;;
 web-http-403) printf '403'; exit 0 ;;
 web-http-401) printf '401'; exit 0 ;;
esac
for arg in "$@"; do
 case "$arg" in
  https://127.0.0.1:47002/*)
   [ "$arg" = "$MOCK_EXPECTED_PANEL_URL" ] || exit 8
   printf '200'
   exit 0 ;;
  http://*|https://*) exit 9 ;;
 esac
done
exit 10
"""

cases = 0
for originally_active in (False, True):
    for originally_enabled in (False, True):
        fail_choices = ("", "install", "install-corrupt", "copy-unit", "copy-unit-corrupt",
                        "enable" if originally_enabled else "disable") + (
            ("restart", "restart-no-activate", "tun-missing", "web-unreachable", "web-http-403", "web-http-401")
            if originally_active else ()
        )
        for fail in fail_choices:
            cases += 1
            with tempfile.TemporaryDirectory(prefix="csqtt-rollback-mock-") as tmp:
                root = Path(tmp)
                for sub in ("bin", "systemd", "mockbin"):
                    (root/sub).mkdir()
                binary = root/"bin/csqtt-core"
                unit = root/"systemd/csqtt-core.service"
                config = root/"config"
                helpers = root/"helpers"
                binary.write_text("old verified binary\n")
                unit.write_text("ExecStart=/usr/local/bin/csqtt-core --listen 0.0.0.0:47000 --web-port 47002 --config-dir /etc/csqtt-core\n")
                # Previously disabled old units may have no config directory
                # at all. Candidate-owned files still must not survive rollback.
                if originally_enabled:
                    config.mkdir()
                    (config/"csqtt-core.env").write_text("OLD_ENV=ok\nCSQTT_WEB_SECRET_PATH=old-panel-secret\n")
                    (config/"deploy-overrides.json").write_text("OLD_OVERRIDE\n")
                    (config/"web_cert.pem").write_text("OLD_CERT\n")
                    (config/"web_key.pem").write_text("OLD_KEY\n")
                    (config/"letsencrypt-ip.env").write_text("OLD_LE\n")
                    (config/"csqtt.db").write_text("live SQLite data")
                    (config/"unrelated.txt").write_text("keep unrelated state\n")
                # Old runtime may lack helper directory. A pre-existing
                # helper directory may also lack the newly added Docker helper.
                if originally_enabled:
                    helpers.mkdir()
                    (helpers/"network-up.sh").write_text("echo old\n")
                    (helpers/"tun-recover.sh").write_text("echo old tun\n")
                (root/"active").write_text("yes" if originally_active else "no")
                (root/"enabled").write_text("yes" if originally_enabled else "no")
                for name, contents in (("systemctl", systemctl), ("ip", ip),
                                       ("install", install), ("cp", cp), ("sleep", sleep), ("curl", curl)):
                    shim = root/"mockbin"/name
                    shim.write_text(contents)
                    shim.chmod(0o755)
                functions = production
                for old, new in (
                    ("/usr/local/bin/csqtt-core", str(binary)),
                    ("/etc/systemd/system/csqtt-core.service", str(unit)),
                    ("/usr/local/lib/csqtt-core", str(helpers)),
                    ("/tmp/csqtt-core-rollback.XXXXXXXX", str(root/"rollback.XXXXXXXX")),
                ):
                    functions = functions.replace(old, new)
                sh = f"""
set -euo pipefail
DEPLOY_MODE=systemd
CSQTT_CONFIG_DIR={str(config)!r}
CSQTT_IFACE=csqtt-core0
WEB_PORT=49999
CSQTT_WEB_SECRET_PATH=candidate-secret-do-not-use
ROLLBACK_DIR=""
ROLLBACK_ARMED=0
ROLLBACK_WAS_ACTIVE=0
ROLLBACK_WAS_ENABLED=0
log_info() {{ :; }}
log_warn() {{ :; }}
log_error() {{ :; }}
{functions}
snapshot_existing_systemd
[ "$ROLLBACK_WAS_ACTIVE" -eq {int(originally_active)} ]
[ "$ROLLBACK_WAS_ENABLED" -eq {int(originally_enabled)} ]
# The new candidate requests a different web port/path; the rollback MUST
# use the old unit + old env rather than candidate settings.
# Simulate a successful cutover stop/disable without consuming the fault
# injection reserved for the subsequent rollback action. Candidate creates
# the config dir when the old runtime lacked one.
mkdir -p {str(config)!r}
echo 'live SQLite data' > {str(config/'csqtt.db')!r}
echo 'keep unrelated state' > {str(config/'unrelated.txt')!r}
echo no > {str(root/'active')!r}
echo no > {str(root/'enabled')!r}
rm -f {str(binary)!r} {str(unit)!r}
echo NEW_ENV=bad > {str(config/'csqtt-core.env')!r}
echo NEW_OVERRIDE > {str(config/'deploy-overrides.json')!r}
echo NEW_CERT > {str(config/'web_cert.pem')!r}
echo NEW_KEY > {str(config/'web_key.pem')!r}
echo NEW_LE > {str(config/'letsencrypt-ip.env')!r}
mkdir -p {str(helpers)!r}
echo 'keep unrelated helper' > {str(helpers/'unrelated.txt')!r}
echo new > {str(helpers/'network-up.sh')!r}
echo newtun > {str(helpers/'tun-recover.sh')!r}
echo newdocker > {str(helpers/'docker-prereq.sh')!r}
if finish_deploy_rollback; then result=success; else result=failed; fi
if [ -z "$MOCK_FAIL_ACTION" ] || [ "$MOCK_FAIL_ACTION" = web-http-401 ]; then
  [ "$result" = success ]
  [ "$ROLLBACK_ARMED" -eq 0 ]
  [ -z "$ROLLBACK_DIR" ]
  [ -z "$(find {str(root)!r} -maxdepth 1 -name 'rollback.*' -print)" ]
  [ "$(cat {str(root/'enabled')!r})" = {'yes' if originally_enabled else 'no'} ]
  [ "$(cat {str(root/'active')!r})" = {'yes' if originally_active else 'no'} ]
  [ "$(cat {str(binary)!r})" = 'old verified binary' ]
  grep -Fq -- '--web-port 47002' {str(unit)!r}
  if [ {int(originally_enabled)} -eq 1 ]; then
    grep -Fxq 'OLD_ENV=ok' {str(config/'csqtt-core.env')!r}
  grep -Fxq 'CSQTT_WEB_SECRET_PATH=old-panel-secret' {str(config/'csqtt-core.env')!r}
    [ "$(cat {str(config/'deploy-overrides.json')!r})" = 'OLD_OVERRIDE' ]
    [ "$(cat {str(config/'web_cert.pem')!r})" = 'OLD_CERT' ]
    [ "$(cat {str(config/'web_key.pem')!r})" = 'OLD_KEY' ]
    [ "$(cat {str(config/'letsencrypt-ip.env')!r})" = 'OLD_LE' ]
  else
    [ ! -e {str(config/'csqtt-core.env')!r} ]
    [ ! -e {str(config/'deploy-overrides.json')!r} ]
    [ ! -e {str(config/'web_cert.pem')!r} ]
    [ ! -e {str(config/'web_key.pem')!r} ]
    [ ! -e {str(config/'letsencrypt-ip.env')!r} ]
  fi
  [ "$(cat {str(config/'unrelated.txt')!r})" = 'keep unrelated state' ]
  if [ {int(originally_enabled)} -eq 1 ]; then
    [ "$(cat {str(helpers/'network-up.sh')!r})" = 'echo old' ]
    [ "$(cat {str(helpers/'tun-recover.sh')!r})" = 'echo old tun' ]
  else
    [ ! -e {str(helpers/'network-up.sh')!r} ]
    [ ! -e {str(helpers/'tun-recover.sh')!r} ]
  fi
  [ ! -e {str(helpers/'docker-prereq.sh')!r} ]
  [ "$(cat {str(helpers/'unrelated.txt')!r})" = 'keep unrelated helper' ]
else
  [ "$result" = failed ]
  [ "$ROLLBACK_ARMED" -eq 1 ]
  [ -f "$ROLLBACK_DIR/binary" ]
  [ -f "$ROLLBACK_DIR/unit" ]
  if [ {int(originally_enabled)} -eq 1 ]; then
    [ -d "$ROLLBACK_DIR/config" ]
  else
    [ ! -e "$ROLLBACK_DIR/config" ]
  fi
  [ "$(stat -c %a "$ROLLBACK_DIR")" = 700 ]
  # The transient systemctl error is now resolved. The same protected
  # snapshot must allow an operator to retry without re-installation.
  export MOCK_FAIL_ACTION=''
  finish_deploy_rollback
  [ "$ROLLBACK_ARMED" -eq 0 ]
  [ -z "$ROLLBACK_DIR" ]
  [ -z "$(find {str(root)!r} -maxdepth 1 -name 'rollback.*' -print)" ]
  [ "$(cat {str(root/'enabled')!r})" = {'yes' if originally_enabled else 'no'} ]
  [ "$(cat {str(root/'active')!r})" = {'yes' if originally_active else 'no'} ]
  [ "$(cat {str(binary)!r})" = 'old verified binary' ]
  grep -Fq -- '--web-port 47002' {str(unit)!r}
  if [ {int(originally_enabled)} -eq 1 ]; then
    grep -Fxq 'OLD_ENV=ok' {str(config/'csqtt-core.env')!r}
  grep -Fxq 'CSQTT_WEB_SECRET_PATH=old-panel-secret' {str(config/'csqtt-core.env')!r}
    [ "$(cat {str(config/'deploy-overrides.json')!r})" = 'OLD_OVERRIDE' ]
    [ "$(cat {str(config/'web_cert.pem')!r})" = 'OLD_CERT' ]
    [ "$(cat {str(config/'web_key.pem')!r})" = 'OLD_KEY' ]
    [ "$(cat {str(config/'letsencrypt-ip.env')!r})" = 'OLD_LE' ]
  else
    [ ! -e {str(config/'csqtt-core.env')!r} ]
    [ ! -e {str(config/'deploy-overrides.json')!r} ]
    [ ! -e {str(config/'web_cert.pem')!r} ]
    [ ! -e {str(config/'web_key.pem')!r} ]
    [ ! -e {str(config/'letsencrypt-ip.env')!r} ]
  fi
  [ "$(cat {str(config/'unrelated.txt')!r})" = 'keep unrelated state' ]
  if [ {int(originally_enabled)} -eq 1 ]; then
    [ "$(cat {str(helpers/'network-up.sh')!r})" = 'echo old' ]
    [ "$(cat {str(helpers/'tun-recover.sh')!r})" = 'echo old tun' ]
  else
    [ ! -e {str(helpers/'network-up.sh')!r} ]
    [ ! -e {str(helpers/'tun-recover.sh')!r} ]
  fi
  [ ! -e {str(helpers/'docker-prereq.sh')!r} ]
  [ "$(cat {str(helpers/'unrelated.txt')!r})" = 'keep unrelated helper' ]
fi
[ "$(cat {str(config/'csqtt.db')!r})" = 'live SQLite data' ]
"""
                env = dict(os.environ)
                env.update(MOCK_ROOT=str(root), MOCK_FAIL_ACTION=fail,
                           MOCK_EXPECTED_PANEL_URL=("https://127.0.0.1:47002/old-panel-secret/"
                                                    if originally_enabled else "https://127.0.0.1:47002/"),
                           PATH=f"{root/'mockbin'}:{os.environ.get('PATH','/usr/bin:/bin')}")
                result = subprocess.run(["bash", "-c", sh], capture_output=True,
                                        text=True, env=env, timeout=15)
                assert result.returncode == 0, (
                    originally_active, originally_enabled, fail,
                    result.returncode, result.stdout, result.stderr
                )
                print(f"PASS active={originally_active} enabled={originally_enabled} fail={fail or 'none'}")
print(f"PASS {cases} isolated rollback behavior scenarios; host unchanged")

# The first safety decision is whether to snapshot at all. An interrupted old
# installation (only an ELF or only a systemd unit) must NOT be mistaken for
# a clean install: deployment would otherwise overwrite the only known runtime
# without a rollback lease. Reject dangling or externally pointed symlinks too.
for prior_state in ("fresh", "binary-only", "unit-only",
                    "symlink-binary", "symlink-unit", "complete"):
    with tempfile.TemporaryDirectory(prefix="csqtt-prior-runtime-") as tmp:
        root = Path(tmp)
        for sub in ("bin", "systemd", "mockbin"):
            (root/sub).mkdir()
        binary = root/"bin/csqtt-core"
        unit = root/"systemd/csqtt-core.service"
        if prior_state in ("binary-only", "complete"):
            binary.write_text("unchanged existing ELF\n")
        if prior_state in ("unit-only", "complete"):
            unit.write_text("ExecStart=existing\n")
        if prior_state == "symlink-binary":
            target = root/"external-old-binary"
            target.write_text("external ELF must not be followed\n")
            binary.symlink_to(target)
            unit.write_text("ExecStart=existing\n")
        if prior_state == "symlink-unit":
            target = root/"external-old-unit"
            target.write_text("external unit must not be followed\n")
            unit.symlink_to(target)
            binary.write_text("unchanged existing ELF\n")
        (root/"active").write_text("no")
        (root/"enabled").write_text("no")
        ctl = root/"mockbin/systemctl"
        ctl.write_text(systemctl)
        ctl.chmod(0o755)
        scoped = production
        for old, new in (
            ("/usr/local/bin/csqtt-core", str(binary)),
            ("/etc/systemd/system/csqtt-core.service", str(unit)),
            ("/usr/local/lib/csqtt-core", str(root/"helpers")),
            ("/tmp/csqtt-core-rollback.XXXXXXXX", str(root/"rollback.XXXXXXXX")),
        ):
            scoped = scoped.replace(old, new)
        script = f"""
set -euo pipefail
DEPLOY_MODE=systemd
CSQTT_CONFIG_DIR={str(root/'config')!r}
CSQTT_IFACE=csqtt-core0
WEB_PORT=49999
CSQTT_WEB_SECRET_PATH=candidate-secret-do-not-use
ROLLBACK_DIR=""
ROLLBACK_ARMED=0
ROLLBACK_WAS_ACTIVE=0
ROLLBACK_WAS_ENABLED=0
log_info() {{ :; }}
log_warn() {{ :; }}
log_error() {{ printf 'EXPECTED_ABORT:%s\\n' "$*" >&2; }}
{scoped}
result=success
if snapshot_existing_systemd; then :; else result=failed; fi
case {prior_state!r} in
  fresh)
    [ "$result" = success ]
    [ "$ROLLBACK_ARMED" -eq 0 ]
    ;;
  complete)
    [ "$result" = success ]
    [ "$ROLLBACK_ARMED" -eq 1 ]
    [ -f "$ROLLBACK_DIR/binary" ]
    [ -f "$ROLLBACK_DIR/unit" ]
    [ "$(stat -c %a "$ROLLBACK_DIR")" = 700 ]
    ;;
  *)
    [ "$result" = failed ]
    [ "$ROLLBACK_ARMED" -eq 0 ]
    [ -z "$ROLLBACK_DIR" ]
    [ -z "$(find {str(root)!r} -maxdepth 1 -name 'rollback.*' -print)" ]
    ;;
esac
"""
        env = dict(os.environ)
        env.update(MOCK_ROOT=str(root), MOCK_FAIL_ACTION="",
                   PATH=f"{root/'mockbin'}:{os.environ.get('PATH','/usr/bin:/bin')}")
        test = subprocess.run(["bash", "-c", script], capture_output=True, text=True,
                              env=env, timeout=12)
        assert test.returncode == 0, (
            prior_state, test.returncode, test.stdout, test.stderr)
        print(f"PASS previous-install snapshot guard={prior_state}")
print("PASS incomplete/symlinked previous runtime is never silently upgraded")

# Snapshot creation itself must be transactional: a partial disk copy may
# contain credentials/database fragments and must never remain in /tmp.
# Inject one copy failure at each stage before ROLLBACK_ARMED is set.
snapshot_hash = """#!/bin/sh
set -eu
# Force failure only at creation of the digest, not while verifying it.
[ "$MOCK_FAIL_SNAPSHOT_STAGE" != sha256 ] || exit 74
exec /usr/bin/sha256sum "$@"
"""

snapshot_cp = """#!/bin/sh
set -eu
case "$MOCK_FAIL_SNAPSHOT_STAGE" in
 binary)
   case "$*" in *"/binary") exit 73 ;; esac ;;
 unit)
   case "$*" in *"/unit") exit 73 ;; esac ;;
 config)
   case "$*" in *"/config") exit 73 ;; esac ;;
 helpers)
   case "$*" in *"/helpers") exit 73 ;; esac ;;
esac
exec /usr/bin/cp "$@"
"""

for failed_stage in ("binary", "unit", "config", "helpers", "sha256"):
    with tempfile.TemporaryDirectory(prefix="csqtt-snapshot-copy-fail-") as tmp:
        root = Path(tmp)
        for d in ("bin", "systemd", "config", "helpers", "mockbin"):
            (root/d).mkdir()
        binary = root/"bin/csqtt-core"
        unit = root/"systemd/csqtt-core.service"
        config = root/"config"
        helpers = root/"helpers"
        binary.write_text("ORIGINAL ELF\n")
        unit.write_text("ORIGINAL UNIT\n")
        (config/"csqtt-core.env").write_text("OLD_SECRET=snapshot-canary\n")
        (config/"csqtt.db").write_text("PERSISTENT DB\n")
        (helpers/"network-up.sh").write_text("original helper\n")
        (root/"active").write_text("yes")
        (root/"enabled").write_text("yes")
        for name, body in (("systemctl", systemctl), ("cp", snapshot_cp), ("sha256sum", snapshot_hash)):
            shim = root/"mockbin"/name
            shim.write_text(body)
            shim.chmod(0o755)
        scoped = production
        for old, new in (
            ("/usr/local/bin/csqtt-core", str(binary)),
            ("/etc/systemd/system/csqtt-core.service", str(unit)),
            ("/usr/local/lib/csqtt-core", str(helpers)),
            ("/tmp/csqtt-core-rollback.XXXXXXXX", str(root/"rollback.XXXXXXXX")),
        ):
            scoped = scoped.replace(old, new)
        sh = f"""
set -euo pipefail
DEPLOY_MODE=systemd
CSQTT_CONFIG_DIR={str(config)!r}
ROLLBACK_DIR=""
ROLLBACK_ARMED=0
ROLLBACK_WAS_ACTIVE=0
ROLLBACK_WAS_ENABLED=0
log_info() {{ :; }}
log_warn() {{ :; }}
log_error() {{ :; }}
{scoped}
if snapshot_existing_systemd; then
  echo "ERROR: failed snapshot was accepted" >&2
  exit 77
fi
[ "$ROLLBACK_ARMED" -eq 0 ]
[ -z "$ROLLBACK_DIR" ]
[ -z "$(find {str(root)!r} -maxdepth 1 -name 'rollback.*' -print)" ]
[ "$(cat {str(binary)!r})" = 'ORIGINAL ELF' ]
[ "$(cat {str(unit)!r})" = 'ORIGINAL UNIT' ]
[ "$(cat {str(config/'csqtt-core.env')!r})" = 'OLD_SECRET=snapshot-canary' ]
[ "$(cat {str(config/'csqtt.db')!r})" = 'PERSISTENT DB' ]
[ "$(cat {str(root/'active')!r})" = yes ]
[ "$(cat {str(root/'enabled')!r})" = yes ]
"""
        env = dict(os.environ)
        env.update(MOCK_ROOT=str(root), MOCK_FAIL_ACTION="",
                   MOCK_FAIL_SNAPSHOT_STAGE=failed_stage,
                   PATH=f"{root/'mockbin'}:{os.environ.get('PATH','/usr/bin:/bin')}")
        result = subprocess.run(["bash", "-c", sh], text=True,
                                capture_output=True, env=env, timeout=12)
        assert result.returncode == 0, (
            failed_stage, result.returncode, result.stdout, result.stderr)
        print(f"PASS snapshot copy failure stage={failed_stage} removes incomplete secret-bearing archive")
print("PASS failed snapshots leave no incomplete copies or runtime mutations")

# Snapshot corruption is different from an I/O error: a copy can succeed
# and then be modified before rollback. Fail before stopping a live service,
# keeping the private snapshot intact for deliberate recovery.
for tampered in ("binary", "unit"):
    with tempfile.TemporaryDirectory(prefix="csqtt-snapshot-tamper-") as tmp:
        root = Path(tmp)
        for d in ("bin", "systemd", "mockbin"):
            (root/d).mkdir()
        binary = root/"bin/csqtt-core"
        unit = root/"systemd/csqtt-core.service"
        binary.write_text("ORIGINAL ELF\n")
        unit.write_text("ORIGINAL UNIT\n")
        (root/"active").write_text("yes")
        (root/"enabled").write_text("yes")
        ctl = root/"mockbin/systemctl"
        ctl.write_text(systemctl)
        ctl.chmod(0o755)
        scoped = production
        for old,new in (
            ("/usr/local/bin/csqtt-core",str(binary)),
            ("/etc/systemd/system/csqtt-core.service",str(unit)),
            ("/usr/local/lib/csqtt-core",str(root/"helpers")),
            ("/tmp/csqtt-core-rollback.XXXXXXXX",str(root/"rollback.XXXXXXXX")),
        ):
            scoped=scoped.replace(old,new)
        script=f"""
set -euo pipefail
DEPLOY_MODE=systemd
CSQTT_CONFIG_DIR={str(root/'config')!r}
ROLLBACK_DIR=""
ROLLBACK_ARMED=0
ROLLBACK_WAS_ACTIVE=0
ROLLBACK_WAS_ENABLED=0
log_info() {{ :; }}
log_warn() {{ :; }}
log_error() {{ :; }}
{scoped}
snapshot_existing_systemd
[ "$ROLLBACK_ARMED" -eq 1 ]
[ -f "$ROLLBACK_DIR/SHA256SUMS" ]
# Corrupt the snapshot while the old service remains online.
echo CORRUPTED >> "$ROLLBACK_DIR/{tampered}"
if finish_deploy_rollback; then
    echo "ERROR: corrupted snapshot accepted" >&2
    exit 80
fi
[ "$ROLLBACK_ARMED" -eq 1 ]
[ -d "$ROLLBACK_DIR" ]
[ "$(stat -c %a "$ROLLBACK_DIR")" = 700 ]
[ "$(cat {str(binary)!r})" = 'ORIGINAL ELF' ]
[ "$(cat {str(unit)!r})" = 'ORIGINAL UNIT' ]
[ "$(cat {str(root/'active')!r})" = yes ]
[ "$(cat {str(root/'enabled')!r})" = yes ]
"""
        env=dict(os.environ)
        env.update(MOCK_ROOT=str(root),MOCK_FAIL_ACTION="",
                   PATH=f"{root/'mockbin'}:{os.environ.get('PATH','/usr/bin:/bin')}")
        res=subprocess.run(["bash","-c",script],capture_output=True,
                           text=True,env=env,timeout=15)
        assert res.returncode==0,(tampered,res.returncode,res.stdout,res.stderr)
        print(f"PASS corrupted prior {tampered} refused before stopping live service")
print("PASS tampered ELF and unit snapshots fail closed before cutover")
