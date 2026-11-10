//go:build linux

package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func testResolver(_ context.Context, host string) ([]net.IPAddr, error) {
	if host == "echo.test" {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	return nil, errors.New("test DNS name not found")
}

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	server, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		Resolve: testResolver,
		StopTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Stop(); err != nil {
			t.Errorf("stop SOCKS5 server: %v", err)
		}
	})
	return server, server.Addr().String()
}

func socksGreeting(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		t.Fatal(err)
	}
	if response != [2]byte{5, 0} {
		t.Fatalf("unexpected greeting response: %v", response)
	}
}

func socksReadReply(t *testing.T, conn net.Conn) byte {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	if header[0] != 5 || header[2] != 0 {
		t.Fatalf("invalid SOCKS5 reply header: %v", header)
	}
	switch header[3] {
	case 1:
		_, err := io.CopyN(io.Discard, conn, 4+2)
		if err != nil {
			t.Fatal(err)
		}
	case 4:
		_, err := io.CopyN(io.Discard, conn, 16+2)
		if err != nil {
			t.Fatal(err)
		}
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			t.Fatal(err)
		}
		_, err := io.CopyN(io.Discard, conn, int64(size[0])+2)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown SOCKS5 reply address type %d", header[3])
	}
	return header[1]
}

func socksAddress(host string, port uint16) []byte {
	ip := net.ParseIP(host)
	var out []byte
	if ip4 := ip.To4(); ip4 != nil {
		out = append([]byte{1}, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		out = append([]byte{4}, ip6...)
	} else {
		out = append([]byte{3, byte(len(host))})
		out = append(out, host...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], port)
	return append(out, p[:]...)
}

func TestNewRejectsNonLoopbackBind(t *testing.T) {
	for _, address := range []string{"0.0.0.0:12500", ":12500", "192.168.1.10:12500"} {
		if _, err := New(Config{ListenAddr: address}); err == nil {
			t.Fatalf("expected non-loopback bind %q to be rejected", address)
		}
	}
}

func TestConnectDomainUsesResolverAndRelaysTCP(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	_, proxyAddr := startTestServer(t)
	proxy, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	socksGreeting(t, proxy)
	_, portText, _ := net.SplitHostPort(echo.Addr().String())
	port, _ := strconv.Atoi(portText)
	request := []byte{5, 1, 0}
	request = append(request, socksAddress("echo.test", uint16(port))...)
	if _, err := proxy.Write(request); err != nil {
		t.Fatal(err)
	}
	if code := socksReadReply(t, proxy); code != 0 {
		t.Fatalf("CONNECT failed with SOCKS reply %d", code)
	}
	_ = proxy.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := proxy.Write([]byte("csqtt-tcp-ok")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("csqtt-tcp-ok"))
	if _, err := io.ReadFull(proxy, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "csqtt-tcp-ok" {
		t.Fatalf("unexpected echo payload %q", got)
	}
}

func TestUDPAssociateRelaysDomainDatagramsAndKeepsControlLifetime(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := echo.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(buf[:n], addr)
		}
	}()

	_, proxyAddr := startTestServer(t)
	control, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	socksGreeting(t, control)
	associate := []byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}
	if _, err := control.Write(associate); err != nil {
		t.Fatal(err)
	}

	var header [4]byte
	if _, err := io.ReadFull(control, header[:]); err != nil {
		t.Fatal(err)
	}
	if header[1] != 0 {
		t.Fatalf("UDP ASSOCIATE failed with code %d", header[1])
	}
	var relayIP net.IP
	switch header[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(control, b); err != nil {
			t.Fatal(err)
		}
		relayIP = net.IP(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(control, b); err != nil {
			t.Fatal(err)
		}
		relayIP = net.IP(b)
	default:
		t.Fatalf("unexpected relay address type %d", header[3])
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(control, portBytes[:]); err != nil {
		t.Fatal(err)
	}
	relayAddr := &net.UDPAddr{IP: relayIP, Port: int(binary.BigEndian.Uint16(portBytes[:]))}

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, targetPortText, _ := net.SplitHostPort(echo.LocalAddr().String())
	targetPort, _ := strconv.Atoi(targetPortText)
	packet := []byte{0, 0, 0}
	packet = append(packet, socksAddress("echo.test", uint16(targetPort))...)
	packet = append(packet, []byte("csqtt-udp-ok")...)
	if _, err := client.WriteToUDP(packet, relayAddr); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(4 * time.Second))
	reply := make([]byte, 2048)
	n, _, err := client.ReadFromUDP(reply)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 || reply[0] != 0 || reply[1] != 0 || reply[2] != 0 {
		t.Fatalf("invalid UDP reply envelope: %v", reply[:min(n, 8)])
	}
	if string(reply[n-len("csqtt-udp-ok"):n]) != "csqtt-udp-ok" {
		t.Fatalf("unexpected UDP echo payload %q", reply[:n])
	}

	_ = control.Close()
	_ = client.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	if _, _, err := client.ReadFromUDP(reply); err == nil {
		t.Fatal("expected UDP relay to stop after control connection closed")
	}
}

func TestUnsupportedAuthMethodIsRejected(t *testing.T) {
	_, proxyAddr := startTestServer(t)
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		t.Fatal(err)
	}
	if response != [2]byte{5, 0xff} {
		t.Fatalf("unexpected auth response: %v", response)
	}
}

func TestStopClosesActiveControlConnection(t *testing.T) {
	server, proxyAddr := startTestServer(t)
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	socksGreeting(t, conn)
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := conn.Read(b[:]); err == nil {
		t.Fatal("expected active connection to close on Stop")
	}
	_ = conn.Close()
}
