#!/usr/bin/env python3
"""Probe mock relay UDP echo socket, not merely its SOCKS5 TCP listener."""
import socket
import sys
import time

port = int(sys.argv[1]) if len(sys.argv) > 1 else 47995
for attempt in range(20):
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.settimeout(0.2)
            payload = f'udp-ready-{attempt}'.encode()
            sock.sendto(payload, ('127.0.0.1', port))
            if sock.recvfrom(1024)[0] == payload:
                print(f'UDP_RELAY_READY attempt={attempt}', flush=True)
                sys.exit(0)
    except OSError:
        time.sleep(0.1)
raise SystemExit('UDP_RELAY_NOT_READY')
