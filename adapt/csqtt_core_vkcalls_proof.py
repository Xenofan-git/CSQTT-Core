#!/usr/bin/env python3
"""Derive only non-secret VK Calls proof markers from a PRIVATE original ELF log.

No network, no token/URL extraction, no persistence, no direct-output of
source lines. This utility is preparation for a future separately-authorized
real external TURN E2E; it is NOT a real TURN test.
"""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import stat
import sys

MAX_BYTES = 2 * 1024 * 1024
LINE = re.compile(
    r"^\[STREAM ([0-9]{1,5})\] \[VKCalls\] "
    r"(step[1234] OK(?:,.*?)?|SUCCESS, TURN urls=([0-9]{1,3}))$"
)
STEPS = {
    "step1": "step1 OK, anonymous_token",
    "step2": "step2 OK, user_id=",
    "step3": "step3 OK, OK anonymToken",
    "step4": "step4 OK, OK session_key",
}
EXPECTED = tuple(STEPS) + ("TURN",)


def proof_status(log: str) -> tuple[str, ...]:
    """Never return log text, arbitrary VK fields, URLs, tokens, or identities."""
    positions: dict[int, int] = {}
    counts: dict[int, int] = {}
    for line in log.splitlines():
        # Explicit opt-in to structured Rust STREAM lines. Ignore all other
        # messages (including errors that may embed URLs or credential data).
        match = LINE.fullmatch(line)
        if match is None:
            continue
        stream = int(match.group(1))
        event = match.group(2)
        current = positions.get(stream, 0)
        if current >= len(EXPECTED):
            continue
        step = EXPECTED[current]
        if step == "TURN":
            # A mere "SUCCESS" with zero TURN URLs is not a credential proof.
            if event.startswith("SUCCESS, TURN urls=") and int(match.group(3)) > 0:
                positions[stream] = len(EXPECTED)
                counts[stream] = int(match.group(3))
        elif event.startswith(STEPS[step]):
            positions[stream] = current + 1
    complete = [s for s, n in positions.items() if n == len(EXPECTED)]
    if complete:
        # Do not expose number of streams, addresses, server hostnames or
        # arbitrary error descriptions. Only allowlisted labels.
        return ("VK_CALLS_STEPS_1_TO_4_CONFIRMED", "VK_CALLS_TURN_URLS_PRESENT")
    partial = max(positions.values(), default=0)
    return tuple(f"VK_CALLS_{step.upper()}_CONFIRMED" for step in EXPECTED[:min(partial, 4)])


def load_private_log(path: Path) -> str:
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode):
        raise ValueError("private regular log required")
    if info.st_mode & 0o077:
        raise ValueError("private log permissions required")
    if info.st_size > MAX_BYTES:
        raise ValueError("log exceeds bounded read size")
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(path, flags)
    with os.fdopen(fd, "rb") as file:
        check = os.fstat(file.fileno())
        if (not stat.S_ISREG(check.st_mode) or check.st_mode & 0o077
                or check.st_size > MAX_BYTES):
            raise ValueError("private regular log required")
        data = file.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES:
        raise ValueError("log exceeds bounded read size")
    return data.decode("utf-8", errors="replace")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("private_log", type=Path)
    args = parser.parse_args()
    try:
        result = proof_status(load_private_log(args.private_log))
    except (OSError, ValueError):
        print("VK_CALLS_PROOF_LOG_REJECTED")
        return 2
    for stage in result:
        print(stage)
    if "VK_CALLS_TURN_URLS_PRESENT" not in result:
        print("VK_CALLS_REAL_TURN_CREDENTIAL_PROOF_NOT_ESTABLISHED")
        return 1
    print("VK_CALLS_CREDENTIAL_LOG_STAGE_PASS_ONLY_NOT_WIRE3_DATAPLANE")
    return 0


if __name__ == "__main__":
    sys.exit(main())
