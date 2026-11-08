//go:build linux

package main

import (
	"crypto/tls"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
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
		FinalOutbound: singBoxDirectTag,
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
	if gotFinal := config["route"].(map[string]any)["final"]; gotFinal != singBoxDirectTag {
		t.Fatalf("system fallback = %v, want explicitly selected Direct", gotFinal)
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
		{"missing inbound address", singBoxConfigInput{CSQTTAddress:"127.0.0.1:12500",FinalOutbound:singBoxDirectTag}},
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
	base := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500", FinalOutbound: singBoxDirectTag}
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
		FinalOutbound: singBoxDirectTag,
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

func TestBuildSingBoxConfigDoesNotImplicitlyUseCSQTTAsFallback(t *testing.T) {
	input := singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500",CSQTTEnabled:true,FinalOutbound:singBoxDirectTag}
	got, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	var config map[string]any
	if err:=json.Unmarshal(got,&config); err!=nil { t.Fatal(err) }
	route:=config["route"].(map[string]any)
	if route["final"] != singBoxDirectTag { t.Fatalf("route.final=%v, want explicit Direct",route["final"]) }
	foundCSQTT := false
	for _, raw := range config["outbounds"].([]any) {
		if raw.(map[string]any)["tag"] == singBoxCSQTTTag { foundCSQTT = true }
	}
	if !foundCSQTT { t.Fatal("enabled CSQTT must remain available as an independently selectable outbound") }
	if _,ok:=route["auto_route"];ok { t.Fatal("sing-box must not create system-wide route capture") }

	input.FinalOutbound = singBoxCSQTTTag
	encodedCSQTT, err := buildSingBoxConfig(input)
	if err != nil { t.Fatalf("explicit CSQTT fallback rejected: %v", err) }
	var csqttConfig map[string]any
	if err := json.Unmarshal(encodedCSQTT, &csqttConfig); err != nil { t.Fatal(err) }
	if got := csqttConfig["route"].(map[string]any)["final"]; got != singBoxCSQTTTag {
		t.Fatalf("explicit route.final=%v, want CSQTT", got)
	}
}

func TestBuildSingBoxConfigRequiresExplicitFallbackEvenWhenCSQTTEnabled(t *testing.T) {
	_, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500",CSQTTEnabled:true})
	if err == nil || !strings.Contains(err.Error(), "select a default outbound") { t.Fatalf("missing explicit fallback error = %v", err) }
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

func TestSingBoxRoutesDomainThroughRealCSQTTSocks5Server(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() { for { c,e:=echo.Accept(); if e!=nil{return}; go func(c net.Conn){defer c.Close(); _,_=io.Copy(c,c)}(c) } }()
	targets:=make(chan string,2)
	csqtt:=NewSocks5Server("127.0.0.1:0")
	csqtt.dialTCPOverride=func(target string)(net.Conn,error){select{case targets<-target:default:};return net.DialTimeout("tcp",echo.Addr().String(),2*time.Second)}
	if err:=csqtt.Start();err!=nil{t.Fatalf("start real CSQTT SOCKS5: %v",err)}
	defer csqtt.Stop()
	input:=singBoxConfigInput{ListenAddress:reserveSingBoxTestAddress(t),CSQTTAddress:csqtt.Status().Address,FinalOutbound:singBoxDirectTag,Rules:[]singBoxRouteRule{{ID:"csqtt-domain",Domains:[]string{"route-csqtt.test"},Outbound:singBoxCSQTTTag,Enabled:true}}}
	config,err:=buildSingBoxConfig(input);if err!=nil{t.Fatal(err)}
	path:=filepath.Join(t.TempDir(),"sing-box.json");if err=os.WriteFile(path,config,0600);err!=nil{t.Fatal(err)}
	cmd:=exec.Command(binaryPath,"run","-c",path);output:=&singBoxTestLogBuffer{};cmd.Stdout,cmd.Stderr=output,output
	if err=cmd.Start();err!=nil{t.Fatalf("start sing-box: %v",err)}
	defer func(){_=cmd.Process.Kill();_=cmd.Wait()}()
	var c net.Conn
	deadline:=time.Now().Add(8*time.Second)
	for time.Now().Before(deadline){c,err=net.DialTimeout("tcp",input.ListenAddress,200*time.Millisecond);if err==nil{break};time.Sleep(50*time.Millisecond)}
	if c==nil{t.Fatalf("sing-box inbound not ready: %v; logs: %s",err,output.String())}
	defer c.Close();_=c.SetDeadline(time.Now().Add(8*time.Second))
	if _,err=c.Write([]byte{5,1,0});err!=nil{t.Fatal(err)}
	var greeting [2]byte;if _,err=io.ReadFull(c,greeting[:]);err!=nil{t.Fatal(err)}
	if greeting!=[2]byte{5,0}{t.Fatalf("greeting=%v",greeting)}
	host:="route-csqtt.test";req:=[]byte{5,1,0,3,byte(len(host))};req=append(req,[]byte(host)...);req=append(req,1,187)
	if _,err=c.Write(req);err!=nil{t.Fatal(err)}
	var reply [10]byte;if _,err=io.ReadFull(c,reply[:]);err!=nil{t.Fatalf("CONNECT response: %v; logs: %s",err,output.String())}
	if reply[1]!=0{t.Fatalf("CONNECT failed: %v; logs: %s",reply,output.String())}
	payload:=[]byte("real-csqtt-socks5-tcp-e2e");if _,err=c.Write(payload);err!=nil{t.Fatal(err)}
	got:=make([]byte,len(payload));if _,err=io.ReadFull(c,got);err!=nil{t.Fatal(err)}
	if string(got)!=string(payload){t.Fatalf("echo=%q",got)}
	select{case target:=<-targets:if target!="route-csqtt.test:443"{t.Fatalf("CSQTT received %q",target)};case <-time.After(3*time.Second):t.Fatal("real CSQTT SOCKS5 did not receive domain")}
}

func TestSingBoxUDPAssociateRelaysThroughRealCSQTTSocks5Server(t *testing.T) {
	binaryPath:=strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"));if binaryPath==""{t.Skip("requires pinned sing-box binary from CI")}
	echo,err:=net.ListenUDP("udp4",&net.UDPAddr{IP:net.ParseIP("127.0.0.1")});if err!=nil{t.Fatal(err)};defer echo.Close()
	go func(){buf:=make([]byte,2048);for{n,a,e:=echo.ReadFromUDP(buf);if e!=nil{return};_,_=echo.WriteToUDP(append([]byte(nil),buf[:n]...),a)}}()
	csqtt:=NewSocks5Server("127.0.0.1:0")
	csqtt.dialUDPOverride=func()(*net.UDPConn,error){return net.ListenUDP("udp4",&net.UDPAddr{IP:net.ParseIP("127.0.0.1")})}
	if err=csqtt.Start();err!=nil{t.Fatalf("start real CSQTT SOCKS5: %v",err)};defer csqtt.Stop()
	input:=singBoxConfigInput{ListenAddress:reserveSingBoxTestAddress(t),CSQTTAddress:csqtt.Status().Address,FinalOutbound:singBoxDirectTag}
	config,err:=buildSingBoxConfig(input);if err!=nil{t.Fatal(err)}
	path:=filepath.Join(t.TempDir(),"sing-box-udp.json");if err=os.WriteFile(path,config,0600);err!=nil{t.Fatal(err)}
	cmd:=exec.Command(binaryPath,"run","-c",path);output:=&singBoxTestLogBuffer{};cmd.Stdout,cmd.Stderr=output,output
	if err=cmd.Start();err!=nil{t.Fatalf("start sing-box: %v",err)};defer func(){_=cmd.Process.Kill();_=cmd.Wait()}()
	var control net.Conn;deadline:=time.Now().Add(8*time.Second)
	for time.Now().Before(deadline){control,err=net.DialTimeout("tcp",input.ListenAddress,200*time.Millisecond);if err==nil{break};time.Sleep(50*time.Millisecond)}
	if control==nil{t.Fatalf("inbound not ready: %v; logs: %s",err,output.String())};defer control.Close();_=control.SetDeadline(time.Now().Add(10*time.Second))
	if _,err=control.Write([]byte{5,1,0});err!=nil{t.Fatal(err)}
	var greeting [2]byte;if _,err=io.ReadFull(control,greeting[:]);err!=nil{t.Fatal(err)};if greeting!=[2]byte{5,0}{t.Fatalf("greeting=%v",greeting)}
	if _,err=control.Write([]byte{5,3,0,1,0,0,0,0,0,0});err!=nil{t.Fatal(err)}
	var assoc [10]byte;if _,err=io.ReadFull(control,assoc[:]);err!=nil{t.Fatalf("UDP ASSOCIATE: %v; logs: %s",err,output.String())};if assoc[1]!=0{t.Fatalf("UDP ASSOCIATE failed: %v; logs: %s",assoc,output.String())}
	ip:=net.IP(assoc[4:8]);if ip.IsUnspecified(){ip=net.ParseIP("127.0.0.1")};relay:=&net.UDPAddr{IP:ip,Port:int(binary.BigEndian.Uint16(assoc[8:]))}
	client,err:=net.DialUDP("udp4",nil,relay);if err!=nil{t.Fatal(err)};defer client.Close();_=client.SetDeadline(time.Now().Add(8*time.Second))
	dst:=echo.LocalAddr().(*net.UDPAddr);payload:=[]byte("real-csqtt-socks5-udp-associate-e2e")
	packet:=[]byte{0,0,0,1};packet=append(packet,dst.IP.To4()...);var p [2]byte;binary.BigEndian.PutUint16(p[:],uint16(dst.Port));packet=append(packet,p[:]...);packet=append(packet,payload...)
	if _,err=client.Write(packet);err!=nil{t.Fatal(err)}
	response:=make([]byte,2048);n,err:=client.Read(response);if err!=nil{t.Fatalf("UDP response: %v; sing-box logs: %s",err,output.String())}
	if n<10||response[0]!=0||response[1]!=0||response[2]!=0{t.Fatalf("invalid SOCKS UDP response: %v",response[:n])}
	if string(response[n-len(payload):n])!=string(payload){t.Fatalf("UDP payload=%q want=%q",response[n-len(payload):n],payload)}
}

func TestBuildSingBoxConfigPassesPinnedBinaryCheck(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binary == "" {
		t.Skip("CSQTT_SING_BOX_BINARY is not set; real sing-box validation runs in pinned CI")
	}

	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
		CSQTTAddress:  "127.0.0.1:12500",
		FinalOutbound: singBoxDirectTag,
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
			Advanced: map[string]json.RawMessage{
				"network": json.RawMessage(`["tcp","udp"]`),
				"invert": json.RawMessage(`true`),
			},
		}, {
			ID: "logical-a", Outbound: "socks-proxy-a", Enabled: true,
			Advanced: map[string]json.RawMessage{
				"type": json.RawMessage(`"logical"`),
				"mode": json.RawMessage(`"or"`),
				"rules": json.RawMessage(`[{"domain_suffix":["example.org"]},{"protocol":["quic"]}]`),
			},
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

func TestSingBoxRuleSetConfigAndRouteReferences(t *testing.T) {
	input := singBoxConfigInput{ListenAddress:"127.0.0.1:19180", CSQTTAddress:"127.0.0.1:12500", FinalOutbound:singBoxDirectTag}
	input.RuleSets = []singBoxRuleSet{{Type:"local", Tag:"geosite-ru", Format:"binary", Path:"/var/lib/csqtt/sing-box/rule-sets/geosite-ru.srs"}}
	input.Rules = []singBoxRouteRule{{ID:"ru", RuleSets:[]string{"geosite-ru"}, Outbound:"direct", Enabled:true}}
	got, err := buildSingBoxConfig(input)
	if err != nil { t.Fatalf("build config with Rule Set: %v", err) }
	var config map[string]any
	if err := json.Unmarshal(got, &config); err != nil { t.Fatal(err) }
	route := config["route"].(map[string]any)
	sets := route["rule_set"].([]any)
	if len(sets) != 1 || sets[0].(map[string]any)["tag"] != "geosite-ru" { t.Fatalf("unexpected Rule Set config: %#v", sets) }
	rules := route["rules"].([]any)
	found := false
	for _, raw := range rules { rule:=raw.(map[string]any); if rule["outbound"]=="direct" { tags,_:=rule["rule_set"].([]any); if len(tags)==1 && tags[0]=="geosite-ru" { found=true } } }
	if !found { t.Fatalf("route rule does not reference Rule Set: %#v", rules) }
}

func TestSingBoxRuleSetRejectsUnknownAndInsecureSources(t *testing.T) {
	input := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500", FinalOutbound: singBoxDirectTag}
	input.Rules = []singBoxRouteRule{{ID:"bad", RuleSets:[]string{"missing"}, Outbound:"direct", Enabled:true}}
	if _, err := buildSingBoxConfig(input); err == nil { t.Fatal("expected unknown Rule Set rejection") }
	input = singBoxConfigInput{ListenAddress:"127.0.0.1:19180", CSQTTAddress:"127.0.0.1:12500"}
	input.RuleSets = []singBoxRuleSet{{Type:"remote",Tag:"remote",Format:"binary",URL:"http://example.com/rules.srs"}}
	if _, err := buildSingBoxConfig(input); err == nil { t.Fatal("expected HTTP Rule Set rejection") }
}


func TestSingBoxAdvancedRouteFieldsRoundTripThroughLoadAndSave(t *testing.T) {
 dir:=t.TempDir(); config:=filepath.Join(dir,"config.json"); manager:=NewSingBoxManager(newFakeSingBox(t,"exit 0"),config,filepath.Join(dir,"sing-box.log"))
 original:=`{"log":{"level":"info"},"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19180}],"outbounds":[{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":12500,"version":"5"},{"type":"direct","tag":"direct"}],"route":{"rules":[{"inbound":["csqtt-route-in"],"type":"logical","mode":"or","rules":[{"domain_suffix":["example.org"]},{"protocol":["quic"]}],"action":"route","outbound":"csqtt","invert":true}],"final":"csqtt","rule_set":[]}}`
 if err:=os.WriteFile(config,[]byte(original),0600);err!=nil{t.Fatal(err)}
 loaded,err:=manager.LoadConfig();if err!=nil{t.Fatal(err)};if len(loaded.Rules)!=1||len(loaded.Rules[0].Advanced)!=4{t.Fatalf("advanced fields not loaded: %#v",loaded.Rules)}
 if err:=manager.SaveConfig(loaded);err!=nil{t.Fatal(err)};data,err:=os.ReadFile(config);if err!=nil{t.Fatal(err)}
 var got map[string]any;if err:=json.Unmarshal(data,&got);err!=nil{t.Fatal(err)};rules:=got["route"].(map[string]any)["rules"].([]any);var advanced map[string]any
 for _,candidate:=range rules{rule:=candidate.(map[string]any);if rule["type"]=="logical"{advanced=rule;break}}
 if advanced==nil||advanced["invert"]!=true{t.Fatalf("advanced logical fields lost: %#v",rules)}
 if advanced["mode"]!="or"||len(advanced["rules"].([]any))!=2{t.Fatalf("logical matcher changed: %#v",advanced)}
}

func TestSingBoxAdvancedInvertAloneIsNotAMatcher(t *testing.T) {
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
		CSQTTAddress:  "127.0.0.1:12500",
		FinalOutbound: singBoxDirectTag,
		Rules: []singBoxRouteRule{{
			ID: "invert-only", Outbound: singBoxCSQTTTag, Enabled: true,
			Advanced: map[string]json.RawMessage{"invert": json.RawMessage("true")},
		}},
	}
	if _, err := buildSingBoxConfig(input); err == nil || !strings.Contains(err.Error(), "has no matchers") {
		t.Fatalf("invert-only route must be rejected as matcherless, got %v", err)
	}
}

func TestSingBoxAdvancedRouteFieldsRejectControlPlaneOverrides(t *testing.T) {
 for _,key:=range []string{"action","outbound","inbound","auto_route","final","logical"}{input:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500",Rules:[]singBoxRouteRule{{ID:"safe",Domains:[]string{"example.org"},Outbound:singBoxCSQTTTag,Enabled:true,Advanced:map[string]json.RawMessage{key:json.RawMessage(`"unsafe"`)}}}};if _,err:=buildSingBoxConfig(input);err==nil{t.Fatalf("unsafe advanced field %q accepted",key)}}
}

func TestBuildSingBoxConfigSupportsExternalDefaultWithoutCSQTT(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
		FinalOutbound: "socks-hydra",
		SOCKS5: []singBoxSOCKS5Outbound{{ID:"hydra", Server:"192.0.2.10", ServerPort:1080, Username:"user", Password:"secret", Enabled:true}},
		Rules: []singBoxRouteRule{{ID:"ru-direct", Domains:[]string{"ru.example"}, Outbound:singBoxDirectTag, Enabled:true}},
	})
	if err != nil { t.Fatalf("external SOCKS fallback without CSQTT rejected: %v", err) }
	var config map[string]any
	if err := json.Unmarshal(got, &config); err != nil { t.Fatal(err) }
	outbounds := config["outbounds"].([]any)
	for _, raw := range outbounds {
		entry := raw.(map[string]any)
		if entry["tag"] == singBoxCSQTTTag { t.Fatal("disabled CSQTT outbound was emitted") }
		if entry["tag"] == "socks-hydra" {
			if entry["server"] != "192.0.2.10" || entry["username"] != "user" || entry["password"] != "secret" { t.Fatalf("external SOCKS credentials/endpoint lost: %#v", entry) }
		}
	}
	if gotFinal := config["route"].(map[string]any)["final"]; gotFinal != "socks-hydra" { t.Fatalf("route.final=%v, want socks-hydra", gotFinal) }
}

func TestBuildSingBoxConfigRejectsMissingFallbackWhenCSQTTDisabled(t *testing.T) {
	_, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19180"})
	if err == nil || !strings.Contains(err.Error(), "select a default outbound") { t.Fatalf("missing explicit fallback error = %v", err) }
}


func TestSingBoxRoutesDomainThroughAuthenticatedExternalSOCKS5WithoutCSQTT(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() {
		for {
			conn, acceptErr := echo.Accept()
			if acceptErr != nil { return }
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer upstream.Close()
	authenticated := make(chan bool, 1)
	targets := make(chan string, 1)
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil { return }
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		var greeting [2]byte
		if _, e := io.ReadFull(conn, greeting[:]); e != nil { return }
		methods := make([]byte, int(greeting[1]))
		if _, e := io.ReadFull(conn, methods); e != nil { return }
		if greeting[0] != 5 { _, _ = conn.Write([]byte{5, 255}); return }
		if _, e := conn.Write([]byte{5, 2}); e != nil { return }
		var authHead [2]byte
		if _, e := io.ReadFull(conn, authHead[:]); e != nil { return }
		user := make([]byte, int(authHead[1]))
		if _, e := io.ReadFull(conn, user); e != nil { return }
		var passLen [1]byte
		if _, e := io.ReadFull(conn, passLen[:]); e != nil { return }
		pass := make([]byte, int(passLen[0]))
		if _, e := io.ReadFull(conn, pass); e != nil { return }
		ok := authHead[0] == 1 && string(user) == "hydra-user" && string(pass) == "hydra-pass"
		authenticated <- ok
		if ok { _, _ = conn.Write([]byte{1, 0}) } else { _, _ = conn.Write([]byte{1, 1}); return }

		var request [4]byte
		if _, e := io.ReadFull(conn, request[:]); e != nil { return }
		var host string
		switch request[3] {
		case 1:
			var ip [4]byte
			if _, e := io.ReadFull(conn, ip[:]); e != nil { return }
			host = net.IP(ip[:]).String()
		case 3:
			var size [1]byte
			if _, e := io.ReadFull(conn, size[:]); e != nil { return }
			name := make([]byte, int(size[0]))
			if _, e := io.ReadFull(conn, name); e != nil { return }
			host = string(name)
		default:
			return
		}
		var portBytes [2]byte
		if _, e := io.ReadFull(conn, portBytes[:]); e != nil { return }
		target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes[:]))))
		targets <- target
		if request[0] != 5 || request[1] != 1 { return }
		remote, dialErr := net.DialTimeout("tcp", echo.Addr().String(), 2*time.Second)
		if dialErr != nil { _, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}); return }
		defer remote.Close()
		if _, e := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); e != nil { return }
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(remote, conn); done <- struct{}{} }()
		go func() { _, _ = io.Copy(conn, remote); done <- struct{}{} }()
		<-done
	}()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		FinalOutbound: "socks-hydra",
		SOCKS5: []singBoxSOCKS5Outbound{{ID: "hydra", Server: "127.0.0.1", ServerPort: upstream.Addr().(*net.TCPAddr).Port, Username: "hydra-user", Password: "hydra-pass", Enabled: true}},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	var decoded map[string]any
	if err := json.Unmarshal(config, &decoded); err != nil { t.Fatal(err) }
	for _, raw := range decoded["outbounds"].([]any) {
		if raw.(map[string]any)["tag"] == singBoxCSQTTTag { t.Fatal("CSQTT outbound unexpectedly present") }
	}
	if got := decoded["route"].(map[string]any)["final"]; got != "socks-hydra" { t.Fatalf("route.final=%v", got) }

	configPath := filepath.Join(t.TempDir(), "sing-box-external-socks.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	var client net.Conn
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		client, err = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
		if err == nil { break }
		time.Sleep(50 * time.Millisecond)
	}
	if client == nil { t.Fatalf("sing-box inbound not ready: %v; logs: %s", err, output.String()) }
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := client.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
	var greeting [2]byte
	if _, err := io.ReadFull(client, greeting[:]); err != nil { t.Fatal(err) }
	if greeting != [2]byte{5, 0} { t.Fatalf("inbound greeting=%v", greeting) }

	host := "external-socks-route.test"
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, []byte(host)...)
	request = append(request, 1, 187)
	if _, err := client.Write(request); err != nil { t.Fatal(err) }
	var reply [10]byte
	if _, err := io.ReadFull(client, reply[:]); err != nil { t.Fatalf("external SOCKS CONNECT: %v; logs: %s", err, output.String()) }
	if reply[1] != 0 { t.Fatalf("external SOCKS CONNECT reply=%v; logs: %s", reply, output.String()) }
	payload := []byte("authenticated-external-socks5-without-csqtt")
	if _, err := client.Write(payload); err != nil { t.Fatal(err) }
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil { t.Fatal(err) }
	if string(got) != string(payload) { t.Fatalf("echo=%q want=%q", got, payload) }

	select {
	case ok := <-authenticated:
		if !ok { t.Fatal("external SOCKS5 did not receive configured username/password") }
	case <-time.After(3 * time.Second):
		t.Fatal("external SOCKS5 authentication handshake was not observed")
	}
	select {
	case target := <-targets:
		if target != "external-socks-route.test:443" { t.Fatalf("external SOCKS5 received target %q", target) }
	case <-time.After(3 * time.Second):
		t.Fatal("external SOCKS5 did not receive the routed destination")
	}
}


