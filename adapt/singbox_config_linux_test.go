//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildSingBoxConfigKeepsDirectAndOrderedRouteRulesSeparate(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "192.168.5.2:12500",
		CSQTTAddress: "127.0.0.1:12500",
		DirectDomains: []string{"LOCAL.example", "local.example."},
		DirectDomainSuffixes: []string{".example.org", "example.org"},
		DirectIPCIDRs: []string{"192.0.2.7/24", "2001:db8::1/64", "203.0.113.8"},
		SOCKS5: []singBoxSOCKS5Outbound{{ID:"proxy-a", Server:"198.51.100.2", ServerPort:1080, Enabled:true}},
		Rules: []singBoxRouteRule{
			{ID:"first", Domains:[]string{"one.example"}, Outbound:"socks-proxy-a", Enabled:true},
			{ID:"second", DomainSuffixes:[]string{".example.net"}, DomainKeywords:[]string{"stream"}, DomainRegexes:[]string{"video"}, IPCIDRs:[]string{"198.51.100.9"}, Outbound:singBoxCSQTTTag, Enabled:true},
			{ID:"disabled", Domains:[]string{"disabled.example"}, Outbound:singBoxDirectTag, Enabled:false},
		},
	})
	if err != nil { t.Fatal(err) }

	var config map[string]any
	if err := json.Unmarshal(got, &config); err != nil { t.Fatal(err) }
	inbounds := config["inbounds"].([]any)
	inbound := inbounds[0].(map[string]any)
	if inbound["type"] != "socks" || inbound["listen"] != "192.168.5.2" || inbound["listen_port"] != float64(12500) {
		t.Fatalf("unexpected inbound: %#v", inbound)
	}
	if _, exists := config["dns"]; exists {
		t.Fatal("DNS must remain an explicit next-stage design, not be silently redirected")
	}
	outbounds := config["outbounds"].([]any)
	if len(outbounds) != 3 { t.Fatalf("got %d outbounds, want direct + CSQTT + one user SOCKS5", len(outbounds)) }
	rules := config["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 4 { t.Fatalf("got %d rules, want sniff + Direct + 2 enabled routes", len(rules)) }
	if gotFinal := config["route"].(map[string]any)["final"]; gotFinal != singBoxCSQTTTag {
		t.Fatalf("system fallback = %v, want CSQTT", gotFinal)
	}
	direct := rules[1].(map[string]any)
	if direct["outbound"] != singBoxDirectTag { t.Fatalf("Direct bypass is not first: %#v", direct) }
	if direct["domain"].([]any)[0] != "local.example" { t.Fatalf("domain normalization/dedup failed: %#v", direct) }
	if direct["domain_suffix"].([]any)[0] != ".example.org" { t.Fatalf("Direct suffix missing: %#v", direct) }
	if direct["ip_cidr"].([]any)[0] != "192.0.2.0/24" || direct["ip_cidr"].([]any)[2] != "203.0.113.8/32" { t.Fatalf("CIDR normalization failed: %#v", direct) }
	first := rules[2].(map[string]any)
	second := rules[3].(map[string]any)
	if first["outbound"] != "socks-proxy-a" || first["domain"].([]any)[0] != "one.example" { t.Fatalf("first user route lost ordering: %#v", first) }
	if second["outbound"] != singBoxCSQTTTag || second["domain_suffix"].([]any)[0] != ".example.net" || second["domain_keyword"].([]any)[0] != "stream" || second["domain_regex"].([]any)[0] != "video" || second["ip_cidr"].([]any)[0] != "198.51.100.9/32" { t.Fatalf("second user route malformed: %#v", second) }

	encoded := string(got)
	for _, forbidden := range []string{"auto_route", "auto_redirect", "\"type\": \"tun\""} {
		if strings.Contains(encoded, forbidden) { t.Fatalf("config contains forbidden global capture field %q", forbidden) }
	}
}

