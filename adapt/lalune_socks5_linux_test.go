//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestSocks5ListenAddressUsesDownstreamLANIP(t *testing.T) {
	got, err := socks5ListenAddrFromAddrs([]net.Addr{
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("192.168.5.1"), Mask: net.CIDRMask(24, 32)},
	})
	if err != nil { t.Fatal(err) }
	if got != "192.168.5.1:12500" { t.Fatalf("listen address=%q", got) }
	if _, err := socks5ListenAddrFromAddrs([]net.Addr{&net.IPNet{IP: net.ParseIP("8.8.8.8"), Mask: net.CIDRMask(32, 32)}}); err == nil {
		t.Fatal("public IPv4 must not be used for unauthenticated SOCKS5")
	}
	if _, err := socks5ListenAddrFromAddrs(nil); err == nil {
		t.Fatal("missing LAN address must fail closed")
	}
}

func TestSocks5ListenAddressRejectsMultiplePrivateIPv4s(t *testing.T) {
	_, err := socks5ListenAddrFromAddrs([]net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.5.1"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
	})
	if err == nil { t.Fatal("multiple private IPv4 addresses must be rejected as ambiguous") }
}

func TestSocks5RebindClosesSessionsFromOldAddress(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	oldAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	newAddr := net.JoinHostPort("127.0.0.2", strconv.Itoa(port))
	var mu sync.Mutex
	current := oldAddr
	s := NewSocks5Server("eth1:12500")
	s.pollInterval = 10 * time.Millisecond
	s.resolveAddr = func() (string, error) { mu.Lock(); defer mu.Unlock(); return current, nil }
	if err := s.Start(); err != nil { t.Fatal(err) }
	defer s.Stop()
	client, err := net.DialTimeout("tcp", oldAddr, time.Second)
	if err != nil { t.Fatal(err) }
	defer client.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock(); active := len(s.active); s.mu.Unlock()
		if active == 1 { break }
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock(); active := len(s.active); s.mu.Unlock()
	if active != 1 { t.Fatalf("expected one active session, got %d", active) }
	mu.Lock(); current = newAddr; mu.Unlock()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock(); got := ""; if s.ln != nil { got = s.ln.Addr().String() }; s.mu.Unlock()
		if got == newAddr { break }
		time.Sleep(5 * time.Millisecond)
	}
	s.mu.Lock(); got := ""; if s.ln != nil { got = s.ln.Addr().String() }; s.mu.Unlock()
	if got != newAddr { t.Fatalf("listener did not rebind: %q", got) }
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := client.Read(b[:]); err == nil { t.Fatal("old downstream session remained open after rebind") }
	// Closing the socket is synchronous, but the handler's deferred active-map
	// cleanup runs when its goroutine is scheduled. Wait for that bookkeeping
	// rather than asserting it in the same scheduling instant as Read returns.
	cleanupDeadline := time.Now().Add(time.Second)
	for time.Now().Before(cleanupDeadline) {
		s.mu.Lock()
		remaining := len(s.active)
		s.mu.Unlock()
		if remaining == 0 { return }
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	remaining := len(s.active)
	s.mu.Unlock()
	if remaining != 0 { t.Fatalf("%d old sessions remain after rebind cleanup deadline", remaining) }
}

func TestWriteSocksReplyUsesDynamicIPv4Address(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	wantIP := net.IPv4(10, 67, 68, 9)
	wantPort := 43127
	done := make(chan error, 1)
	go func() { done <- writeSocksReply(server, 0, &net.TCPAddr{IP: wantIP, Port: wantPort}) }()

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 || reply[3] != 1 {
		t.Fatalf("unexpected SOCKS5 reply header: %v", reply[:4])
	}
	if !net.IP(reply[4:8]).Equal(wantIP) {
		t.Fatalf("reply IP = %v, want %v", net.IP(reply[4:8]), wantIP)
	}
	if got := int(binary.BigEndian.Uint16(reply[8:10])); got != wantPort {
		t.Fatalf("reply port = %d, want %d", got, wantPort)
	}
}