// TestSingBoxUDPAssociateRelaysThroughAuthenticatedExternalSOCKS5 verifies
// that the pinned sing-box build negotiates RFC 1929 credentials, establishes
// an upstream UDP ASSOCIATE, and relays an actual datagram in both directions.
func TestSingBoxDoesNotFallbackWhenExternalSOCKS5AuthenticationFails(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() {
		for {
			conn, acceptErr := echo.Accept()
			if acceptErr != nil { return }
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer upstream.Close()
	authSeen := make(chan bool, 1)
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil { authSeen <- false; return }
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		var greeting [2]byte
		if _, e := io.ReadFull(conn, greeting[:]); e != nil { authSeen <- false; return }
		methods := make([]byte, int(greeting[1]))
		if _, e := io.ReadFull(conn, methods); e != nil { authSeen <- false; return }
		if greeting[0] != 5 { authSeen <- false; return }
		if _, e := conn.Write([]byte{5, 2}); e != nil { authSeen <- false; return }
		var authHead [2]byte
		if _, e := io.ReadFull(conn, authHead[:]); e != nil { authSeen <- false; return }
		user := make([]byte, int(authHead[1]))
		if _, e := io.ReadFull(conn, user); e != nil { authSeen <- false; return }
		var passLen [1]byte
		if _, e := io.ReadFull(conn, passLen[:]); e != nil { authSeen <- false; return }
		pass := make([]byte, int(passLen[0]))
		if _, e := io.ReadFull(conn, pass); e != nil { authSeen <- false; return }
		credentialsArrived := authHead[0] == 1 && string(user) == "bad-user" && string(pass) == "bad-pass"
		authSeen <- credentialsArrived
		// RFC 1929: reject the configured credentials. No CONNECT is accepted.
		_, _ = conn.Write([]byte{1, 1})
	}()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		FinalOutbound: "socks-reject",
		SOCKS5: []singBoxSOCKS5Outbound{{ID:"reject",Server:"127.0.0.1",ServerPort:upstream.Addr().(*net.TCPAddr).Port,Username:"bad-user",Password:"bad-pass",Enabled:true}},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	configPath := filepath.Join(t.TempDir(), "sing-box-auth-reject.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	var client net.Conn
	deadline := time.Now().Add(8*time.Second)
	for time.Now().Before(deadline) {
		client, err = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
		if err == nil { break }
		time.Sleep(50*time.Millisecond)
	}
	if client == nil { t.Fatalf("sing-box inbound not ready: %v; logs: %s",err,output.String()) }
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(8*time.Second))
	if _,err:=client.Write([]byte{5,1,0});err!=nil{t.Fatal(err)}
	var greeting [2]byte
	if _,err:=io.ReadFull(client,greeting[:]);err!=nil{t.Fatal(err)}
	if greeting != [2]byte{5,0} { t.Fatalf("inbound greeting=%v",greeting) }

	port:=echo.Addr().(*net.TCPAddr).Port
	request:=[]byte{5,1,0,1,127,0,0,1,byte(port>>8),byte(port)}
	if _,err:=client.Write(request);err!=nil{t.Fatal(err)}
	var reply [10]byte
	_,replyErr:=io.ReadFull(client,reply[:])
	if replyErr==nil && reply[1]==0 {
		// A successful local SOCKS CONNECT reply alone is not enough to prove
		// that traffic bypassed the rejected upstream: verify end-to-end payload
		// delivery to the local echo target before declaring a fallback.
		probe := []byte("must-not-bypass-selected-outbound")
		if _, err := client.Write(probe); err != nil {
			t.Fatalf("write fallback probe after successful CONNECT reply: %v; logs: %s", err, output.String())
		}
		echoed := make([]byte, len(probe))
		if _, err := io.ReadFull(client, echoed); err == nil && bytes.Equal(echoed, probe) {
			t.Fatal("payload reached the direct local echo target after upstream SOCKS5 authentication rejection; selected outbound was bypassed")
		}
	}
	select {
	case ok:=<-authSeen:
		if !ok { t.Fatalf("upstream did not receive configured credentials; logs: %s",output.String()) }
	case <-time.After(3*time.Second):
		t.Fatalf("upstream authentication attempt was not observed; logs: %s",output.String())
	}
}

func TestSingBoxUDPAssociateRelaysThroughAuthenticatedExternalSOCKS5(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" {
		t.Skip("requires pinned sing-box binary from CI")
	}

	relay, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()

	authenticated := make(chan bool, 1)
	associate := make(chan bool, 1)
	relayErrors := make(chan error, 1)
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil {
			relayErrors <- acceptErr
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(12 * time.Second))

		var greeting [2]byte
		if _, e := io.ReadFull(conn, greeting[:]); e != nil {
			relayErrors <- e
			return
		}
		methods := make([]byte, int(greeting[1]))
		if _, e := io.ReadFull(conn, methods); e != nil {
			relayErrors <- e
			return
		}
		if greeting[0] != 5 {
			_, _ = conn.Write([]byte{5, 255})
			relayErrors <- fmt.Errorf("unexpected SOCKS version %d", greeting[0])
			return
		}
		if _, e := conn.Write([]byte{5, 2}); e != nil {
			relayErrors <- e
			return
		}

		var authHead [2]byte
		if _, e := io.ReadFull(conn, authHead[:]); e != nil {
			relayErrors <- e
			return
		}
		user := make([]byte, int(authHead[1]))
		if _, e := io.ReadFull(conn, user); e != nil {
			relayErrors <- e
			return
		}
		var passLen [1]byte
		if _, e := io.ReadFull(conn, passLen[:]); e != nil {
			relayErrors <- e
			return
		}
		pass := make([]byte, int(passLen[0]))
		if _, e := io.ReadFull(conn, pass); e != nil {
			relayErrors <- e
			return
		}
		ok := authHead[0] == 1 && string(user) == "udp-user" && string(pass) == "udp-pass"
		authenticated <- ok
		if ok {
			_, _ = conn.Write([]byte{1, 0})
		} else {
			_, _ = conn.Write([]byte{1, 1})
			return
		}

		var request [4]byte
		if _, e := io.ReadFull(conn, request[:]); e != nil {
			relayErrors <- e
			return
		}
		if request[0] != 5 || request[1] != 3 || request[2] != 0 {
			_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
			associate <- false
			return
		}
		switch request[3] {
		case 1:
			var address [4]byte
			if _, e := io.ReadFull(conn, address[:]); e != nil {
				relayErrors <- e
				return
			}
		case 3:
			var size [1]byte
			if _, e := io.ReadFull(conn, size[:]); e != nil {
				relayErrors <- e
				return
			}
			name := make([]byte, int(size[0]))
			if _, e := io.ReadFull(conn, name); e != nil {
				relayErrors <- e
				return
			}
		case 4:
			var address [16]byte
			if _, e := io.ReadFull(conn, address[:]); e != nil {
				relayErrors <- e
				return
			}
		default:
			relayErrors <- fmt.Errorf("unsupported upstream UDP ASSOCIATE address type %d", request[3])
			return
		}
		var port [2]byte
		if _, e := io.ReadFull(conn, port[:]); e != nil {
			relayErrors <- e
			return
		}

		bound := relay.LocalAddr().(*net.UDPAddr)
		response := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}
		binary.BigEndian.PutUint16(response[8:10], uint16(bound.Port))
		if _, e := conn.Write(response); e != nil {
			relayErrors <- e
			return
		}
		associate <- true

		buffer := make([]byte, 2048)
		n, source, readErr := relay.ReadFromUDP(buffer)
		if readErr != nil {
			relayErrors <- readErr
			return
		}
		packet := append([]byte(nil), buffer[:n]...)
		if len(packet) < 10 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
			relayErrors <- fmt.Errorf("invalid upstream SOCKS UDP packet: %v", packet)
			return
		}
		if packet[3] == 3 {
			if len(packet) < 5 || int(packet[4])+7 > len(packet) {
				relayErrors <- fmt.Errorf("truncated upstream domain UDP packet")
				return
			}
			host := string(packet[5 : 5+int(packet[4])])
			if host != "udp-external-route.test" {
				relayErrors <- fmt.Errorf("upstream UDP destination = %q", host)
				return
			}
		}
		if _, e := relay.WriteToUDP(packet, source); e != nil {
			relayErrors <- e
			return
		}
	}()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		FinalOutbound: "socks-udp",
		SOCKS5: []singBoxSOCKS5Outbound{{
			ID: "udp", Server: "127.0.0.1",
			ServerPort: upstream.Addr().(*net.TCPAddr).Port,
			Username: "udp-user", Password: "udp-pass", Enabled: true,
		}},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "sing-box-external-socks-udp.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sing-box: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	var control net.Conn
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		control, err = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if control == nil {
		t.Fatalf("sing-box inbound not ready: %v; logs: %s", err, output.String())
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(12 * time.Second))
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(control, greeting[:]); err != nil {
		t.Fatal(err)
	}
	if greeting != [2]byte{5, 0} {
		t.Fatalf("inbound greeting = %v", greeting)
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var response [10]byte
	if _, err := io.ReadFull(control, response[:]); err != nil {
		t.Fatalf("inbound UDP ASSOCIATE: %v; sing-box logs: %s", err, output.String())
	}
	if response[1] != 0 {
		t.Fatalf("inbound UDP ASSOCIATE reply = %v; sing-box logs: %s", response, output.String())
	}

	relayIP := net.IP(response[4:8])
	if relayIP.IsUnspecified() {
		relayIP = net.ParseIP("127.0.0.1")
	}
	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP: relayIP, Port: int(binary.BigEndian.Uint16(response[8:10])),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(8 * time.Second))

	host := "udp-external-route.test"
	payload := []byte("authenticated-external-socks5-udp-associate")
	packet := []byte{0, 0, 0, 3, byte(len(host))}
	packet = append(packet, []byte(host)...)
	packet = append(packet, 0x01, 0xbb)
	packet = append(packet, payload...)
	if _, err := client.Write(packet); err != nil {
		t.Fatal(err)
	}

	// sing-box opens the upstream SOCKS5 UDP association lazily, when the
	// first datagram arrives. Trigger traffic before waiting for auth/ASSOCIATE.
	select {
	case ok := <-authenticated:
		if !ok {
			t.Fatal("external SOCKS5 received incorrect UDP credentials")
		}
	case e := <-relayErrors:
		t.Fatalf("upstream authentication failed: %v", e)
	case <-time.After(4 * time.Second):
		t.Fatalf("external SOCKS5 authentication was not observed; sing-box logs: %s", output.String())
	}
	select {
	case ok := <-associate:
		if !ok {
			t.Fatal("external SOCKS5 rejected UDP ASSOCIATE")
		}
	case e := <-relayErrors:
		t.Fatalf("upstream UDP ASSOCIATE failed: %v; logs: %s", e, output.String())
	case <-time.After(4 * time.Second):
		t.Fatalf("external SOCKS5 UDP ASSOCIATE was not observed; sing-box logs: %s", output.String())
	}
	got := make([]byte, 2048)
	n, err := client.Read(got)
	if err != nil {
		select {
		case relayErr := <-relayErrors:
			t.Fatalf("upstream UDP relay: %v; sing-box logs: %s", relayErr, output.String())
		default:
			t.Fatalf("UDP response: %v; sing-box logs: %s", err, output.String())
		}
	}
	if n < len(payload) || string(got[n-len(payload):n]) != string(payload) {
		t.Fatalf("UDP payload = %q, want %q; sing-box logs: %s", got[:n], payload, output.String())
	}
}


