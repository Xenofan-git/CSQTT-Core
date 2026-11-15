#!/usr/bin/env python3
"""Real TCP+UDP client for the isolated TPROXY recovery test."""
import concurrent.futures
import os
from pathlib import Path
import socket
import time

root = Path(os.environ.get('CSQTT_LAB_DIR', '/tmp/csqtt-recovery'))
duration = int(os.environ.get('CSQTT_RECOVERY_DURATION', '160'))

def tcp(i):
    try:
        with socket.create_connection(('198.18.0.1', 5555), timeout=1.4) as s:
            s.settimeout(1.4)
            data = f'REC_TCP_{i}'.encode()
            s.sendall(data)
            return s.recv(4096) == data
    except OSError:
        return False

def udp(i):
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            s.settimeout(1.4)
            data = f'REC_UDP_{i}'.encode()
            s.sendto(data, ('198.18.0.1', 5555))
            return s.recvfrom(4096)[0] == data
    except OSError:
        return False

start = time.monotonic()
phases = {}
i = 0
with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
    while time.monotonic() - start < duration:
        phase = (root / 'phase').read_text().strip()
        tasks = [pool.submit(tcp, i*8+j) for j in range(8)] + [pool.submit(udp, i*8+j) for j in range(8)]
        results = [f.result() for f in tasks]
        counts = phases.setdefault(phase, [0, 0, 0, 0])
        counts[0] += sum(results[:8]); counts[1] += 8 - sum(results[:8])
        counts[2] += sum(results[8:]); counts[3] += 8 - sum(results[8:])
        i += 1
        if i % 10 == 0:
            print('RECOVERY_PROGRESS', round(time.monotonic()-start, 1), phase, phases, flush=True)
        time.sleep(0.2)
print('RECOVERY_RESULT', phases, flush=True)
(root / 'result').write_text(repr(phases))
before = phases.get('before', [0]*4)
after = phases.get('after', [0]*4)
assert before[0] >= 40 and before[2] >= 40, 'baseline missing'
assert after[0] >= 40 and after[2] >= 40, 'no recovery'
assert after[1] == 0 and after[3] == 0, 'post-recovery loss'