func TestSocks5RejectsGreetingWithoutNoAuth(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	s := NewSocks5Server(":0")
	go s.handle(serverConn)

	if _, err := clientConn.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(clientConn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 5 || reply[1] != 0xff {
		t.Fatalf("greeting reply = %v, want [5 255]", reply)
	}
}

func TestReadUDPAddrDomainAndPort(t *testing.T) {
	packet := []byte{3, 11, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 0x30, 0x39}
	got, next, err := readUDPAddr(packet, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.com:12345" {
		t.Fatalf("address = %q, want example.com:12345", got)
	}
	if next != len(packet) {
		t.Fatalf("next offset = %d, want %d", next, len(packet))
	}
}

func TestReadUDPAddrRejectsTruncatedPacket(t *testing.T) {
	if _, _, err := readUDPAddr([]byte{1, 127, 0}, 0); err == nil {
		t.Fatal("expected truncated address to fail")
	}
}

func TestSocks5ServerStartStopIsIdempotent(t *testing.T) {
	s := NewSocks5Server("127.0.0.1:0")
	if err := s.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	s.Stop()
	s.Stop()
	if err := s.Start(); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	s.Stop()
}

func TestBuildUDPReplyIPv4(t *testing.T) {
	payload := []byte("reply")
	got, err := buildUDPReply(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 5353}, payload)
	if err != nil { t.Fatal(err) }
	wantHeader := []byte{0, 0, 0, 1, 192, 0, 2, 10, 0x14, 0xe9}
	if len(got) != len(wantHeader)+len(payload) { t.Fatalf("packet length = %d", len(got)) }
	for i := range wantHeader { if got[i] != wantHeader[i] { t.Fatalf("header = %v, want %v", got[:len(wantHeader)], wantHeader) } }
	if string(got[len(wantHeader):]) != string(payload) { t.Fatal("payload was not preserved") }
}

func TestBuildUDPReplyIPv6(t *testing.T) {
	payload := []byte("v6")
	got, err := buildUDPReply(&net.UDPAddr{IP: net.ParseIP("2001:db8::7"), Port: 443}, payload)
	if err != nil { t.Fatal(err) }
	if len(got) != 4+16+2+len(payload) { t.Fatalf("packet length = %d", len(got)) }
	if got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 4 { t.Fatalf("ATYP header = %v, want IPv6", got[:4]) }
	if !net.IP(got[4:20]).Equal(net.ParseIP("2001:db8::7")) { t.Fatalf("reply IP = %v", net.IP(got[4:20])) }
	if binary.BigEndian.Uint16(got[20:22]) != 443 { t.Fatalf("reply port = %d", binary.BigEndian.Uint16(got[20:22])) }
	if string(got[22:]) != string(payload) { t.Fatal("payload was not preserved") }
}

func TestBuildUDPReplyRejectsInvalidAddress(t *testing.T) {
	if _, err := buildUDPReply(nil, nil); err == nil { t.Fatal("expected invalid address error") }
}

func TestResolveTCPAddrMarkedLiteralIPv4(t *testing.T) {
	got, err := resolveTCPAddrMarked(context.Background(), "192.0.2.25:443")
	if err != nil { t.Fatal(err) }
	if got != "192.0.2.25:443" { t.Fatalf("resolved target=%q", got) }
}

func TestResolveTCPAddrMarkedRejectsInvalidPort(t *testing.T) {
	if _, err := resolveTCPAddrMarked(context.Background(), "example.invalid:70000"); err == nil {
		t.Fatal("expected invalid TCP target port")
	}
}

func TestResolveTCPAddrMarkedRejectsMissingPort(t *testing.T) {
	if _, err := resolveTCPAddrMarked(context.Background(), "example.invalid"); err == nil {
		t.Fatal("expected missing TCP target port")
	}
}