// TestSingBoxDoesNotFallbackWhenExternalSOCKS5UpstreamResets verifies that a
// selected external SOCKS5 outbound fails closed when its TCP upstream resets
// immediately after accepting the connection. A local echo target detects any
// accidental direct fallback by observing end-to-end payload delivery.
func TestSingBoxDoesNotFallbackWhenExternalSOCKS5UpstreamResets(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" {
		t.Skip("requires pinned sing-box binary from CI")
	}

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, acceptErr := echo.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstreamAccepted := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		upstreamAccepted <- struct{}{}
		_ = upstream.Close() // Refuse subsequent retries rather than queueing them.
		_ = conn.Close() // Simulate an upstream reset before SOCKS negotiation.
	}()
	defer upstream.Close()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		FinalOutbound: "socks-reset",
		SOCKS5: []singBoxSOCKS5Outbound{{
			ID: "reset", Server: "127.0.0.1",
			ServerPort: upstream.Addr().(*net.TCPAddr).Port, Enabled: true,
		}},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "sing-box-upstream-reset.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sing-box: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	var client net.Conn
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		client, err = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("sing-box inbound not ready: %v; logs: %s", err, output.String())
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := client.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(client, greeting[:]); err != nil {
		t.Fatal(err)
	}
	if greeting != [2]byte{5, 0} {
		t.Fatalf("inbound greeting=%v", greeting)
	}

	port := echo.Addr().(*net.TCPAddr).Port
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	_, replyErr := io.ReadFull(client, reply[:])
	if replyErr == nil && reply[1] == 0 {
		probe := []byte("must-not-bypass-reset-external-socks5")
		if _, err := client.Write(probe); err != nil {
			t.Fatalf("write fallback probe: %v; logs: %s", err, output.String())
		}
		echoed := make([]byte, len(probe))
		if _, err := io.ReadFull(client, echoed); err == nil && bytes.Equal(echoed, probe) {
			t.Fatal("payload reached direct echo target after external SOCKS5 upstream reset; selected outbound was bypassed")
		}
	}

	select {
	case <-upstreamAccepted:
	case <-time.After(3 * time.Second):
		t.Fatalf("sing-box never attempted the selected external SOCKS5 upstream; logs: %s", output.String())
	}
}

