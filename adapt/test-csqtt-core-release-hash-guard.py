#!/usr/bin/env python3
"""Static contract: installer refuses unverified release payload before cutover."""
from pathlib import Path
s=Path('server-assets/deploy-csqtt-core-250.sh').read_text()
start=s.index('prepare_uploaded_release() {')
end=s.index('\nfinish_deployment() {',start)
body=s[start:end]
for term in ('CSQTT_CORE_VERIFIED_BINARY_SHA256','sha256sum "$UPLOAD_BINARY"','Verified CSQTT binary SHA-256 mismatch','64 hex'):
    assert term in body,term
assert body.index('sha256sum "$UPLOAD_BINARY"') < body.index('chmod 0755 "$UPLOAD_BINARY"')
assert s.index('run_timed "проверка upload" prepare_uploaded_release') < s.index('run_timed "переключение runtime" csqtt_cleanup')
print('PASS verified binary SHA-256 fail-closed preflight before runtime cutover')
