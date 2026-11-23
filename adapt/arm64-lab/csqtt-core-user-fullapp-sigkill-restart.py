#!/usr/bin/env python3
"""True guest user-systemd Go process SIGKILL -> restart -> real SOCKS5 TCP.

No root operations, credentials, TUN, VK TURN, production routes or external
traffic. This is a user-session restart test, NOT a pre-login OS boot test.
"""
import hashlib
import os
from pathlib import Path
import platform
import re
import signal
import socket
import subprocess
import sys
import time

UNIT = "csqtt-core-user-fullapp-autostart-test.service"
MAIN = "/home/csqttlab/powerloss/headless-autostart-arm64"
HEALTH = "/home/csqttlab/powerloss/fullapp-noauth-health.py"
CONFIG = Path("/home/csqttlab/powerloss/config.json")
PANEL = Path("/home/csqttlab/powerloss/config.json.panel.json")
DB = Path("/home/csqttlab/.la-lune/configs.db")


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def run(*args, timeout=12):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, text=True, timeout=timeout).stdout.strip()


def show(field):
    return run("systemctl", "--user", "show", UNIT,
               "-p", field, "--value")


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def routes(family):
    text = run("ip", family, "route", "show", "default")
    return re.sub(r" expires \d+sec", "", text)


def live_main():
    value = show("MainPID")
    if not value.isdecimal() or int(value) <= 0:
        return 0
    return int(value)


def verify_health():
    output = run("python3", "-B", HEALTH, timeout=90)
    check("CSQTT_FULLAPP_QEMU_ARM64_USER_SYSTEMD_GO_AND_SINGBOX_REAL_TCP_PASS" in output,
          "actual Sing-box SOCKS5 loopback TCP was not proven")
    check("CSQTT_FULLAPP_QEMU_RUST_CLIENT_AND_CORE8_HASH_VERIFIED_NO_VK_CONNECT" in output,
          "unmodified Rust client/Core8 hashes were not verified")


def guard():
    check(platform.machine() == "aarch64", "must run on true guest ARM64")
    check(socket.gethostname() == "csqtt-core8-isolated", "unexpected host")
    check(os.geteuid() != 0, "must not run as root")
    check(show("FragmentPath") ==
          "/home/csqttlab/.config/systemd/user/" + UNIT,
          "unexpected systemd unit path")
    check(show("ActiveState") == "inactive", "refuse to interrupt already active application")
    check(not Path("/home/csqttlab/powerloss/fullapp-auth.env").exists(),
          "this is a no-auth experiment")
    check(not Path("/home/csqttlab/powerloss/config.json.live-apply.json").exists(),
          "unfinished transaction")
    check(not Path("/home/csqttlab/powerloss/config.json.transaction.json").exists(),
          "unfinished nested transaction")


def main():
    guard()
    old_boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
    old_route4, old_route6 = routes("-4"), routes("-6")
    original_config, original_state, original_db = map(digest, (CONFIG, PANEL, DB))
    try:
        run("systemctl", "--user", "start", UNIT, timeout=90)
        verify_health()
        first = live_main()
        check(first > 0, "systemd did not start the Go controller")
        cmdline = Path(f"/proc/{first}/cmdline").read_bytes().split(b"\0")[0].decode()
        check(cmdline == MAIN, "refuse to SIGKILL an unrelated PID")
        print("CSQTT_FULLAPP_GUEST_USER_SYSTEMD_PRECRASH_PID", first, flush=True)
        run("systemctl", "--user", "kill", "--kill-who=main",
            "-s", "SIGKILL", UNIT)
        deadline = time.monotonic() + 90
        next_pid = 0
        while time.monotonic() < deadline:
            next_pid = live_main()
            if next_pid != first and next_pid > 0 and show("ActiveState") == "active":
                break
            time.sleep(2)
        check(next_pid not in (0, first), "systemd did not restart after real Go SIGKILL")
        verify_health()
        check(int(show("NRestarts")) >= 1, "systemd did not account for restart")
        check(not Path(f"/proc/{first}").exists(), "old killed Go PID still exists")
        check((original_config, original_state, original_db) ==
              tuple(map(digest, (CONFIG, PANEL, DB))),
              "config, panel or SQLite DB changed after controller death")
        check((old_route4, old_route6) == (routes("-4"), routes("-6")),
              "guest default routes changed during restart")
        check(Path("/proc/sys/kernel/random/boot_id").read_text().strip() == old_boot,
              "unexpected whole guest reboot instead of controlled failure")
        print("CSQTT_FULLAPP_GUEST_USER_SYSTEMD_POSTCRASH_PID", next_pid, flush=True)
        print("CSQTT_FULLAPP_REAL_ARM64_USER_SYSTEMD_GO_SIGKILL_AUTO_RESTART_SOCKS5_TCP_SQLITE_PASS",
              flush=True)
    finally:
        subprocess.run(["systemctl", "--user", "stop", UNIT],
                       timeout=40, check=False)
        # A stopped fixture should never be enabled for boot without consent.
        assert show("ActiveState") == "inactive"
        print("CSQTT_FULLAPP_GUEST_TEST_UNIT_STOPPED", flush=True)


if __name__ == "__main__":
    main()
