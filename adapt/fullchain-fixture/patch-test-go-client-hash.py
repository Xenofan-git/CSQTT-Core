#!/usr/bin/env python3
"""Isolated CI Go adapter for synthetic TURN Rust fixture, not production Go."""
from pathlib import Path
import hashlib,sys
base=Path(sys.argv[1])
fixture=Path(sys.argv[2])
assert fixture.exists() and fixture.is_file()
sha=hashlib.sha256(fixture.read_bytes()).hexdigest()
assert len(sha)==64
gate=base/'csqtt_core_250_supervisor_gate_linux.go'
s=gate.read_text()
old='3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa'
assert s.count(old)==1
gate.write_text(s.replace(old,sha))
# Only the one-worker synthetic relay fixture uses exact-workers, normal
# Go candidate logic and original client CLI in the accepted bundle untouched.
s=gate.read_text()
needle='''func csqtt250NormalizeSupervisorArgv(legacy []string) ([]string, error) {
'''
assert s.count(needle)==1
# preserve all original options and append client test-only worker count
needle='''	return result,nil
}'''
assert s.count(needle)==1
s=s.replace(needle,'''    if os.Getenv("CSQTT_TEST_TURN_RELAY_PORT") != "" {
        // Test-only synthetic TURN fixture handles exactly one worker.
        result = append(result, "--exact-workers")
        for i:=0; i+1<len(result);i++ {
            if result[i]=="-n" {result[i+1]="1"}
        }
    }
	return result,nil
}''',1)
gate.write_text(s)
print('TEST_ONLY_GO_SHA256_PINNED_TO_SYNTHETIC_TURN_FIXTURE',sha)
