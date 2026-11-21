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
runner=base/'app_linux.go'
s=runner.read_text()
old='''if len(dnsServers) > 0 {'''
assert s.count(old)==1
s=s.replace(old,'''if len(dnsServers) > 0 && os.Getenv("CSQTT_TEST_TURN_RELAY_PORT")=="" {''',1)
# The isolated GitHub runner does not have dnsmasq installed. Direct
# integration is already independently verified; this one-chain fixture
# explicitly checks a no-default-route negative control and never enables
# Direct domains. Skip only dnsmasq state management in THIS test binary.
old='if err := t.applyDirectRoutingLocked(); err != nil {\n\t\tt.app.core.AddLog(fmt.Sprintf("[DIRECT] Ошибка применения: %v; откат TUN", err))\n        rollback()\n        return fmt.Errorf("apply Direct routing: %w", err)\n\t}'
assert s.count(old)==1, "upstream Go TUN Direct setup changed"
s=s.replace(old,'if os.Getenv("CSQTT_TEST_TURN_RELAY_PORT") == "" {\n        if err := t.applyDirectRoutingLocked(); err != nil {\n            t.app.core.AddLog(fmt.Sprintf("[DIRECT] Ошибка применения: %v; откат TUN", err))\n            rollback()\n            return fmt.Errorf("apply Direct routing: %w", err)\n        }\n    }',1)
# Enable the existing Go SOCKS5 diagnostic hook only inside the
# disposable ARM64 test fixture. No product changes.
needle = 'app.socksLocal = NewSocks5Server("127.0.0.1:12500")'
assert s.count(needle) == 1, "upstream Go NewApp loopback listener changed"
addition = '''
    if os.Getenv("CSQTT_TEST_TURN_RELAY_PORT") != "" {
        app.socksLocal.tracef = func(format string, args ...any) {
            app.core.AddLog("[TEST CSQTT SOCKS5] " + fmt.Sprintf(format,args...))
        }
    }
'''
s=s.replace(needle,needle+addition,1)
runner.write_text(s)
print('TEST_ONLY_GO_SOCKS5_TRACE_ENABLED')
print('TEST_ONLY_DIRECT_DNSMASQ_SKIPPED_ON_DISPOSABLE_ARM64_WITHOUT_DNSMASQ')
print('TEST_ONLY_NO_RESOLVED_DBUS_SIDE_EFFECT_IN_ISOLATED_NETNS')
print('TEST_ONLY_GO_SHA256_PINNED_TO_SYNTHETIC_TURN_FIXTURE',sha)
