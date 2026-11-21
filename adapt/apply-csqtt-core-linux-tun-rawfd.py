#!/usr/bin/env python3
"""Minimal Go Linux TUN fd I/O fix for the CSQTT Core 2.5 WIRE-3 release candidate.

Applied AFTER pinned upstream/Linux policy adaptation. It does NOT modify
the WIRE-4 production branch, Rust, Core8, SOCKS5, host routing or DNS.
"""
from pathlib import Path

p=Path("upstream-lalune/Desktop/Linux/app_linux.go")
s=p.read_text(encoding="utf-8")
orig="""\tt.mu.Lock()
\tt.tunFile, t.udpConn = f, udpConn
\tt.workers.Add(2)
\tt.mu.Unlock()"""
replace="""\t// TUN is not pollable via Go's os.File network poller on native ARM64.
\t// Do not use os.File.Read here: it returns "not pollable" immediately.
\t// Nonblocking raw I/O permits bounded poll + prompt Stop/Close.
\ttunFD := int(f.Fd())
\tif err := unix.SetNonblock(tunFD, true); err != nil {
\t\t_ = f.Close()
\t\treturn fmt.Errorf("set nonblocking csqtt0 TUN fd: %w", err)
\t}
\tt.mu.Lock()
\tt.tunFile, t.udpConn = f, udpConn
\tt.workers.Add(2)
\tt.mu.Unlock()"""
assert s.count(orig)==1, "unexpected Go Linux TUN lifecycle source"
s=s.replace(orig,replace,1)
orig="""\t\tfor {
\t\t\tn, readErr := f.Read(buf)
\t\t\tif readErr != nil { return }
\t\t\tif n > 0 {
\t\t\t\tif _, writeErr := udpConn.Write(buf[:n]); writeErr != nil { return }
\t\t\t}
\t\t}"""
replace="""\t\tfor {
\t\t\tn, readErr := unix.Read(tunFD, buf)
\t\t\tif readErr == unix.EINTR { continue }
\t\t\tif readErr == unix.EAGAIN || readErr == unix.EWOULDBLOCK {
\t\t\t\t_, pollErr := unix.Poll([]unix.PollFd{{Fd: int32(tunFD), Events: unix.POLLIN}}, 150)
\t\t\t\tif pollErr == unix.EINTR { continue }
\t\t\t\tif pollErr != nil { return }
\t\t\t\tcontinue
\t\t\t}
\t\t\tif readErr != nil { return }
\t\t\tif n > 0 {
\t\t\t\tif _, writeErr := udpConn.Write(buf[:n]); writeErr != nil { return }
\t\t\t}
\t\t}"""
assert s.count(orig)==1, "unexpected Go Linux TUN read worker"
s=s.replace(orig,replace,1)
orig="""\t\tfor {
\t\t\tn, readErr := udpConn.Read(buf)
\t\t\tif readErr != nil { return }
\t\t\tif n > 0 {
\t\t\t\tif _, writeErr := f.Write(buf[:n]); writeErr != nil { return }
\t\t\t}
\t\t}"""
replace="""\t\tfor {
\t\t\tn, readErr := udpConn.Read(buf)
\t\t\tif readErr != nil { return }
\t\t\tif n == 0 { continue }
\t\t\tfor {
\t\t\t\twritten, writeErr := unix.Write(tunFD, buf[:n])
\t\t\t\tif writeErr == unix.EINTR { continue }
\t\t\t\tif writeErr == unix.EAGAIN || writeErr == unix.EWOULDBLOCK {
\t\t\t\t\t_, pollErr := unix.Poll([]unix.PollFd{{Fd: int32(tunFD), Events: unix.POLLOUT}}, 150)
\t\t\t\t\tif pollErr == unix.EINTR { continue }
\t\t\t\t\tif pollErr != nil { return }
\t\t\t\t\tcontinue
\t\t\t\t}
\t\t\t\tif writeErr != nil || written != n { return }
\t\t\t\tbreak
\t\t\t}
\t\t}"""
assert s.count(orig)==1, "unexpected Go Linux TUN write worker"
s=s.replace(orig,replace,1)
p.write_text(s,encoding="utf-8")
assert "f.Read(buf)" not in s and "f.Write(buf[:n])" not in s
print("CSQTT_250_PRODUCTION_GRADE_LINUX_TUN_RAW_FD_SOURCE_FIX_APPLIED")