func TestUDPNetworkForIPSelectsAddressFamily(t *testing.T) {
	if network, local := udpNetworkForIP(net.ParseIP("192.0.2.1")); network != "udp4" || local != "0.0.0.0:0" {
		t.Fatalf("IPv4 socket = %s %s", network, local)
	}
	if network, local := udpNetworkForIP(net.ParseIP("2001:db8::1")); network != "udp6" || local != "[::]:0" {
		t.Fatalf("IPv6 socket = %s %s", network, local)
	}
}

func TestResolveUDPAddrMarkedLiteralIPv4(t *testing.T) {
	got, err := resolveUDPAddrMarked("192.0.2.25:5353")
	if err != nil { t.Fatal(err) }
	if !got.IP.Equal(net.IPv4(192, 0, 2, 25)) || got.Port != 5353 {
		t.Fatalf("resolved address = %v, want 192.0.2.25:5353", got)
	}
}

func TestResolveUDPAddrMarkedRejectsInvalidPort(t *testing.T) {
	if _, err := resolveUDPAddrMarked("192.0.2.25:70000"); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestResolveUDPAddrMarkedRejectsMissingPort(t *testing.T) {
	if _, err := resolveUDPAddrMarked("192.0.2.25"); err == nil {
		t.Fatal("expected missing port error")
	}
}


func socks5Handshake(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil { t.Fatal(err) }
	if reply[0] != 5 || reply[1] != 0 { t.Fatalf("handshake reply = %v", reply) }
}

func TestSocks5ConnectRelaysTCPPayload(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil { return }
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	s := NewSocks5Server("127.0.0.1:0")
	s.dialTCPOverride = func(addr string) (net.Conn, error) { return net.Dial("tcp", addr) }
	if err := s.Start(); err != nil { t.Fatal(err) }
	defer s.Stop()
	client, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil { t.Fatal(err) }
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	socks5Handshake(t, client)
	host, portText, err := net.SplitHostPort(echo.Addr().String())
	if err != nil { t.Fatal(err) }
	port, err := strconv.Atoi(portText)
	if err != nil { t.Fatal(err) }
	ip := net.ParseIP(host).To4()
	req := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port)}
	if _, err := client.Write(req); err != nil { t.Fatal(err) }
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil { t.Fatal(err) }
	if reply[1] != 0 { t.Fatalf("CONNECT failed: %v", reply) }
	payload := []byte("socks5-tcp-e2e")
	if _, err := client.Write(payload); err != nil { t.Fatal(err) }
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil { t.Fatal(err) }
	if string(got) != string(payload) { t.Fatalf("echo = %q, want %q", got, payload) }
}

func TestSocks5UDPAssociateRelaysDatagram(t *testing.T) {
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := echo.ReadFromUDP(buf)
			if err != nil { return }
			_, _ = echo.WriteToUDP(buf[:n], addr)
		}
	}()

	s := NewSocks5Server("127.0.0.1:0")
	s.dialUDPOverride = func() (*net.UDPConn, error) {
		return net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	}
	if err := s.Start(); err != nil { t.Fatal(err) }
	defer s.Stop()
	control, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil { t.Fatal(err) }
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(4 * time.Second))
	socks5Handshake(t, control)
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil { t.Fatal(err) }
	reply := make([]byte, 10)
	if _, err := io.ReadFull(control, reply); err != nil { t.Fatal(err) }
	if reply[1] != 0 { t.Fatalf("UDP ASSOCIATE failed: %v", reply) }
	relayIP := net.IP(reply[4:8])
	relayPort := int(binary.BigEndian.Uint16(reply[8:10]))
	if relayIP.IsUnspecified() { relayIP = net.IPv4(127, 0, 0, 1) }
	relay, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: relayIP, Port: relayPort})
	if err != nil { t.Fatal(err) }
	defer relay.Close()
	_ = relay.SetDeadline(time.Now().Add(4 * time.Second))
	dst := echo.LocalAddr().(*net.UDPAddr)
	const count = 32
	want := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		payload := fmt.Sprintf("socks5-udp-parallel-%02d", i)
		want[payload] = true
		packet := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(dst.Port >> 8), byte(dst.Port)}
		packet = append(packet, []byte(payload)...)
		if _, err := relay.Write(packet); err != nil { t.Fatal(err) }
	}
	got := make([]byte, 2048)
	for i := 0; i < count; i++ {
		n, err := relay.Read(got)
		if err != nil { t.Fatalf("UDP reply %d/%d: %v", i+1, count, err) }
		if n < 10 || got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 1 {
			t.Fatalf("invalid UDP relay response: %v", got[:n])
		}
		payload := string(got[10:n])
		if !want[payload] { t.Fatalf("unexpected or duplicate UDP payload %q", payload) }
		delete(want, payload)
	}
	if len(want) != 0 { t.Fatalf("missing UDP responses: %v", want) }
}


