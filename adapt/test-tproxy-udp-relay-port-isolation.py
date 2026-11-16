#!/usr/bin/env python3
"""Static release-source guard: SOCKS5 UDP sockets must not reuse TPROXY port."""
from pathlib import Path
import argparse

parser = argparse.ArgumentParser()
parser.add_argument('--source', required=True, type=Path, help='actual generated Rust source to compile')
args = parser.parse_args()
src = args.source.read_text()
listener = src.index('impl UdpTproxy {')
relay = src.index('async fn create_association(')
end = src.index('fn spawn_refill(', relay)
listener_source = src[listener:relay]
relay_source = src[relay:end]
assert 'enable SO_REUSEADDR on transparent UDP socket' in listener_source
assert 'enable SO_REUSEADDR on SOCKS5 UDP relay socket' not in relay_source
assert '.set_reuse_address(true)' not in relay_source
assert 'bind SOCKS5 UDP relay socket' in relay_source
assert 'Keep SO_REUSEADDR disabled on the ephemeral relay socket.' in relay_source
print('PASS TPROXY UDP listener reuse retained; ephemeral SOCKS5 UDP relay reuse disabled')
