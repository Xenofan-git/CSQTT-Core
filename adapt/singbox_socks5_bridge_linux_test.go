//go:build linux

package main

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestSocks5BridgePreservesDomainAndTCPStream(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer upstream.Close()
	targets := make(chan string, 1)
	go func() {
		conn, err := upstream.Accept()
		if err != nil { return }
		defer conn.Close()
		var h [2]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil { return }
		methods := make([]byte, int(h[1]))
		if _, err := io.ReadFull(conn, methods); err != nil { return }
		_, _ = conn.Write([]byte{5, 0})
		var req [4]byte
		if _, err := io.ReadFull(conn, req[:]); err != nil { return }
		if req[3] != 3 { return }
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil { return }
		domain := make([]byte, int(n[0]))
		if _, err := io.ReadFull(conn, domain); err != nil { return }
		var port [2]byte
		if _, err := io.ReadFull(conn, port[:]); err != nil { return }
		targets <- net.JoinHostPort(string(domain), strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		_, _ = io.Copy(conn, conn)
	}()

	bridge := NewSocks5Bridge("127.0.0.1:0")
	bridge.resolveUpstream = func() (string, error) { return upstream.Addr().String(), nil }
	if err := bridge.Start(); err != nil { t.Fatal(err) }
	defer bridge.Stop()
	addr := bridge.Addr()
	if addr == "" { t.Fatal("bridge did not publish listener address") }
	client, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil { t.Fatal(err) }
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3*time.Second))
	if _, err := client.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
	var greeting [2]byte
	if _, err := io.ReadFull(client, greeting[:]); err != nil { t.Fatal(err) }
	if greeting != [2]byte{5, 0} { t.Fatalf("greeting=%v", greeting) }
	domain := "preserved-domain.example"
	req := []byte{5, 1, 0, 3, byte(len(domain))}
	req = append(req, []byte(domain)...)
	req = append(req, 1, 187) // 443
	if _, err := client.Write(req); err != nil { t.Fatal(err) }
	var reply [10]byte
	if _, err := io.ReadFull(client, reply[:]); err != nil { t.Fatal(err) }
	if reply[1] != 0 { t.Fatalf("CONNECT reply=%v", reply) }
	payload := []byte("bridge-transparent-stream")
	if _, err := client.Write(payload); err != nil { t.Fatal(err) }
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil { t.Fatal(err) }
	if string(got) != string(payload) { t.Fatalf("echo=%q want=%q", got, payload) }
	select {
	case target := <-targets:
		if target != "preserved-domain.example:443" { t.Fatalf("upstream target=%q", target) }
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive domain target")
	}
}

func TestSocks5BridgeRejectsNonLoopbackAndStops(t *testing.T) {
	for _, address := range []string{"0.0.0.0:12501", "192.168.1.2:12501", "127.0.0.1:0", ":12501"} {
		bridge := NewSocks5Bridge(address)
		if err := bridge.Start(); err == nil {
			bridge.Stop()
			t.Fatalf("unsafe bridge bind %q was accepted", address)
		}
	}
	bridge := NewSocks5Bridge("127.0.0.1:0")
	// Start requires a stable, nonzero loopback port in production. Reserve one
	// dynamically for this lifecycle test, then release it before binding.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	bridge.listenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := bridge.Start(); err != nil { t.Fatal(err) }
	if bridge.Addr() == "" { t.Fatal("listener missing after Start") }
	bridge.Stop()
	if bridge.Addr() != "" { t.Fatal("listener remains published after Stop") }
}
