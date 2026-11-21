#!/usr/bin/env python3
"""Guard Core's own 10.67.69/24 route index, TUN and owner IP after derivation."""
import argparse
from pathlib import Path
import re

parser = argparse.ArgumentParser()
parser.add_argument("--source", required=True, type=Path)
args = parser.parse_args()
source = args.source.resolve(strict=True)
files = {name: (source / name).read_text() for name in ("tun_device.rs", "net_setup.rs", "model.rs", "protocol.rs")}
tun = files["tun_device.rs"]
expected_prefix = (10, 67, 69)
match = re.search(r"SUBNET_PREFIX:\s*\[u8;\s*3\]\s*=\s*\[(\d+),\s*(\d+),\s*(\d+)\]", tun)
assert match, "RouteTable subnet prefix not found"
actual = tuple(map(int, match.groups()))
assert actual == expected_prefix, f"BUG: RouteTable downlink prefix {actual} differs from Core TUN /24 {expected_prefix}"
assert 'TUN_SUBNET: &str = "10.67.69.0/24"' in tun
assert 'TUN_IFACE: &str = "csqtt-core0"' in tun
assert 'TUN_ADDR: &str = "10.67.69.1"' in files["net_setup.rs"]
assert 'OWNER_TUNNEL_IP: &str = "10.67.69.2"' in files["model.rs"]
assert '[10, 66, 67,' not in tun
assert 'let ip = [10, 67, 69, 10];' in files["protocol.rs"]
assert '[10, 67, 69, last]' in tun, "Core route unit-test data still uses original subnet"
assert 'route_index(ip: [u8; 4])' in tun
print("CORE_TUN_ROUTE_INDEX_SUBNET_PARITY_PASS: 10.67.69/24, 10.67.69.1, 10.67.69.2")
