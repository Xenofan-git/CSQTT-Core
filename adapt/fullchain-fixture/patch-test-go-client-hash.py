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
# Add bounded, metadata-only trace to the ACTUAL Go TUN<->UDP bridge.
# This is inserted only when compiling the disposable ARM64 lab binary.
# Never log raw packet payload or production credentials.
needle = """	go func() {
		defer t.workers.Done()
		buf := make([]byte, 65535)
		for {
			n, readErr := f.Read(buf)
			if readErr != nil { return }
			if n > 0 {
				if _, writeErr := udpConn.Write(buf[:n]); writeErr != nil { return }
			}
		}
	}()"""
assert s.count(needle)==1, "Go TUN outbound bridge changed"
new = """	go func() {
		defer t.workers.Done()
		buf := make([]byte, 65535)
		count := 0
		for {
			n, readErr := f.Read(buf)
			if readErr != nil {
				t.app.core.AddLog(fmt.Sprintf("[TEST TUN->UDP] read exited: %v",readErr))
				return
			}
			if n > 0 {
				count++
				if count <= 12 {
					t.app.core.AddLog(fmt.Sprintf("[TEST TUN->UDP] count=%d bytes=%d ipv=%d", count,n,buf[0]>>4))
				}
				if _, writeErr := udpConn.Write(buf[:n]); writeErr != nil {
					t.app.core.AddLog(fmt.Sprintf("[TEST TUN->UDP] udp send failed: %v",writeErr))
					return
				}
			}
		}
	}()"""
s=s.replace(needle,new,1)
needle="""	go func() {
		defer t.workers.Done()
		buf := make([]byte, 65535)
		for {
			n, readErr := udpConn.Read(buf)
			if readErr != nil { return }
			if n > 0 {
				if _, writeErr := f.Write(buf[:n]); writeErr != nil { return }
			}
		}
	}()"""
assert s.count(needle)==1, "Go TUN inbound bridge changed"
new="""	go func() {
		defer t.workers.Done()
		buf := make([]byte, 65535)
		count := 0
		for {
			n, readErr := udpConn.Read(buf)
			if readErr != nil {
				t.app.core.AddLog(fmt.Sprintf("[TEST UDP->TUN] udp read exited: %v",readErr))
				return
			}
			if n > 0 {
				count++
				if count <= 12 {
					t.app.core.AddLog(fmt.Sprintf("[TEST UDP->TUN] count=%d bytes=%d ipv=%d", count,n,buf[0]>>4))
				}
				if _, writeErr := f.Write(buf[:n]); writeErr != nil {
					t.app.core.AddLog(fmt.Sprintf("[TEST UDP->TUN] write failed: %v",writeErr))
					return
				}
			}
		}
	}()"""
s=s.replace(needle,new,1)

# ONLY isolated ARM64 fixture: os.File.Read(/dev/net/tun) exits "not pollable".
# Raw nonblocking unix.Read() plus unix.Poll() tests the smallest plausible fix.
needle = "\tt.mu.Lock()\n\tt.tunFile, t.udpConn = f, udpConn"
assert s.count(needle) == 1, "TUN fd install source marker changed"
s=s.replace(needle, """\ttunFD := int(f.Fd())
\tif err := unix.SetNonblock(tunFD, true); err != nil {
\t\t_ = f.Close()
\t\treturn fmt.Errorf("nonblocking test TUN fd: %w", err)
\t}
""" + needle, 1)
needle = "\t\t\tn, readErr := f.Read(buf)\n"
assert s.count(needle) == 1, "Go TUN os.File.Read marker changed"
s=s.replace(needle, """\t\t\tn, readErr := unix.Read(tunFD, buf)
\t\t\tif readErr == unix.EAGAIN || readErr == unix.EWOULDBLOCK {
\t\t\t\t_, pollErr := unix.Poll([]unix.PollFd{{Fd: int32(tunFD), Events: unix.POLLIN}}, 150)
\t\t\t\tif pollErr == unix.EINTR { continue }
\t\t\t\tif pollErr != nil {
\t\t\t\t\tt.app.core.AddLog(fmt.Sprintf("[TEST TUN->UDP] poll exited: %v", pollErr))
\t\t\t\t\treturn
\t\t\t\t}
\t\t\t\tcontinue
\t\t\t}
\t\t\tif readErr == unix.EINTR { continue }
""", 1)
# Header-only packet evidence; no raw payload and no user credentials.
for old,new in (
    ('if count <= 12 {','if count <= 48 {'),
    ('fmt.Sprintf("[TEST TUN->UDP] count=%d bytes=%d ipv=%d", count,n,buf[0]>>4)',
     'fmt.Sprintf("[TEST TUN->UDP] count=%d bytes=%d header=%s", count,n,csqttCoreTestPacketHeader(buf[:n]))'),
    ('fmt.Sprintf("[TEST UDP->TUN] count=%d bytes=%d ipv=%d", count,n,buf[0]>>4)',
     'fmt.Sprintf("[TEST UDP->TUN] count=%d bytes=%d header=%s", count,n,csqttCoreTestPacketHeader(buf[:n]))')
):
    required = 2 if old=='if count <= 12 {' else 1
    assert s.count(old)==required, "test-only header trace marker changed"
    s=s.replace(old,new,required)
s += """
// Test-only packet-header metadata. Never prints TCP/UDP content.
func csqttCoreTestPacketHeader(p []byte) string {
    if len(p)<20 { return fmt.Sprintf("short=%d",len(p)) }
    if p[0]>>4 != 4 { return fmt.Sprintf("ipver=%d",p[0]>>4) }
    ihl:=int(p[0]&0x0f)*4
    if ihl<20 || len(p)<ihl { return "invalid-ipv4-header" }
    proto:=p[9]
    src:=net.IP(p[12:16]).String()
    dst:=net.IP(p[16:20]).String()
    if proto==6 && len(p)>=ihl+14 {
        sp:=int(p[ihl])<<8|int(p[ihl+1])
        dp:=int(p[ihl+2])<<8|int(p[ihl+3])
        return fmt.Sprintf("tcp %s:%d>%s:%d flags=0x%02x",src,sp,dst,dp,p[ihl+13])
    }
    if proto==1 && len(p)>=ihl+1 {
        return fmt.Sprintf("icmp %s>%s type=%d",src,dst,p[ihl])
    }
    return fmt.Sprintf("v4 proto=%d %s>%s",proto,src,dst)
}
"""
runner.write_text(s)
print('TEST_ONLY_RAW_TUN_READ_NONBLOCKING_POLL_ENABLED')
print('TEST_ONLY_GO_TUN_UDP_BIDIRECTIONAL_METADATA_TRACE_ENABLED')
print('TEST_ONLY_GO_SOCKS5_TRACE_ENABLED')
print('TEST_ONLY_DIRECT_DNSMASQ_SKIPPED_ON_DISPOSABLE_ARM64_WITHOUT_DNSMASQ')
print('TEST_ONLY_NO_RESOLVED_DBUS_SIDE_EFFECT_IN_ISOLATED_NETNS')
print('TEST_ONLY_GO_SHA256_PINNED_TO_SYNTHETIC_TURN_FIXTURE',sha)