func TestBuildSingBoxConfigRejectsInvalidOrAmbiguousInputs(t *testing.T) {
	tests := []struct{name string; input singBoxConfigInput}{
		{"missing inbound address", singBoxConfigInput{CSQTTAddress:"127.0.0.1:12500"}},
		{"same inbound and CSQTT endpoint", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12500"}},
		{"invalid domain", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12500", DirectDomains:[]string{"https://bad.example"}}},
		{"invalid CIDR", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12500", DirectIPCIDRs:[]string{"not-a-cidr"}}},
		{"invalid route regex", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12500", Rules:[]singBoxRouteRule{{ID:"r1",DomainRegexes:[]string{"["},Outbound:singBoxCSQTTTag,Enabled:true}}}},
		{"unknown outbound", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12500", Rules:[]singBoxRouteRule{{ID:"r1",Domains:[]string{"a.example"},Outbound:"missing",Enabled:true}}}},
		{"partial credentials", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12500", SOCKS5:[]singBoxSOCKS5Outbound{{ID:"p1",Server:"198.51.100.1",ServerPort:1080,Username:"u",Enabled:true}}}},
	}
	for _, tt := range tests { t.Run(tt.name,func(t *testing.T){ if _,err:=buildSingBoxConfig(tt.input); err==nil { t.Fatal("expected validation error") } }) }
}

func TestBuildSingBoxConfigRestrictsListenerAndCSQTTOutboundScope(t *testing.T) {
	base := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500"}
	for _, address := range []string{"0.0.0.0:19180", "[::]:19180", "8.8.8.8:19180", "169.254.1.2:19180", "224.0.0.1:19180"} {
		t.Run("reject inbound "+address, func(t *testing.T) {
			input := base
			input.ListenAddress = address
			if _, err := buildSingBoxConfig(input); err == nil { t.Fatalf("unsafe inbound %q was accepted", address) }
		})
	}
	for _, address := range []string{"192.168.5.1:12500", "8.8.8.8:12500", "0.0.0.0:12500"} {
		t.Run("reject non-loopback CSQTT "+address, func(t *testing.T) {
			input := base
			input.CSQTTAddress = address
			if _, err := buildSingBoxConfig(input); err == nil { t.Fatalf("non-loopback CSQTT endpoint %q was accepted", address) }
		})
	}
	for _, address := range []string{"192.168.5.2:19180", "[fd00::2]:19180", "127.0.0.1:19180", "[::1]:19180"} {
		t.Run("allow scoped inbound "+address, func(t *testing.T) {
			input := base
			input.ListenAddress = address
			if _, err := buildSingBoxConfig(input); err != nil { t.Fatalf("safe inbound %q rejected: %v", address, err) }
		})
	}
}

func TestBuildSingBoxConfigAllowsIPOnlyRouteRule(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
		CSQTTAddress: "127.0.0.1:12500",
		Rules: []singBoxRouteRule{{ID: "ip-only", IPCIDRs: []string{"203.0.113.0/24"}, Outbound: singBoxCSQTTTag, Enabled: true}},
	})
	if err != nil { t.Fatalf("IP-only route rule rejected: %v", err) }
	var config map[string]any
	if err := json.Unmarshal(got, &config); err != nil { t.Fatal(err) }
	rules := config["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 { t.Fatalf("route rules=%d, want sniff + IP-only rule", len(rules)) }
	ipRule := rules[1].(map[string]any)
	if ipRule["ip_cidr"].([]any)[0] != "203.0.113.0/24" || ipRule["outbound"] != singBoxCSQTTTag {
		t.Fatalf("IP-only rule malformed: %#v", ipRule)
	}
}

func TestCSQTTAndLoopbackListenersCanSharePort12500(t *testing.T) {
    // Linux permits the same TCP port on distinct local addresses. This models
    // eth1's assigned address and the private loopback endpoint used by sing-box.
    downstream := NewSocks5Server("127.0.0.2:12500")
    if err := downstream.Start(); err != nil { t.Fatalf("start downstream listener: %v", err) }
    defer downstream.Stop()

    local := NewSocks5Server("127.0.0.1:12500")
    if err := local.Start(); err != nil { t.Fatalf("start loopback listener on the same port: %v", err) }
    defer local.Stop()

    if got := downstream.Status().Address; got != "127.0.0.2:12500" {
        t.Fatalf("downstream listener = %q", got)
    }
    if got := local.Status().Address; got != "127.0.0.1:12500" {
        t.Fatalf("loopback listener = %q", got)
    }
}

func TestBuildSingBoxConfigUsesCSQTTAsExplicitSystemFallback(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:12500",CSQTTAddress:"127.0.0.1:12500"})
	if err != nil { t.Fatal(err) }
	var config map[string]any
	if err:=json.Unmarshal(got,&config); err!=nil { t.Fatal(err) }
	route:=config["route"].(map[string]any)
	if route["final"] != singBoxCSQTTTag { t.Fatalf("route.final=%v, want CSQTT",route["final"]) }
	if _,ok:=route["auto_route"];ok { t.Fatal("sing-box must not create system-wide route capture") }
}

type singBoxTestLogBuffer struct {
	mu sync.Mutex
	builder strings.Builder
}
func (b *singBoxTestLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.Write(p)
}
func (b *singBoxTestLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.String()
}

func reserveSingBoxTestAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	address := ln.Addr().String()
	if err := ln.Close(); err != nil { t.Fatal(err) }
	return address
}

func readSingBoxTestSOCKSAddress(conn net.Conn) (string, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil { return "", err }
	var host string
	switch header[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil { return "", err }
		host = net.IP(b).String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil { return "", err }
		b := make([]byte, int(size[0]))
		if _, err := io.ReadFull(conn, b); err != nil { return "", err }
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil { return "", err }
		host = net.IP(b).String()
	default:
		return "", io.ErrUnexpectedEOF
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil { return "", err }
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))), nil
}

func TestSingBoxRoutesDomainThroughCSQTTOutbound(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer upstream.Close()
	targets := make(chan string, 4)
	go func() {
		for {
			conn, acceptErr := upstream.Accept()
			if acceptErr != nil { return }
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(8 * time.Second))
				var greeting [2]byte
				if _, err := io.ReadFull(c, greeting[:]); err != nil { return }
				methods := make([]byte, int(greeting[1]))
				if _, err := io.ReadFull(c, methods); err != nil { return }
				if _, err := c.Write([]byte{5, 0}); err != nil { return }
				target, err := readSingBoxTestSOCKSAddress(c)
				if err != nil { return }
				select { case targets <- target: default: }
				if _, err := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil { return }
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		CSQTTAddress: upstream.Addr().String(),
		Rules: []singBoxRouteRule{{ID: "csqtt-domain", Domains: []string{"route-csqtt.test"}, Outbound: singBoxCSQTTTag, Enabled: true}},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	configPath := filepath.Join(t.TempDir(), "sing-box.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	var inbound net.Conn
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		inbound, err = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
		if err == nil { break }
		time.Sleep(50 * time.Millisecond)
	}
	if inbound == nil { t.Fatalf("sing-box inbound did not become ready: %v; logs: %s", err, output.String()) }
	defer inbound.Close()
	_ = inbound.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := inbound.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
	var greetingReply [2]byte
	if _, err := io.ReadFull(inbound, greetingReply[:]); err != nil { t.Fatal(err) }
	if greetingReply != [2]byte{5, 0} { t.Fatalf("inbound SOCKS greeting = %v", greetingReply) }
	host := "route-csqtt.test"
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, []byte(host)...)
	request = append(request, 0x01, 0xbb) // TCP/443
	if _, err := inbound.Write(request); err != nil { t.Fatal(err) }
	var reply [10]byte
	if _, err := io.ReadFull(inbound, reply[:]); err != nil { t.Fatalf("read CONNECT response: %v; sing-box logs: %s", err, output.String()) }
	if reply[1] != 0 { t.Fatalf("domain route was not connected: SOCKS reply=%v; logs: %s", reply, output.String()) }
	payload := []byte("sing-box-to-csqtt-loopback-e2e")
	if _, err := inbound.Write(payload); err != nil { t.Fatal(err) }
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(inbound, got); err != nil { t.Fatalf("read echo through CSQTT outbound: %v", err) }
	if string(got) != string(payload) { t.Fatalf("echo=%q want=%q", got, payload) }
	select {
	case target := <-targets:
		if target != "route-csqtt.test:443" { t.Fatalf("CSQTT SOCKS outbound received %q", target) }
	case <-time.After(3 * time.Second):
		t.Fatal("CSQTT SOCKS outbound did not receive the routed domain")
	}

	_ = inbound.Close()
	// Unmatched traffic must use the explicit CSQTT system fallback.
	unmatched, err := net.DialTimeout("tcp", input.ListenAddress, time.Second)
	if err != nil { t.Fatal(err) }
	defer unmatched.Close()
	_ = unmatched.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := unmatched.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
	if _, err := io.ReadFull(unmatched, greetingReply[:]); err != nil { t.Fatal(err) }
	unknown := "unmatched-route.test"
	request = []byte{5, 1, 0, 3, byte(len(unknown))}
	request = append(request, []byte(unknown)...)
	request = append(request, 0x01, 0xbb)
	if _, err := unmatched.Write(request); err != nil { t.Fatal(err) }
	if _, err := io.ReadFull(unmatched, reply[:]); err != nil { t.Fatalf("read unmatched SOCKS response: %v", err) }
	if reply[1] != 0 { t.Fatalf("unmatched domain did not use CSQTT system fallback: %v; logs: %s", reply, output.String()) }
	select {
	case target := <-targets:
		if target != "unmatched-route.test:443" { t.Fatalf("fallback target=%q, want unmatched domain", target) }
	case <-time.After(3 * time.Second):
		t.Fatal("unmatched traffic did not reach the CSQTT SOCKS5 outbound")
	}
}

func TestBuildSingBoxConfigPassesPinnedBinaryCheck(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binary == "" {
		t.Skip("CSQTT_SING_BOX_BINARY is not set; real sing-box validation runs in pinned CI")
	}

	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:12500",
		CSQTTAddress:  "127.0.0.1:12500",
		DirectDomains: []string{"example.com"},
		DirectIPCIDRs: []string{"192.0.2.0/24", "2001:db8::/32"},
		SOCKS5: []singBoxSOCKS5Outbound{{
			ID: "proxy-a", Server: "198.51.100.2", ServerPort: 1080, Enabled: true,
		}},
		DirectDomainSuffixes: []string{".example.org"},
		Rules: []singBoxRouteRule{{
			ID: "route-a", DomainSuffixes: []string{".example.net"}, DomainKeywords: []string{"stream"},
			DomainRegexes: []string{"video"}, IPCIDRs: []string{"198.51.100.0/24"},
			Outbound: "socks-proxy-a", Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("build sing-box config: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "check", "-c", configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pinned sing-box config check failed: %v\n%s", err, output)
	}
}
