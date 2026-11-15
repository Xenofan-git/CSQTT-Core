#!/usr/bin/env python3
"""Ensure Core's isolated tunnel address rewrite preserves hot-update test expectations."""
import argparse
import tempfile
import subprocess
import sys
from pathlib import Path
p = argparse.ArgumentParser()
p.add_argument('--upstream-root', type=Path, required=True)
a = p.parse_args()
with tempfile.TemporaryDirectory(prefix='csqtt-core-address-') as tmp:
    target = Path(tmp)
    subprocess.run([sys.executable, str(Path(__file__).with_name('derive-csqtt-core-rust-250.py')),
                    '--upstream-root', str(a.upstream_root), '--output', str(target)],
                   check=True, stdout=subprocess.DEVNULL)
    protocol = (target / 'rust-server/protocol.rs').read_text()
    assert 'Ipv4Addr::from([10, 67, 69, 42])' in protocol
    assert 'TUNCONF:10.67.69.42:8.8.8.8,8.8.4.4:0:stream-v1' in protocol
    assert '10.66.67.' not in protocol
    print('PASS isolated Core address rewrite and hot tunnel test fixture')