// TestSocks5MarkedSocketEndToEnd runs only in the disposable privileged VM.
// The test intentionally leaves dial overrides unset: it exercises SO_MARK and
// the kernel policy route, not merely the SOCKS protocol relay.
func TestSocks5MarkedSocketEndToEnd(t *testing.T) {
	if os.Getenv("CSQTT_SOCKS5_MARKED_E2E") != "1" {
		t.Skip("requires disposable VM fwmark policy")
	}
	if _, err := net.InterfaceByName("csqtt0"); err != nil {
		t.Fatalf("test TUN/egress interface csqtt0 missing: %v", err)
	}
	downstream, err := net.InterfaceByName("eth1")
	if err != nil { t.Fatalf("production downstream interface eth1 missing: %v", err) }
	addrs, err := downstream.Addrs()
	if err != nil { t.Fatalf("read eth1 addresses: %v", err) }
	foundDownstreamIP := false
	for _, addr := range addrs {
		ip, _, parseErr := net.ParseCIDR(addr.String())
		if parseErr == nil && ip.Equal(net.ParseIP("10.67.68.1")) { foundDownstreamIP = true }
	}
	if !foundDownstreamIP { t.Fatalf("eth1 must own 10.67.68.1 for production listener test; addresses=%v", addrs) }
	// The VM provides echo servers in a separate network namespace. Traffic to
	// that namespace must use the marked policy table; the main table deliberately
	// blackholes the test subnet, so an unmarked socket cannot accidentally pass.
	target := os.Getenv("CSQTT_SOCKS5_E2E_TARGET")
	if target == "" {
		t.Fatal("CSQTT_SOCKS5_E2E_TARGET must point to the isolated namespace echo server")
	}
	tcpPort, udpPort := 18080, 18081

	t.Run("TCP", func(t *testing.T) {
		s := NewSocks5Server("eth1:12500")
		s.tracef = t.Logf
		if err := s.Start(); err != nil { t.Fatal(err) }
		defer s.Stop()
		client, err := net.DialTimeout("tcp", "10.67.68.1:12500", 2*time.Second)
		if err != nil { t.Fatal(err) }
		defer client.Close()
		_ = client.SetDeadline(time.Now().Add(5*time.Second))
		socks5Handshake(t, client)
		// The VM resolver points at the isolated DNS fixture. A domain-form
		// CONNECT proves that DNS itself is resolved through SO_MARK, not just
		// that the final TCP socket is marked.
		host := "marked.socks.test."
		req := []byte{5, 1, 0, 3, byte(len(host))}
		req = append(req, []byte(host)...)
		req = append(req, byte(tcpPort >> 8), byte(tcpPort))
		if _, err := client.Write(req); err != nil { t.Fatal(err) }
		reply := make([]byte, 10)
		if _, err := io.ReadFull(client, reply); err != nil { t.Fatal(err) }
		if reply[1] != 0 { t.Fatalf("marked CONNECT failed: %v", reply) }
		payload := []byte("marked-tcp-via-policy-route")
		if _, err := client.Write(payload); err != nil { t.Fatal(err) }
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(client, got); err != nil { t.Fatalf("TCP echo via csqtt0 failed: %v", err) }
		if string(got) != string(payload) { t.Fatalf("TCP echo %q", got) }
	})

	t.Run("UDP", func(t *testing.T) {
		s := NewSocks5Server("eth1:12500")
		if err := s.Start(); err != nil { t.Fatal(err) }
		defer s.Stop()
		control, err := net.DialTimeout("tcp", "10.67.68.1:12500", 2*time.Second)
		if err != nil { t.Fatal(err) }
		defer control.Close()
		_ = control.SetDeadline(time.Now().Add(6*time.Second))
		socks5Handshake(t, control)
		if _, err := control.Write([]byte{5,3,0,1,0,0,0,0,0,0}); err != nil { t.Fatal(err) }
		reply := make([]byte, 10)
		if _, err := io.ReadFull(control, reply); err != nil { t.Fatal(err) }
		if reply[1] != 0 { t.Fatalf("UDP ASSOCIATE failed: %v", reply) }
		relayIP := net.IP(reply[4:8])
		if relayIP.IsUnspecified() { relayIP = net.ParseIP("10.67.68.1").To4() }
		relay, err := net.DialUDP("udp", nil, &net.UDPAddr{IP:relayIP, Port:int(binary.BigEndian.Uint16(reply[8:10]))})
		if err != nil { t.Fatal(err) }
		defer relay.Close()
		_ = relay.SetDeadline(time.Now().Add(20*time.Second))
		host := "marked.socks.test."
		const count = 100
		for i := 0; i < count; i++ {
			packet := []byte{0,0,0,3,byte(len(host))}
			packet = append(packet, []byte(host)...)
			packet = append(packet, byte(udpPort>>8), byte(udpPort))
			packet = append(packet, []byte(fmt.Sprintf("marked-udp-%02d", i))...)
			if _, err := relay.Write(packet); err != nil { t.Fatal(err) }
			time.Sleep(100 * time.Millisecond)
		}
		seen := make(map[string]bool, count)
		got := make([]byte, 1024)
		for i := 0; i < count; i++ {
			n, err := relay.Read(got)
			if err != nil { t.Fatalf("marked UDP reply %d/%d: %v", i+1, count, err) }
			if n < 10 { t.Fatalf("short marked UDP response: %v", got[:n]) }
			payload := string(got[10:n])
			if seen[payload] { t.Fatalf("duplicate marked UDP response %q", payload) }
			seen[payload] = true
		}
		if len(seen) != count { t.Fatalf("received %d/%d marked UDP replies", len(seen), count) }
		t.Logf("UDP_SOAK_PASS duration>=10s sent=%d received=%d", count, len(seen))
	})
}