// TestSingBoxSwitchesBetweenCSQTTAndDirectForRealTCPFlows verifies that
// ordered rules select distinct outbounds for separate connections through the
// same SOCKS inbound. The CSQTT server records only its own routed destination;
// the Direct flow reaches the echo target without touching CSQTT.
func TestSingBoxSwitchesBetweenCSQTTAndDirectForRealTCPFlows(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() {
		for {
			conn, acceptErr := echo.Accept()
			if acceptErr != nil { return }
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	targets := make(chan string, 4)
	csqtt := NewSocks5Server("127.0.0.1:0")
	csqtt.dialTCPOverride = func(target string) (net.Conn, error) {
		select { case targets <- target: default: }
		return net.DialTimeout("tcp", echo.Addr().String(), 2*time.Second)
	}
	if err := csqtt.Start(); err != nil { t.Fatalf("start CSQTT SOCKS5: %v", err) }
	defer csqtt.Stop()

	echoPort := echo.Addr().(*net.TCPAddr).Port
	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		CSQTTAddress: csqtt.Status().Address,
		FinalOutbound: singBoxDirectTag,
		Rules: []singBoxRouteRule{
			{ID:"route-via-csqtt", Domains:[]string{"via-csqtt.test"}, Outbound:singBoxCSQTTTag, Enabled:true},
			{ID:"route-direct-ip", IPCIDRs:[]string{"127.0.0.1/32"}, Outbound:singBoxDirectTag, Enabled:true},
		},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	configPath := filepath.Join(t.TempDir(), "sing-box-switch.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	dialInbound := func() net.Conn {
		t.Helper()
		var client net.Conn
		var dialErr error
		deadline := time.Now().Add(8*time.Second)
		for time.Now().Before(deadline) {
			client, dialErr = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
			if dialErr == nil { break }
			time.Sleep(50*time.Millisecond)
		}
		if client == nil { t.Fatalf("sing-box inbound not ready: %v; logs: %s", dialErr, output.String()) }
		_ = client.SetDeadline(time.Now().Add(8*time.Second))
		if _, err := client.Write([]byte{5,1,0}); err != nil { t.Fatal(err) }
		var greeting [2]byte
		if _, err := io.ReadFull(client, greeting[:]); err != nil { t.Fatal(err) }
		if greeting != [2]byte{5,0} { t.Fatalf("SOCKS greeting=%v", greeting) }
		return client
	}
	connectAndEcho := func(host string, port int, domain bool, payload string) {
		t.Helper()
		client := dialInbound()
		defer client.Close()
		request := []byte{5,1,0}
		if domain {
			request = append(request, 3, byte(len(host)))
			request = append(request, []byte(host)...)
		} else {
			ip := net.ParseIP(host).To4()
			if ip == nil { t.Fatalf("invalid test IPv4 %q", host) }
			request = append(request, 1)
			request = append(request, ip...)
		}
		var portBytes [2]byte
		binary.BigEndian.PutUint16(portBytes[:], uint16(port))
		request = append(request, portBytes[:]...)
		if _, err := client.Write(request); err != nil { t.Fatal(err) }
		var reply [10]byte
		if _, err := io.ReadFull(client, reply[:]); err != nil { t.Fatalf("CONNECT %s: %v; logs: %s", host, err, output.String()) }
		if reply[1] != 0 { t.Fatalf("CONNECT %s rejected: %v; logs: %s", host, reply, output.String()) }
		data := []byte(payload)
		if _, err := client.Write(data); err != nil { t.Fatal(err) }
		got := make([]byte, len(data))
		if _, err := io.ReadFull(client, got); err != nil { t.Fatalf("echo %s: %v", host, err) }
		if !bytes.Equal(got, data) { t.Fatalf("echo for %s=%q, want %q", host, got, data) }
	}

	connectAndEcho("via-csqtt.test", echoPort, true, "flow-selected-csqtt")
	select {
	case target := <-targets:
		if target != net.JoinHostPort("via-csqtt.test", strconv.Itoa(echoPort)) { t.Fatalf("CSQTT received %q", target) }
	case <-time.After(3*time.Second):
		t.Fatal("CSQTT did not receive the domain-routed flow")
	}

	connectAndEcho("127.0.0.1", echoPort, false, "flow-selected-direct")
	select {
	case target := <-targets:
		t.Fatalf("Direct flow unexpectedly traversed CSQTT as %q", target)
	case <-time.After(300*time.Millisecond):
		// No CSQTT dial is the expected proof that the second rule selected Direct.
	}
}

// TestSingBoxSelectsBetweenTwoExternalSOCKS5OutboundsForRealTCPFlows proves
// that two ordered domain rules select different external SOCKS5 upstreams.
// Each fake upstream records the requested destination and relays real payload.
func TestSingBoxSelectsBetweenTwoExternalSOCKS5OutboundsForRealTCPFlows(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer echo.Close()
	go func() {
		for {
			conn, acceptErr := echo.Accept()
			if acceptErr != nil { return }
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	startUpstream := func(name string) (net.Listener, <-chan string) {
		t.Helper()
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil { t.Fatal(listenErr) }
		targets := make(chan string, 2)
		go func() {
			for {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil { return }
				go func(c net.Conn) {
					defer c.Close()
					_ = c.SetDeadline(time.Now().Add(10*time.Second))
					var greeting [2]byte
					if _, e := io.ReadFull(c, greeting[:]); e != nil { return }
					methods := make([]byte, int(greeting[1]))
					if _, e := io.ReadFull(c, methods); e != nil { return }
					if greeting[0] != 5 { return }
					if _, e := c.Write([]byte{5, 0}); e != nil { return }

					var request [4]byte
					if _, e := io.ReadFull(c, request[:]); e != nil { return }
					if request[0] != 5 || request[1] != 1 { return }
					var host string
					switch request[3] {
					case 1:
						var ip [4]byte
						if _, e := io.ReadFull(c, ip[:]); e != nil { return }
						host = net.IP(ip[:]).String()
					case 3:
						var size [1]byte
						if _, e := io.ReadFull(c, size[:]); e != nil { return }
						domain := make([]byte, int(size[0]))
						if _, e := io.ReadFull(c, domain); e != nil { return }
						host = string(domain)
					default:
						return
					}
					var portBytes [2]byte
					if _, e := io.ReadFull(c, portBytes[:]); e != nil { return }
					target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes[:]))))
					select { case targets <- target: default: }
					remote, dialErr := net.DialTimeout("tcp", echo.Addr().String(), 2*time.Second)
					if dialErr != nil { _, _ = c.Write([]byte{5,5,0,1,0,0,0,0,0,0}); return }
					defer remote.Close()
					if _, e := c.Write([]byte{5,0,0,1,127,0,0,1,0,0}); e != nil { return }
					done := make(chan struct{}, 2)
					go func() { _, _ = io.Copy(remote, c); done <- struct{}{} }()
					go func() { _, _ = io.Copy(c, remote); done <- struct{}{} }()
					<-done
				}(conn)
			}
		}()
		_ = name // retained in the call site to make upstream identity explicit.
		return listener, targets
	}

	upstreamA, targetsA := startUpstream("proxy-a")
	defer upstreamA.Close()
	upstreamB, targetsB := startUpstream("proxy-b")
	defer upstreamB.Close()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		FinalOutbound: singBoxDirectTag,
		SOCKS5: []singBoxSOCKS5Outbound{
			{ID:"proxy-a", Server:"127.0.0.1", ServerPort:upstreamA.Addr().(*net.TCPAddr).Port, Enabled:true},
			{ID:"proxy-b", Server:"127.0.0.1", ServerPort:upstreamB.Addr().(*net.TCPAddr).Port, Enabled:true},
		},
		Rules: []singBoxRouteRule{
			{ID:"route-proxy-a", Domains:[]string{"via-proxy-a.test"}, Outbound:"socks-proxy-a", Enabled:true},
			{ID:"route-proxy-b", Domains:[]string{"via-proxy-b.test"}, Outbound:"socks-proxy-b", Enabled:true},
		},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	configPath := filepath.Join(t.TempDir(), "sing-box-two-external.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	dialInbound := func() net.Conn {
		t.Helper()
		var client net.Conn
		var dialErr error
		deadline := time.Now().Add(8*time.Second)
		for time.Now().Before(deadline) {
			client, dialErr = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
			if dialErr == nil { break }
			time.Sleep(50*time.Millisecond)
		}
		if client == nil { t.Fatalf("sing-box inbound not ready: %v; logs: %s", dialErr, output.String()) }
		_ = client.SetDeadline(time.Now().Add(8*time.Second))
		if _, err := client.Write([]byte{5,1,0}); err != nil { t.Fatal(err) }
		var greeting [2]byte
		if _, err := io.ReadFull(client, greeting[:]); err != nil { t.Fatal(err) }
		if greeting != [2]byte{5,0} { t.Fatalf("SOCKS greeting=%v", greeting) }
		return client
	}
	connectAndEcho := func(host, payload string) {
		t.Helper()
		client := dialInbound()
		defer client.Close()
		request := []byte{5,1,0,3,byte(len(host))}
		request = append(request, []byte(host)...)
		var portBytes [2]byte
		binary.BigEndian.PutUint16(portBytes[:], uint16(echo.Addr().(*net.TCPAddr).Port))
		request = append(request, portBytes[:]...)
		if _, err := client.Write(request); err != nil { t.Fatal(err) }
		var reply [10]byte
		if _, err := io.ReadFull(client, reply[:]); err != nil { t.Fatalf("CONNECT %s: %v; logs: %s", host, err, output.String()) }
		if reply[1] != 0 { t.Fatalf("CONNECT %s rejected: %v; logs: %s", host, reply, output.String()) }
		data := []byte(payload)
		if _, err := client.Write(data); err != nil { t.Fatal(err) }
		got := make([]byte, len(data))
		if _, err := io.ReadFull(client, got); err != nil { t.Fatalf("echo %s: %v", host, err) }
		if !bytes.Equal(got, data) { t.Fatalf("echo %s=%q, want %q", host, got, data) }
	}
	assertTarget := func(name string, targets <-chan string, want string) {
		t.Helper()
		select {
		case got := <-targets:
			if got != want { t.Fatalf("%s received target %q, want %q", name, got, want) }
		case <-time.After(3*time.Second):
			t.Fatalf("%s did not receive its routed flow; logs: %s", name, output.String())
		}
	}

	connectAndEcho("via-proxy-a.test", "payload-through-proxy-a")
	assertTarget("proxy-a", targetsA, "via-proxy-a.test:"+strconv.Itoa(echo.Addr().(*net.TCPAddr).Port))
	select {
	case got := <-targetsB: t.Fatalf("proxy-b unexpectedly received proxy-a flow %q", got)
	case <-time.After(200*time.Millisecond):
	}

	connectAndEcho("via-proxy-b.test", "payload-through-proxy-b")
	assertTarget("proxy-b", targetsB, "via-proxy-b.test:"+strconv.Itoa(echo.Addr().(*net.TCPAddr).Port))
	select {
	case got := <-targetsA: t.Fatalf("proxy-a unexpectedly received proxy-b flow %q", got)
	case <-time.After(200*time.Millisecond):
	}
}



