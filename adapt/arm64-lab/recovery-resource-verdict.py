#!/usr/bin/env python3
"""Assert that TPROXY FD count returns to idle baseline during the quiet period."""
import datetime as dt
import json
from pathlib import Path
import re
import sys

log, quiet_start, output = map(Path, sys.argv[1:4])
quiet = dt.datetime.fromisoformat(quiet_start.read_text().strip().replace('Z', '+00:00'))
records = []
for line in log.read_text().splitlines():
    match = re.fullmatch(r'(\S+) rss_kb=(\d+) fds=(\d+)', line)
    if match:
        timestamp = dt.datetime.fromisoformat(match[1].replace('Z', '+00:00'))
        records.append((timestamp, int(match[2]), int(match[3])))
post = [(timestamp, rss, fds) for timestamp, rss, fds in records if timestamp >= quiet + dt.timedelta(seconds=65)]
result = {
    'sample_count': len(records),
    'quiet_samples_after_65s': len(post),
    'peak_observed_fd': max((row[2] for row in records), default=None),
    'peak_observed_rss_kb': max((row[1] for row in records), default=None),
    'last_observed_fd': records[-1][2] if records else None,
    'quiet_fd_values': [row[2] for row in post],
}
output.write_text(json.dumps(result, indent=2, sort_keys=True))
print('RESOURCE_VERDICT', result, flush=True)
assert len(post) >= 3, 'insufficient samples beyond UDP idle timeout'
assert max(row[2] for row in post) <= 64, 'FDs did not return to expected idle range'
assert max(row[1] for row in post) <= 128 * 1024, 'RSS exceeded 128 MiB budget'