func TestSocks5ListenerRebindsAfterDownstreamAddressChange(t *testing.T) {
 probe,err:=net.Listen("tcp","127.0.0.1:0"); if err!=nil {t.Fatal(err)}; port:=probe.Addr().(*net.TCPAddr).Port; _=probe.Close()
 oldAddr:=net.JoinHostPort("127.0.0.1",strconv.Itoa(port)); newAddr:=net.JoinHostPort("127.0.0.2",strconv.Itoa(port))
 var addrMu sync.Mutex
 current:=oldAddr
 s:=NewSocks5Server("eth1:12500"); s.resolveAddr=func()(string,error){addrMu.Lock(); defer addrMu.Unlock(); return current,nil}; s.pollInterval=20*time.Millisecond
 s.dialTCPOverride=func(addr string)(net.Conn,error){return net.Dial("tcp",addr)}
 s.dialUDPOverride=func()(*net.UDPConn,error){return net.ListenUDP("udp",&net.UDPAddr{IP:net.IPv4zero,Port:0})}
 if err:=s.Start(); err!=nil {t.Fatal(err)}; defer s.Stop(); addrMu.Lock(); current=newAddr; addrMu.Unlock()
 deadline:=time.Now().Add(2*time.Second); got:=""; for time.Now().Before(deadline){s.mu.Lock(); if s.ln!=nil {got=s.ln.Addr().String()}; s.mu.Unlock(); if got==newAddr {break}; time.Sleep(10*time.Millisecond)}
 if got!=newAddr {t.Fatalf("listener did not rebind: got %q want %q",got,newAddr)}
 echo,err:=net.Listen("tcp","127.0.0.1:0"); if err!=nil {t.Fatal(err)}; defer echo.Close(); go func(){for{c,e:=echo.Accept();if e!=nil{return};go func(c net.Conn){defer c.Close();_,_=io.Copy(c,c)}(c)}}()
 c,err:=net.DialTimeout("tcp",newAddr,time.Second);if err!=nil{t.Fatal(err)};_=c.SetDeadline(time.Now().Add(2*time.Second));socks5Handshake(t,c)
 dst:=echo.Addr().(*net.TCPAddr);ip:=dst.IP.To4();req:=[]byte{5,1,0,1,ip[0],ip[1],ip[2],ip[3],byte(dst.Port>>8),byte(dst.Port)};if _,err=c.Write(req);err!=nil{t.Fatal(err)}
 rep:=make([]byte,10);if _,err=io.ReadFull(c,rep);err!=nil{t.Fatal(err)};if rep[1]!=0{t.Fatalf("TCP CONNECT after rebind: %v",rep)};p:=[]byte("tcp-after-rebind");if _,err=c.Write(p);err!=nil{t.Fatal(err)};gotP:=make([]byte,len(p));if _,err=io.ReadFull(c,gotP);err!=nil{t.Fatal(err)};if string(gotP)!=string(p){t.Fatalf("TCP echo %q",gotP)};_=c.Close()
 ue,err:=net.ListenUDP("udp",&net.UDPAddr{IP:net.IPv4(127,0,0,1)});if err!=nil{t.Fatal(err)};defer ue.Close();go func(){b:=make([]byte,1024);for{n,a,e:=ue.ReadFromUDP(b);if e!=nil{return};_,_=ue.WriteToUDP(b[:n],a)}}()
 ctl,err:=net.DialTimeout("tcp",newAddr,time.Second);if err!=nil{t.Fatal(err)};defer ctl.Close();_=ctl.SetDeadline(time.Now().Add(3*time.Second));socks5Handshake(t,ctl);if _,err=ctl.Write([]byte{5,3,0,1,0,0,0,0,0,0});err!=nil{t.Fatal(err)}
 ar:=make([]byte,10);if _,err=io.ReadFull(ctl,ar);err!=nil{t.Fatal(err)};if ar[1]!=0{t.Fatalf("UDP ASSOCIATE after rebind: %v",ar)};rip:=net.IP(ar[4:8]);if rip.IsUnspecified(){rip=net.IPv4(127,0,0,1)}
 relay,err:=net.DialUDP("udp",nil,&net.UDPAddr{IP:rip,Port:int(binary.BigEndian.Uint16(ar[8:10]))});if err!=nil{t.Fatal(err)};defer relay.Close();_=relay.SetDeadline(time.Now().Add(3*time.Second));ud:=ue.LocalAddr().(*net.UDPAddr)
 packet:=[]byte{0,0,0,1,127,0,0,1,byte(ud.Port>>8),byte(ud.Port)};packet=append(packet,[]byte("udp-after-rebind")...);if _,err=relay.Write(packet);err!=nil{t.Fatal(err)};b:=make([]byte,1024);n,err:=relay.Read(b);if err!=nil{t.Fatal(err)};if n<10||string(b[10:n])!="udp-after-rebind"{t.Fatalf("UDP echo after rebind: %q",b[10:n])}
}


