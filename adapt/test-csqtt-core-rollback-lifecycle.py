#!/usr/bin/env python3
"""Run production rollback functions with fake systemctl/ip in a temporary tree."""
from pathlib import Path
import os
import subprocess
import tempfile

source = Path("server-assets/deploy-csqtt-core-250.sh").read_text()
start = source.index("snapshot_existing_systemd() {")
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
        echo yes > "$MOCK_ROOT/active"; fi ;;
 *) echo "unexpected systemctl $1" >&2; exit 43 ;;
esac
"""
ip = """#!/bin/sh
set -eu
[ "$1" = link ] && [ "$2" = show ] && [ "$3" = csqtt-core0 ] &&
[ "$(cat "$MOCK_ROOT/active")" = yes ]
"""

cases = 0
for originally_active in (False, True):
    for originally_enabled in (False, True):
        fail_choices = ("", "enable" if originally_enabled else "disable") + (("restart",) if originally_active else ())
        for fail in fail_choices:
            cases += 1
            with tempfile.TemporaryDirectory(prefix="csqtt-rollback-mock-") as tmp:
                root = Path(tmp)
                for sub in ("bin", "systemd", "helpers", "mockbin"):
                    (root/sub).mkdir()
                binary = root/"bin/csqtt-core"
                unit = root/"systemd/csqtt-core.service"
                config = root/"config"
                helpers = root/"helpers"
                binary.write_text("old verified binary\n")
                unit.write_text("ExecStart=old\n")
                # Previously disabled old units may have no config directory
                # at all. Candidate-owned files still must not survive rollback.
                if originally_enabled:
                    config.mkdir()
                    (config/"csqtt-core.env").write_text("OLD_ENV=ok\n")
                    (config/"deploy-overrides.json").write_text("OLD_OVERRIDE\n")
                    (config/"web_cert.pem").write_text("OLD_CERT\n")
                    (config/"web_key.pem").write_text("OLD_KEY\n")
                    (config/"letsencrypt-ip.env").write_text("OLD_LE\n")
                    (config/"csqtt.db").write_text("live SQLite data")
                    (config/"unrelated.txt").write_text("keep unrelated state\n")
                (helpers/"network-up.sh").write_text("echo old\n")
                (root/"active").write_text("yes" if originally_active else "no")
                (root/"enabled").write_text("yes" if originally_enabled else "no")
                for name, contents in (("systemctl", systemctl), ("ip", ip)):
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
echo new > {str(helpers/'network-up.sh')!r}
if finish_deploy_rollback; then result=success; else result=failed; fi
if [ -z "$MOCK_FAIL_ACTION" ]; then
  [ "$result" = success ]
  [ "$ROLLBACK_ARMED" -eq 0 ]
  [ -z "$ROLLBACK_DIR" ]
  [ -z "$(find {str(root)!r} -maxdepth 1 -name 'rollback.*' -print)" ]
  [ "$(cat {str(root/'enabled')!r})" = {'yes' if originally_enabled else 'no'} ]
  [ "$(cat {str(root/'active')!r})" = {'yes' if originally_active else 'no'} ]
  [ "$(cat {str(binary)!r})" = 'old verified binary' ]
  [ "$(cat {str(unit)!r})" = 'ExecStart=old' ]
  if [ {int(originally_enabled)} -eq 1 ]; then
    [ "$(cat {str(config/'csqtt-core.env')!r})" = 'OLD_ENV=ok' ]
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
  [ "$(cat {str(helpers/'network-up.sh')!r})" = 'echo old' ]
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
  if [ {int(originally_enabled)} -eq 1 ]; then
    [ "$(cat {str(config/'csqtt-core.env')!r})" = 'OLD_ENV=ok' ]
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
  [ "$(cat {str(helpers/'network-up.sh')!r})" = 'echo old' ]
fi
[ "$(cat {str(config/'csqtt.db')!r})" = 'live SQLite data' ]
"""
                env = dict(os.environ)
                env.update(MOCK_ROOT=str(root), MOCK_FAIL_ACTION=fail,
                           PATH=f"{root/'mockbin'}:{os.environ.get('PATH','/usr/bin:/bin')}")
                result = subprocess.run(["bash", "-c", sh], capture_output=True,
                                        text=True, env=env, timeout=15)
                assert result.returncode == 0, (
                    originally_active, originally_enabled, fail,
                    result.returncode, result.stdout, result.stderr
                )
                print(f"PASS active={originally_active} enabled={originally_enabled} fail={fail or 'none'}")
print(f"PASS {cases} isolated rollback behavior scenarios; host unchanged")
