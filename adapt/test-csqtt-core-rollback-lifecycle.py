#!/usr/bin/env python3
"""Run production rollback functions with fake systemctl/ip in a temporary tree."""
from pathlib import Path
import os
import subprocess
import tempfile

source = Path("server-assets/deploy-csqtt-core-250.sh").read_text()
start = source.index("snapshot_existing_systemd() {")
end = source.index("\ncleanup_rollback_snapshot() {", start)
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
        fail_choices = ("", "enable" if originally_enabled else "disable")
        for fail in fail_choices:
            cases += 1
            with tempfile.TemporaryDirectory(prefix="csqtt-rollback-mock-") as tmp:
                root = Path(tmp)
                for sub in ("bin", "systemd", "config", "helpers", "mockbin"):
                    (root/sub).mkdir()
                binary = root/"bin/csqtt-core"
                unit = root/"systemd/csqtt-core.service"
                config = root/"config"
                helpers = root/"helpers"
                binary.write_text("old verified binary\n")
                unit.write_text("ExecStart=old\n")
                (config/"csqtt-core.env").write_text("OLD_ENV=ok\n")
                (config/"csqtt.db").write_text("live SQLite data")
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
{functions}
snapshot_existing_systemd
[ "$ROLLBACK_WAS_ACTIVE" -eq {int(originally_active)} ]
[ "$ROLLBACK_WAS_ENABLED" -eq {int(originally_enabled)} ]
# Simulate a successful cutover stop/disable without consuming the fault
# injection reserved for the subsequent rollback action.
echo no > {str(root/'active')!r}
echo no > {str(root/'enabled')!r}
rm -f {str(binary)!r} {str(unit)!r}
echo NEW_ENV=bad > {str(config/'csqtt-core.env')!r}
echo new > {str(helpers/'network-up.sh')!r}
if restore_previous_systemd; then result=success; else result=failed; fi
if [ -z "$MOCK_FAIL_ACTION" ]; then
  [ "$result" = success ]
  [ "$(cat {str(root/'enabled')!r})" = {'yes' if originally_enabled else 'no'} ]
  [ "$(cat {str(root/'active')!r})" = {'yes' if originally_active else 'no'} ]
  [ "$(cat {str(binary)!r})" = 'old verified binary' ]
  [ "$(cat {str(unit)!r})" = 'ExecStart=old' ]
  [ "$(cat {str(config/'csqtt-core.env')!r})" = 'OLD_ENV=ok' ]
  [ "$(cat {str(helpers/'network-up.sh')!r})" = 'echo old' ]
else
  [ "$result" = failed ]
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