// TestSingBoxSniffsHTTPHostAndTLSSNIFromIPDestination verifies domain routing
// when SOCKS5 CONNECT carries only an IP and the hostname exists only in the
// application payload. Both flows run through the pinned real sing-box binary.
func TestSingBoxSniffsHTTPHostAndTLSSNIFromIPDestination(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	startUpstream := func() (net.Listener, <-chan string) {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil { t.Fatal(err) }
		selected := make(chan string, 4)
		go func() {
			for {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil { return }
				go func(c net.Conn) {
					defer c.Close()
					_ = c.SetDeadline(time.Now().Add(10*time.Second))
					var greeting [2]byte
					if _, e := io.ReadFull(c, greeting[:]); e != nil { return }
					methods := make([]byte, int(greeting[1]))
					if _, e := io.ReadFull(c, methods); e != nil { return }
					if greeting[0] != 5 { return }
					if _, e := c.Write([]byte{5, 0}); e != nil { return }
					var request [4]byte
					if _, e := io.ReadFull(c, request[:]); e != nil { return }
					if request[0] != 5 || request[1] != 1 { return }
					var host string
					switch request[3] {
					case 1:
						var ip [4]byte
						if _, e := io.ReadFull(c, ip[:]); e != nil { return }
						host = net.IP(ip[:]).String()
					case 3:
						var size [1]byte
						if _, e := io.ReadFull(c, size[:]); e != nil { return }
						domain := make([]byte, int(size[0]))
						if _, e := io.ReadFull(c, domain); e != nil { return }
						host = string(domain)
					default:
						return
					}
					var port [2]byte
					if _, e := io.ReadFull(c, port[:]); e != nil { return }
					select { case selected <- net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))): default: }
					_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
					_, _ = io.Copy(io.Discard, c)
				}(conn)
			}
		}()
		return listener, selected
	}

	httpProxy, httpSelected := startUpstream()
	defer httpProxy.Close()
	tlsProxy, tlsSelected := startUpstream()
	defer tlsProxy.Close()
	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		FinalOutbound: singBoxDirectTag,
		SOCKS5: []singBoxSOCKS5Outbound{
			{ID: "http-sniff", Server: "127.0.0.1", ServerPort: httpProxy.Addr().(*net.TCPAddr).Port, Enabled: true},
			{ID: "tls-sniff", Server: "127.0.0.1", ServerPort: tlsProxy.Addr().(*net.TCPAddr).Port, Enabled: true},
		},
		Rules: []singBoxRouteRule{
			{ID: "http-host", Domains: []string{"sniff-http.test"}, Outbound: "socks-http-sniff", Enabled: true},
			{ID: "tls-sni", Domains: []string{"sniff-tls.test"}, Outbound: "socks-tls-sniff", Enabled: true},
		},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	configPath := filepath.Join(t.TempDir(), "sing-box-sniff.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	dialSOCKSIP := func(port int) net.Conn {
		t.Helper()
		var conn net.Conn
		var dialErr error
		deadline := time.Now().Add(8*time.Second)
		for time.Now().Before(deadline) {
			conn, dialErr = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
			if dialErr == nil { break }
			time.Sleep(50*time.Millisecond)
		}
		if conn == nil { t.Fatalf("sing-box inbound not ready: %v; logs: %s", dialErr, output.String()) }
		_ = conn.SetDeadline(time.Now().Add(8*time.Second))
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil { t.Fatal(err) }
		if greeting != [2]byte{5, 0} { t.Fatalf("SOCKS greeting=%v", greeting) }
		request := []byte{5, 1, 0, 1, 192, 0, 2, 10, byte(port >> 8), byte(port)}
		if _, err := conn.Write(request); err != nil { t.Fatal(err) }
		var reply [10]byte
		if _, err := io.ReadFull(conn, reply[:]); err != nil { t.Fatalf("SOCKS CONNECT to IP: %v; logs: %s", err, output.String()) }
		if reply[1] != 0 { t.Fatalf("SOCKS CONNECT to IP rejected: %v; logs: %s", reply, output.String()) }
		return conn
	}
	assertSelected := func(name string, selected <-chan string) {
		t.Helper()
		select {
		case got := <-selected:
			if got == "" { t.Fatalf("%s sniff route reached upstream with an empty target", name) }
		case <-time.After(4*time.Second):
			t.Fatalf("%s Host/SNI did not select its SOCKS5 outbound; logs: %s", name, output.String())
		}
	}

	httpConn := dialSOCKSIP(80)
	if _, err := io.WriteString(httpConn, "GET / HTTP/1.1\r\nHost: sniff-http.test\r\nConnection: close\r\n\r\n"); err != nil { t.Fatal(err) }
	assertSelected("HTTP", httpSelected)
	httpConn.Close()

	tlsConn := dialSOCKSIP(443)
	tlsClient := tls.Client(tlsConn, &tls.Config{ServerName: "sniff-tls.test", InsecureSkipVerify: true})
	handshakeDone := make(chan error, 1)
	go func() { handshakeDone <- tlsClient.Handshake() }()
	assertSelected("TLS", tlsSelected)
	_ = tlsConn.Close()
	select {
	case <-handshakeDone:
	case <-time.After(time.Second):
	}
}