func TestSocks5StartReturnsInitialBindConflict(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer occupied.Close()
	s := NewSocks5Server(occupied.Addr().String())
	if err := s.Start(); err == nil {
		s.Stop()
		t.Fatal("Start must report an occupied TCP port instead of returning healthy")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil || s.stop != nil || !s.stopped {
		t.Fatalf("failed Start left live state: listener=%v stop=%v stopped=%v", s.ln, s.stop != nil, s.stopped)
	}
}

func TestSocks5StartWaitsForDownstreamAddress(t *testing.T) {
	var mu sync.Mutex
	addr := ""
	s := NewSocks5Server("eth1:12500")
	s.pollInterval = 10 * time.Millisecond
	s.resolveAddr = func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if addr == "" { return "", fmt.Errorf("%w: no eth1 IPv4", errSocksAddressUnavailable) }
		return addr, nil
	}
	if err := s.Start(); err != nil { t.Fatalf("Start must tolerate missing eth1 address: %v", err) }
	mu.Lock()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	addr = probe.Addr().String()
	_ = probe.Close()
	mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ln := s.ln
		s.mu.Unlock()
		if ln != nil { s.Stop(); return }
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	t.Fatal("listener did not start after downstream address appeared")
}

func TestSocks5StopClosesActiveSessions(t *testing.T) {
	s := NewSocks5Server("127.0.0.1:0")
	if err := s.Start(); err != nil { t.Fatal(err) }
	s.mu.Lock()
	addr := s.ln.Addr().String()
	s.mu.Unlock()
	client, err := net.Dial("tcp", addr)
	if err != nil { t.Fatal(err) }
	if _, err := client.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil { t.Fatal(err) }
	// Keep the accepted session alive waiting for the SOCKS request.
	s.Stop()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := client.Read(b[:]); err == nil { t.Fatal("client connection remained open after Stop") }
	s.mu.Lock()
	remaining := len(s.active)
	listener := s.ln
	s.mu.Unlock()
	if remaining != 0 { t.Fatalf("%d active sessions remain after Stop", remaining) }
	if listener != nil { t.Fatal("listener remains published after Stop") }
}

