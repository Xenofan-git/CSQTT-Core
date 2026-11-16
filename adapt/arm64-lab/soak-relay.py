#!/usr/bin/env python3
"""Disposable ARM64 lab SOCKS5 TCP/UDP echo relay with packet telemetry."""
import socket
import threading
import time

UDP_PORT = 47995
TCP_PORT = 47996
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
u.bind(('127.0.0.1', UDP_PORT))
u.settimeout(1)
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', TCP_PORT))
s.listen(80)
s.settimeout(1)
counts = {'udp_recv': 0, 'udp_sent': 0, 'udp_send_error': 0,
          'tcp_accept': 0, 'tcp_probe_close': 0, 'socks_udp_associate': 0,
          'socks_tcp_connect': 0, 'socks_invalid': 0}
lock = threading.Lock()

def inc(name):
    with lock:
        counts[name] += 1

def udp():
    while True:
        try:
            data, addr = u.recvfrom(4096)
        except socket.timeout:
            continue
        inc('udp_recv')
        # SOCKS5 UDP header is 10 bytes for IPv4; the client payload follows.
        marker = data[10:].decode('ascii', errors='replace') if len(data) >= 10 else '<short>'
        if not marker.startswith('REC_UDP_'):
            marker = '<other>'
        print(f'RELAY_PACKET epoch_ns={time.time_ns()} event=RECV marker={marker}', flush=True)
        try:
            u.sendto(data, addr)
            inc('udp_sent')
            print(f'RELAY_PACKET epoch_ns={time.time_ns()} event=SEND marker={marker}', flush=True)
        except OSError:
            inc('udp_send_error')

def read_exact(c, size):
    result = bytearray()
    while len(result) < size:
        part = c.recv(size - len(result))
        if not part:
            return None
        result.extend(part)
    return bytes(result)

def handle(c):
    try:
        c.settimeout(6)
        greeting = read_exact(c, 3)
        if greeting is None:
            inc('tcp_probe_close')
            return
        if greeting != b'\x05\x01\x00':
            inc('socks_invalid')
            return
        c.sendall(b'\x05\x00')
        req = read_exact(c, 10)
        if req is None or req[:4] not in (b'\x05\x03\x00\x01', b'\x05\x01\x00\x01'):
            inc('socks_invalid')
            return
        if req[1] == 3:
            inc('socks_udp_associate')
            c.sendall(b'\x05\x00\x00\x01' + socket.inet_aton('127.0.0.1') + UDP_PORT.to_bytes(2, 'big'))
            while c.recv(1):
                pass
        else:
            inc('socks_tcp_connect')
            c.sendall(b'\x05\x00\x00\x01' + socket.inet_aton('127.0.0.1') + b'\x00\x00')
            while True:
                data = c.recv(4096)
                if not data:
                    break
                c.sendall(data)
    except (OSError, TimeoutError):
        pass
    finally:
        c.close()

def metrics():
    while True:
        time.sleep(1)
        with lock:
            print('RELAY_SAMPLE epoch_ms={} {}'.format(int(time.time() * 1000), ' '.join(f'{k}={v}' for k, v in sorted(counts.items()))), flush=True)

threading.Thread(target=udp, daemon=True).start()
threading.Thread(target=metrics, daemon=True).start()
print('MIXED_RELAY_READY', flush=True)
while True:
    try:
        c, _ = s.accept()
    except socket.timeout:
        continue
    inc('tcp_accept')
    threading.Thread(target=handle, args=(c,), daemon=True).start()
