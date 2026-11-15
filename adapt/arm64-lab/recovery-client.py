#!/usr/bin/env python3
"""Isolated TPROXY TCP/UDP test; classify in-flight phase boundaries separately."""
import concurrent.futures
import json
import os
from pathlib import Path
import socket
import time

root = Path(os.environ.get('CSQTT_LAB_DIR', '/tmp/csqtt-recovery'))
duration = int(os.environ.get('CSQTT_RECOVERY_DURATION', '160'))


def request(protocol, index):
    try:
        if protocol == 'tcp':
            with socket.create_connection(('198.18.0.1', 5555), timeout=1.4) as sock:
                sock.settimeout(1.4)
                data = f'REC_TCP_{index}'.encode()
                sock.sendall(data)
                return sock.recv(4096) == data
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.settimeout(1.4)
            data = f'REC_UDP_{index}'.encode()
            sock.sendto(data, ('198.18.0.1', 5555))
            return sock.recvfrom(4096)[0] == data
    except OSError:
        return False


start = time.monotonic()
counts = {}
iteration = 0
with concurrent.futures.ThreadPoolExecutor(max_workers=16) as executor:
    while time.monotonic() - start < duration:
        start_phase = (root / 'phase').read_text().strip()
        tasks = [executor.submit(request, 'tcp', iteration * 8 + j) for j in range(8)]
        tasks += [executor.submit(request, 'udp', iteration * 8 + j) for j in range(8)]
        outcomes = [task.result() for task in tasks]
        end_phase = (root / 'phase').read_text().strip()
        phase = start_phase if start_phase == end_phase else 'transition'
        item = counts.setdefault(phase, {'tcp_ok': 0, 'tcp_fail': 0, 'udp_ok': 0, 'udp_fail': 0})
        for protocol, results in [('tcp', outcomes[:8]), ('udp', outcomes[8:])]:
            item[f'{protocol}_ok'] += sum(results)
            item[f'{protocol}_fail'] += len(results) - sum(results)
        iteration += 1
        if iteration % 10 == 0:
            print('RECOVERY_PROGRESS', round(time.monotonic() - start, 1), counts, flush=True)
        time.sleep(0.2)
print('RECOVERY_RESULT', counts, flush=True)
(root / 'result.json').write_text(json.dumps(counts, indent=2, sort_keys=True))
before = counts.get('before', {})
recovered = [counts[key] for key in sorted(counts) if key.startswith('after_')]
assert before.get('tcp_ok', 0) >= 40 and before.get('udp_ok', 0) >= 40, 'baseline missing'
assert before.get('tcp_fail', 0) == 0 and before.get('udp_fail', 0) == 0, 'baseline loss'
assert len(recovered) == int(os.environ.get('CSQTT_OUTAGE_CYCLES', '2')), 'missing recovery cycle'
for index, phase in enumerate(recovered, 1):
    assert phase.get('tcp_ok', 0) >= 40 and phase.get('udp_ok', 0) >= 40, f'recovery {index} missing'
    assert phase.get('tcp_fail', 0) == 0 and phase.get('udp_fail', 0) == 0, f'recovery {index} lost requests'