func TestSocks5StopRacingWithAddressResolution(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := NewSocks5Server("eth1:12500")
	s.pollInterval = 5 * time.Millisecond
	var calls int
	s.resolveAddr = func() (string, error) {
		calls++
		if calls == 1 { return "", fmt.Errorf("%w: address not assigned yet", errSocksAddressUnavailable) }
		select { case <-entered: default: close(entered) }
		<-release
		return "127.0.0.1:1", nil
	}
	if err := s.Start(); err != nil { t.Fatal(err) }
	<-entered
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second): t.Fatal("Stop deadlocked during address resolution")
	}
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if ln != nil { t.Fatal("listener was published after Stop") }
}


func TestSocks5StartRejectsAmbiguousAddressResolution(t *testing.T) {
    s := NewSocks5Server("eth1:12500")
    s.resolveAddr = func() (string, error) { return "", errors.New("multiple private IPv4 addresses") }
    if err := s.Start(); err == nil { s.Stop(); t.Fatal("ambiguous bind address must fail closed") }
    s.mu.Lock()
    defer s.mu.Unlock()
    if !s.stopped || s.stop != nil || s.done != nil || s.ln != nil {
        t.Fatalf("failed Start left live state: stopped=%v stop=%v done=%v listener=%v", s.stopped, s.stop != nil, s.done != nil, s.ln)
    }
}

func TestSocks5StatusReportsOnlyActualListener(t *testing.T) {
	s := NewSocks5Server("127.0.0.1:0")
	before := s.Status()
	if before.Listening || before.Monitoring || before.Address != "" {
		t.Fatalf("stopped server status = %+v", before)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer ln.Close()
	s.mu.Lock()
	s.stop = make(chan struct{})
	s.stopped = false
	s.ln = ln
	s.mu.Unlock()
	got := s.Status()
	if !got.Monitoring || !got.Listening || got.Address != ln.Addr().String() {
		t.Fatalf("active listener status = %+v, actual=%s", got, ln.Addr())
	}
	if got.Interface != "eth1" || got.Protocol != "SOCKS5" || got.Auth != "no-auth" {
		t.Fatalf("unexpected SOCKS5 contract: %+v", got)
	}
}
