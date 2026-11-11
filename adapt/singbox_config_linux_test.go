//go:build linux

package main

import (
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
	dns := config["dns"].(map[string]any)
	if dns["reverse_mapping"] != true || dns["strategy"] != "prefer_ipv4" || dns["final"] != singBoxDNSLocalTag {
		t.Fatalf("DNS must use the isolated local resolver with reverse mapping: %#v", dns)
	}
	dnsServers := dns["servers"].([]any)
	if len(dnsServers) != 1 || dnsServers[0].(map[string]any)["type"] != "local" {
		t.Fatalf("default DNS server is not local/system-resolver mode: %#v", dnsServers)
	}
	outbounds := config["outbounds"].([]any)
	if len(outbounds) != 3 { t.Fatalf("got %d outbounds, want DIRECT + CSQTT + one user SOCKS5", len(outbounds)) }
	for _, raw := range outbounds {
		if raw.(map[string]any)["tag"] == singBoxDirectBypassTag { t.Fatal("legacy direct-bypass outbound must not be emitted") }
	}
	rules := config["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 5 { t.Fatalf("got %d rules, want DNS hijack + sniff + system-direct + 2 enabled routes", len(rules)) }
	dnsHijack := rules[0].(map[string]any)
	if dnsHijack["action"] != "hijack-dns" || dnsHijack["port"].([]any)[0] != float64(53) {
		t.Fatalf("DNS traffic is not hijacked before ordinary routing: %#v", dnsHijack)
	}
	if gotFinal := config["route"].(map[string]any)["final"]; gotFinal != singBoxDirectTag {
		t.Fatalf("unmatched fallback = %v, want explicit DIRECT", gotFinal)
	}
	direct := rules[2].(map[string]any)
	if direct["outbound"] != singBoxDirectTag { t.Fatalf("system-direct card is not first after DNS/sniff: %#v", direct) }
	if direct["domain"].([]any)[0] != "local.example" { t.Fatalf("domain normalization/dedup failed: %#v", direct) }
	if direct["domain_suffix"].([]any)[0] != ".example.org" { t.Fatalf("Direct suffix missing: %#v", direct) }
	if direct["ip_cidr"].([]any)[0] != "192.0.2.0/24" || direct["ip_cidr"].([]any)[2] != "203.0.113.8/32" { t.Fatalf("CIDR normalization failed: %#v", direct) }
	if direct["domain"].([]any)[0] != "local.example" || direct["domain_suffix"].([]any)[0] != ".example.org" { t.Fatalf("legacy Direct fields were not merged into system-direct: %#v", direct) }
	first := rules[3].(map[string]any)
	second := rules[4].(map[string]any)
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
	if len(rules) != 3 { t.Fatalf("route rules=%d, want DNS hijack + sniff + IP-only rule", len(rules)) }
	ipRule := rules[2].(map[string]any)
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

func TestBuildSingBoxConfigDNSModesAndValidation(t *testing.T) {
	local, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500"})
	if err != nil { t.Fatalf("build local DNS config: %v", err) }
	var localConfig map[string]any
	if err := json.Unmarshal(local, &localConfig); err != nil { t.Fatal(err) }
	localDNS := localConfig["dns"].(map[string]any)
	if localDNS["reverse_mapping"] != true || localDNS["strategy"] != "prefer_ipv4" { t.Fatalf("local DNS defaults: %#v", localDNS) }
	localServer := localDNS["servers"].([]any)[0].(map[string]any)
	if localServer["type"] != "local" || localServer["prefer_go"] != true { t.Fatalf("local DNS server: %#v", localServer) }

	udp, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500", DNSMode: "udp", DNSServer: "127.0.0.1", DNSPort: 5353, DNSStrategy: "ipv6_only"})
	if err != nil { t.Fatalf("build UDP DNS config: %v", err) }
	var udpConfig map[string]any
	if err := json.Unmarshal(udp, &udpConfig); err != nil { t.Fatal(err) }
	udpDNS := udpConfig["dns"].(map[string]any)
	udpServer := udpDNS["servers"].([]any)[0].(map[string]any)
	_, hasDNSDetour := udpServer["detour"]
	if udpServer["type"] != "udp" || udpServer["server"] != "127.0.0.1" || udpServer["server_port"] != float64(5353) || hasDNSDetour || udpDNS["strategy"] != "ipv6_only" {
		t.Fatalf("UDP DNS server configuration must not set an explicit DIRECT detour: %#v / %#v", udpDNS, udpServer)
	}

	invalid := []struct{name string; input singBoxConfigInput}{
		{"unknown mode", singBoxConfigInput{ListenAddress:"127.0.0.1:19180", DNSMode:"https"}},
		{"domain bootstrap server", singBoxConfigInput{ListenAddress:"127.0.0.1:19180", DNSMode:"udp", DNSServer:"dns.example", DNSPort:53}},
		{"invalid port", singBoxConfigInput{ListenAddress:"127.0.0.1:19180", DNSMode:"udp", DNSServer:"192.0.2.1", DNSPort:65536}},
		{"unknown strategy", singBoxConfigInput{ListenAddress:"127.0.0.1:19180", DNSStrategy:"automatic"}},
		{"custom server in local mode", singBoxConfigInput{ListenAddress:"127.0.0.1:19180", DNSServer:"192.0.2.1"}},
	}
	for _, tc := range invalid { t.Run(tc.name, func(t *testing.T) { if _, err := buildSingBoxConfig(tc.input); err == nil { t.Fatal("invalid DNS configuration was accepted") } }) }
}

func TestBuildSingBoxConfigValidatesDisabledRuleReferences(t *testing.T) {
	input:=singBoxConfigInput{
		ListenAddress:"127.0.0.1:19180",
		SOCKS5:[]singBoxSOCKS5Outbound{{ID:"proxy",Server:"192.0.2.10",ServerPort:1080,Enabled:true}},
		RuleSets:[]singBoxRuleSet{{Type:"remote",Tag:"geo-test",Format:"source",URL:"https://example.com/geosite.json"}},
		Rules:[]singBoxRouteRule{{ID:"disabled-rule",Enabled:false,RuleSets:[]string{"geo-test"},Outbound:"socks-proxy"}},
	}
	if _,err:=buildSingBoxConfig(input);err!=nil{t.Fatalf("disabled rule with registered Rule Set should be preserved: %v",err)}
	input.RuleSets=nil
	if _,err:=buildSingBoxConfig(input);err==nil||!strings.Contains(err.Error(),"unknown Rule Set"){
		t.Fatalf("disabled rule referencing an unregistered Rule Set was accepted: %v",err)
	}
}

func TestBuildSingBoxConfigPreservesSystemRouteCardOrder(t *testing.T) {
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500", CSQTTEnabled: false, CSQTTExplicitlyDisabled: true,
		SOCKS5: []singBoxSOCKS5Outbound{{ID:"one",Server:"192.0.2.10",ServerPort:1080,Enabled:true},{ID:"two",Server:"192.0.2.11",ServerPort:1080,Enabled:true}},
		Rules: []singBoxRouteRule{
			{ID:singBoxDirectRuleID,Domains:[]string{"direct-first.test"},Outbound:singBoxDirectTag,Enabled:true},
			{ID:"user-one",Domains:[]string{"first.test"},Outbound:"socks-one",Enabled:true},
			{ID:singBoxCSQTTRuleID,Domains:[]string{"csqtt-only.test"},Outbound:singBoxCSQTTTag,Enabled:true},
			{ID:"user-two",Domains:[]string{"after-csqtt.test"},Outbound:"socks-two",Enabled:true},
		},
	}
	encoded,err:=buildSingBoxConfig(input);if err!=nil{t.Fatal(err)}
	var config map[string]any;if err:=json.Unmarshal(encoded,&config);err!=nil{t.Fatal(err)}
	rules:=config["route"].(map[string]any)["rules"].([]any)
	got:=[]string{}
	for _,raw:=range rules{rule:=raw.(map[string]any);if rule["action"]=="route"{got=append(got,rule["outbound"].(string))}}
	want:=[]string{singBoxDirectTag,"socks-one",singBoxCSQTTTag,"socks-two"}
	if len(got)!=len(want){t.Fatalf("ordered route count=%d want=%d: %#v",len(got),len(want),got)}
	for i:=range want{if got[i]!=want[i]{t.Fatalf("route order=%#v want=%#v",got,want)}}
	if config["route"].(map[string]any)["final"]!=singBoxDirectTag{t.Fatalf("system card must not become hidden route.final: %#v",config["route"])}
	var csqttRule map[string]any
	for _, raw := range rules {
		candidate, ok := raw.(map[string]any)
		if ok && candidate["outbound"] == singBoxCSQTTTag { csqttRule = candidate; break }
	}
	if csqttRule == nil { t.Fatal("enabled CSQTT route rule was not generated") }
	if domains := stringSlice(csqttRule["domain"]); len(domains) != 1 || domains[0] != "csqtt-only.test" {
		t.Fatalf("CSQTT system rule lost its domain matcher: %#v", csqttRule)
	}
	var csqttOutbound bool
	for _, raw := range config["outbounds"].([]any) {
		if raw.(map[string]any)["tag"] == singBoxCSQTTTag { csqttOutbound = true }
	}
	if !csqttOutbound { t.Fatal("CSQTT outbound must remain configured while Core runtime state is disabled") }
}

func TestBuildSingBoxConfigRejectsInvalidSystemRouteCardsAndRuleOverflow(t *testing.T) {
	base:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500",CSQTTEnabled:true}
	badOrder:=base;badOrder.Rules=[]singBoxRouteRule{{ID:"user",Domains:[]string{"x.test"},Outbound:singBoxCSQTTTag,Enabled:true},{ID:singBoxDirectRuleID,Domains:[]string{"direct.test"},Outbound:singBoxDirectTag,Enabled:true}}
	if _,err:=buildSingBoxConfig(badOrder);err==nil||!strings.Contains(err.Error(),"top of the Route queue"){t.Fatalf("misplaced DIRECT card error=%v",err)}
	badCSQTT:=base;badCSQTT.CSQTTAddress="";badCSQTT.CSQTTEnabled=false;badCSQTT.CSQTTExplicitlyDisabled=true;badCSQTT.Rules=[]singBoxRouteRule{{ID:singBoxCSQTTRuleID,Domains:[]string{"csqtt.test"},Outbound:singBoxCSQTTTag,Enabled:true}}
	if _,err:=buildSingBoxConfig(badCSQTT);err==nil||!strings.Contains(err.Error(),"SOCKS endpoint is empty"){t.Fatalf("enabled CSQTT rule without endpoint error=%v",err)}
	disabledCSQTT:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180",Rules:[]singBoxRouteRule{{ID:singBoxCSQTTRuleID,Outbound:singBoxCSQTTTag,Enabled:false}}}
	if _,err:=buildSingBoxConfig(disabledCSQTT);err!=nil{t.Fatalf("disabled CSQTT rule without endpoint should remain editable: %v",err)}
	overflow:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180",SOCKS5:[]singBoxSOCKS5Outbound{{ID:"proxy",Server:"192.0.2.10",ServerPort:1080,Enabled:true}}}
	for i:=0;i<16;i++{overflow.Rules=append(overflow.Rules,singBoxRouteRule{ID:fmt.Sprintf("rule-%d",i),Domains:[]string{fmt.Sprintf("r%d.test",i)},Outbound:"socks-proxy",Enabled:true})}
	if _,err:=buildSingBoxConfig(overflow);err==nil||!strings.Contains(err.Error(),"15 user route rules"){t.Fatalf("route rule overflow error=%v",err)}
	tooMany:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180"}
	for i:=0;i<16;i++{tooMany.SOCKS5=append(tooMany.SOCKS5,singBoxSOCKS5Outbound{ID:fmt.Sprintf("proxy-%d",i),Server:"192.0.2.10",ServerPort:1080,Enabled:false})}
	if _,err:=buildSingBoxConfig(tooMany);err==nil||!strings.Contains(err.Error(),"15 user SOCKS5 outbounds"){t.Fatalf("SOCKS5 outbound overflow error=%v",err)}
}

func TestBuildSingBoxConfigUsesDIRECTAsExplicitDefaultFallback(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500"})
	if err != nil { t.Fatal(err) }
	var config map[string]any
	if err:=json.Unmarshal(got,&config); err!=nil { t.Fatal(err) }
	route:=config["route"].(map[string]any)
	if route["final"] != singBoxDirectTag { t.Fatalf("route.final=%v, want DIRECT",route["final"]) }
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
	input:=singBoxConfigInput{ListenAddress:reserveSingBoxTestAddress(t),CSQTTAddress:csqtt.Status().Address,Rules:[]singBoxRouteRule{{ID:"csqtt-domain",Domains:[]string{"route-csqtt.test"},Outbound:singBoxCSQTTTag,Enabled:true}}}
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

func TestSingBoxFirstMatchingRuleWinsAndFinalOutboundIsUsed(t *testing.T) {
	binaryPath:=strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"));if binaryPath==""{t.Skip("requires pinned sing-box binary from CI")}
	echo,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};defer echo.Close()
	go func(){for{conn,e:=echo.Accept();if e!=nil{return};go func(c net.Conn){defer c.Close();_,_=io.Copy(c,c)}(conn)}}()
	startCapture:=func(name string)(*Socks5Server,<-chan string){
		t.Helper();targets:=make(chan string,4);server:=NewSocks5Server("127.0.0.1:0")
		server.dialTCPOverride=func(target string)(net.Conn,error){select{case targets<-target:default:};return net.DialTimeout("tcp",echo.Addr().String(),2*time.Second)}
		if err:=server.Start();err!=nil{t.Fatalf("start %s SOCKS5: %v",name,err)};t.Cleanup(func(){server.Stop()});return server,targets
	}
	first,firstTargets:=startCapture("first-match");second,secondTargets:=startCapture("later-match");fallback,fallbackTargets:=startCapture("final")
	input:=singBoxConfigInput{ListenAddress:reserveSingBoxTestAddress(t),FinalOutbound:"socks-final",SOCKS5:[]singBoxSOCKS5Outbound{
		{ID:"first",Server:"127.0.0.1",ServerPort:mustPort(t,first.Status().Address),Enabled:true},
		{ID:"second",Server:"127.0.0.1",ServerPort:mustPort(t,second.Status().Address),Enabled:true},
		{ID:"final",Server:"127.0.0.1",ServerPort:mustPort(t,fallback.Status().Address),Enabled:true},
	},Rules:[]singBoxRouteRule{
		{ID:"suffix-first",DomainSuffixes:[]string{"example.test"},Outbound:"socks-first",Enabled:true},
		{ID:"exact-second",Domains:[]string{"api.example.test"},Outbound:"socks-second",Enabled:true},
	}}
	config,err:=buildSingBoxConfig(input);if err!=nil{t.Fatal(err)}
	path:=filepath.Join(t.TempDir(),"sing-box-order.json");if err:=os.WriteFile(path,config,0600);err!=nil{t.Fatal(err)}
	cmd:=exec.Command(binaryPath,"run","-c",path);output:=&singBoxTestLogBuffer{};cmd.Stdout,cmd.Stderr=output,output
	if err:=cmd.Start();err!=nil{t.Fatalf("start sing-box: %v",err)};defer func(){_=cmd.Process.Kill();_=cmd.Wait()}()
	dial:=func(host string)net.Conn{
		t.Helper();var conn net.Conn;deadline:=time.Now().Add(8*time.Second)
		for time.Now().Before(deadline){conn,err=net.DialTimeout("tcp",input.ListenAddress,200*time.Millisecond);if err==nil{break};time.Sleep(50*time.Millisecond)}
		if conn==nil{t.Fatalf("sing-box inbound not ready: %v; logs: %s",err,output.String())};_=conn.SetDeadline(time.Now().Add(8*time.Second))
		if _,err:=conn.Write([]byte{5,1,0});err!=nil{t.Fatal(err)};var greeting [2]byte;if _,err:=io.ReadFull(conn,greeting[:]);err!=nil{t.Fatal(err)};if greeting!=[2]byte{5,0}{t.Fatalf("SOCKS greeting=%v",greeting)}
		request:=[]byte{5,1,0,3,byte(len(host))};request=append(request,[]byte(host)...);request=append(request,1,187)
		if _,err:=conn.Write(request);err!=nil{t.Fatal(err)};var reply [10]byte;if _,err:=io.ReadFull(conn,reply[:]);err!=nil{t.Fatalf("CONNECT %s: %v; logs: %s",host,err,output.String())};if reply[1]!=0{t.Fatalf("CONNECT %s failed: %v; logs: %s",host,reply,output.String())};return conn
	}
	matched:=dial("api.example.test");defer matched.Close()
	select{case target:=<-firstTargets:if target!="api.example.test:443"{t.Fatalf("first matching outbound received %q",target)};case <-time.After(3*time.Second):t.Fatal("first matching route was not selected")}
	select{case target:=<-secondTargets:t.Fatalf("later exact rule incorrectly overrode first suffix rule: %q",target);case <-time.After(200*time.Millisecond):}
	unmatched:=dial("unmatched.test");defer unmatched.Close()
	select{case target:=<-fallbackTargets:if target!="unmatched.test:443"{t.Fatalf("final outbound received %q",target)};case <-time.After(3*time.Second):t.Fatal("explicit route.final outbound was not selected for unmatched traffic")}
}

func mustPort(t *testing.T,address string)int{t.Helper();_,port,err:=net.SplitHostPort(address);if err!=nil{t.Fatal(err)};n,err:=strconv.Atoi(port);if err!=nil{t.Fatal(err)};return n}

func TestSingBoxUDPAssociateRelaysThroughRealCSQTTSocks5Server(t *testing.T) {
	binaryPath:=strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"));if binaryPath==""{t.Skip("requires pinned sing-box binary from CI")}
	echo,err:=net.ListenUDP("udp4",&net.UDPAddr{IP:net.ParseIP("127.0.0.1")});if err!=nil{t.Fatal(err)};defer echo.Close()
	go func(){buf:=make([]byte,2048);for{n,a,e:=echo.ReadFromUDP(buf);if e!=nil{return};_,_=echo.WriteToUDP(append([]byte(nil),buf[:n]...),a)}}()
	csqtt:=NewSocks5Server("127.0.0.1:0")
	csqtt.dialUDPOverride=func()(*net.UDPConn,error){return net.ListenUDP("udp4",&net.UDPAddr{IP:net.ParseIP("127.0.0.1")})}
	if err=csqtt.Start();err!=nil{t.Fatalf("start real CSQTT SOCKS5: %v",err)};defer csqtt.Stop()
	input:=singBoxConfigInput{ListenAddress:reserveSingBoxTestAddress(t),CSQTTAddress:csqtt.Status().Address}
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

func TestSingBoxDNSHijackAndReverseMappingThroughSOCKSInbound(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" { t.Skip("requires pinned sing-box binary from CI") }

	upstream, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil { t.Fatal(err) }
	defer upstream.Close()
	go func() {
		buf := make([]byte, 2048)
		_ = upstream.SetReadDeadline(time.Now().Add(15 * time.Second))
		for {
			n, peer, readErr := upstream.ReadFromUDP(buf)
			if readErr != nil { return }
			query := append([]byte(nil), buf[:n]...)
			questionEnd := 12
			for questionEnd < len(query) {
				labelLen := int(query[questionEnd])
				questionEnd++
				if labelLen == 0 { break }
				questionEnd += labelLen
			}
			if questionEnd+4 > len(query) { return }
			queryType := binary.BigEndian.Uint16(query[questionEnd:questionEnd+2])
			questionEnd += 4
			response := make([]byte, 12)
			copy(response, query[:12])
			response[2], response[3] = 0x81, 0x80
			response[6], response[7] = 0, 0
			response[8], response[9], response[10], response[11] = 0, 0, 0, 0
			response = append(response, query[12:questionEnd]...)
			if queryType == 1 {
				response[7] = 1
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 198, 51, 100, 42)
			}
			_, _ = upstream.WriteToUDP(response, peer)
			if queryType == 1 { return }
		}
	}()

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
	targets := make(chan string, 1)
	csqtt := NewSocks5Server("127.0.0.1:0")
	csqtt.dialTCPOverride = func(target string) (net.Conn, error) {
		select { case targets <- target: default: }
		return net.DialTimeout("tcp", echo.Addr().String(), 2*time.Second)
	}
	if err := csqtt.Start(); err != nil { t.Fatalf("start CSQTT SOCKS5: %v", err) }
	defer csqtt.Stop()

	input := singBoxConfigInput{
		ListenAddress: reserveSingBoxTestAddress(t),
		CSQTTAddress: csqtt.Status().Address,
		FinalOutbound: singBoxDirectTag,
		DNSMode: "udp", DNSServer: "127.0.0.1", DNSPort: upstream.LocalAddr().(*net.UDPAddr).Port, DNSStrategy: "ipv4_only",
		Rules: []singBoxRouteRule{{ID: "dns-domain", Domains: []string{"dns-route.test"}, Outbound: singBoxCSQTTTag, Enabled: true}},
	}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	configPath := filepath.Join(t.TempDir(), "sing-box-dns.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	output := &singBoxTestLogBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil { t.Fatalf("start sing-box: %v", err) }
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	dialInbound := func() net.Conn {
		t.Helper()
		var conn net.Conn
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			conn, err = net.DialTimeout("tcp", input.ListenAddress, 200*time.Millisecond)
			if err == nil { break }
			time.Sleep(50 * time.Millisecond)
		}
		if conn == nil { t.Fatalf("sing-box inbound not ready: %v; logs: %s", err, output.String()) }
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil { t.Fatal(err) }
		if greeting != [2]byte{5, 0} { t.Fatalf("SOCKS greeting: %v", greeting) }
		return conn
	}

	// A TCP DNS request to an external resolver address must be hijacked by
	// sing-box and answered by the local fake UDP DNS server, without Internet.
	dnsConn := dialInbound()
	defer dnsConn.Close()
	dnsConnect := []byte{5, 1, 0, 1, 8, 8, 8, 8, 0, 53}
	if _, err := dnsConn.Write(dnsConnect); err != nil { t.Fatal(err) }
	var connectReply [10]byte
	if _, err := io.ReadFull(dnsConn, connectReply[:]); err != nil { t.Fatalf("DNS CONNECT: %v; logs: %s", err, output.String()) }
	if connectReply[1] != 0 { t.Fatalf("DNS CONNECT was not hijacked: %v; logs: %s", connectReply, output.String()) }

	dnsQuery := make([]byte, 12)
	binary.BigEndian.PutUint16(dnsQuery[0:2], 0x1234)
	binary.BigEndian.PutUint16(dnsQuery[2:4], 0x0100)
	binary.BigEndian.PutUint16(dnsQuery[4:6], 1)
	for _, label := range strings.Split("dns-route.test", ".") {
		dnsQuery = append(dnsQuery, byte(len(label)))
		dnsQuery = append(dnsQuery, []byte(label)...)
	}
	dnsQuery = append(dnsQuery, 0, 0, 1, 0, 1)
	var frameLen [2]byte
	binary.BigEndian.PutUint16(frameLen[:], uint16(len(dnsQuery)))
	if _, err := dnsConn.Write(append(frameLen[:], dnsQuery...)); err != nil { t.Fatal(err) }
	if _, err := io.ReadFull(dnsConn, frameLen[:]); err != nil { t.Fatalf("DNS response length: %v; logs: %s", err, output.String()) }
	response := make([]byte, int(binary.BigEndian.Uint16(frameLen[:])))
	if _, err := io.ReadFull(dnsConn, response); err != nil { t.Fatalf("DNS response: %v; logs: %s", err, output.String()) }
	if len(response) < 4 || !bytes.Equal(response[len(response)-4:], []byte{198, 51, 100, 42}) {
		t.Fatalf("DNS answer did not contain the expected address: %v", response)
	}

	// The client now connects by IP. reverse_mapping must recover the DNS
	// domain for route matching, selecting CSQTT rather than route.final=direct.
	// The SOCKS outbound may still receive the original IP as its destination;
	// reverse_mapping is route metadata, not a requirement to rewrite SOCKS ATYP.
	dataConn := dialInbound()
	defer dataConn.Close()
	connectIP := []byte{5, 1, 0, 1, 198, 51, 100, 42, 1, 187}
	if _, err := dataConn.Write(connectIP); err != nil { t.Fatal(err) }
	if _, err := io.ReadFull(dataConn, connectReply[:]); err != nil { t.Fatalf("reverse-mapped CONNECT: %v; logs: %s", err, output.String()) }
	if connectReply[1] != 0 { t.Fatalf("reverse-mapped domain did not select CSQTT: %v; logs: %s", connectReply, output.String()) }
	payload := []byte("dns-reverse-mapping-e2e")
	if _, err := dataConn.Write(payload); err != nil { t.Fatal(err) }
	echoed := make([]byte, len(payload))
	if _, err := io.ReadFull(dataConn, echoed); err != nil { t.Fatalf("CSQTT echo: %v", err) }
	if !bytes.Equal(echoed, payload) { t.Fatalf("echoed payload = %q, want %q", echoed, payload) }
	select {
	case target := <-targets:
		if target != "198.51.100.42:443" { t.Fatalf("reverse-mapped domain rule selected CSQTT, but outbound destination was %q", target) }
	case <-time.After(2 * time.Second):
		t.Fatal("CSQTT outbound was not selected after DNS reverse mapping")
	}
}

func TestBuildSingBoxConfigPassesPinnedBinaryCheck(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binary == "" {
		t.Skip("CSQTT_SING_BOX_BINARY is not set; real sing-box validation runs in pinned CI")
	}

	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
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
	input := singBoxConfigInput{ListenAddress:"127.0.0.1:19180", CSQTTAddress:"127.0.0.1:12500"}
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

func TestSingBoxRemoteRuleSetsUseExplicitHTTPClientAndCache(t *testing.T) {
	input:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180",RuleSetCachePath:"/var/lib/csqtt/sing-box/sing-box-cache.db",RuleSets:[]singBoxRuleSet{{Type:"remote",Tag:"geo-test",Format:"binary",URL:"https://example.org/geo-test.srs",UpdateInterval:"1d"}}}
	encoded,err:=buildSingBoxConfig(input);if err!=nil{t.Fatal(err)}
	var config map[string]any;if err:=json.Unmarshal(encoded,&config);err!=nil{t.Fatal(err)}
	clients,ok:=config["http_clients"].([]any);if !ok||len(clients)!=1||clients[0].(map[string]any)["tag"]!=singBoxRuleSetHTTPClientTag{t.Fatalf("explicit HTTP clients missing: %#v",config["http_clients"])}
	route:=config["route"].(map[string]any);if route["default_http_client"]!=singBoxRuleSetHTTPClientTag{t.Fatalf("default_http_client=%v",route["default_http_client"])}
	sets:=route["rule_set"].([]any);if len(sets)!=1||sets[0].(map[string]any)["http_client"]!=singBoxRuleSetHTTPClientTag{t.Fatalf("remote Rule Set HTTP client not linked: %#v",sets)}
	cache:=config["experimental"].(map[string]any)["cache_file"].(map[string]any)
	if cache["enabled"]!=true||cache["path"]!=input.RuleSetCachePath{t.Fatalf("remote Rule Set cache not persistent: %#v",cache)}
	if _,exists:=sets[0].(map[string]any)["download_detour"];exists{t.Fatal("deprecated download_detour must not be emitted")}
	binary:=strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"));if binary!=""{path:=filepath.Join(t.TempDir(),"remote-ruleset.json");if err:=os.WriteFile(path,encoded,0600);err!=nil{t.Fatal(err)};output,err:=exec.Command(binary,"check","-c",path).CombinedOutput();if err!=nil{t.Fatalf("pinned sing-box rejected remote Rule Set config: %v: %s",err,output)}}
}

func TestSingBoxRuleSetRejectsUnknownAndInsecureSources(t *testing.T) {
	input := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:12500"}
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
 loaded,err:=manager.LoadConfig();if err!=nil{t.Fatal(err)};if len(loaded.Rules)!=2||loaded.Rules[0].ID!="loaded-1"||len(loaded.Rules[0].Advanced)!=4||loaded.Rules[0].Outbound!=singBoxCSQTTTag||!loaded.Rules[0].Enabled||loaded.Rules[1].ID!=singBoxCSQTTRuleID||loaded.Rules[1].Enabled||loaded.FinalOutbound!=singBoxDirectTag{t.Fatalf("advanced CSQTT rule / disabled legacy catch-all migration not loaded: %#v",loaded.Rules)}
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

func TestBuildSingBoxConfigUsesDIRECTFallbackWhenCSQTTDisabled(t *testing.T) {
	encoded, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19180"})
	if err != nil { t.Fatalf("DIRECT fallback should be available without CSQTT: %v", err) }
	var config map[string]any
	if err := json.Unmarshal(encoded, &config); err != nil { t.Fatal(err) }
	if got := config["route"].(map[string]any)["final"]; got != singBoxDirectTag { t.Fatalf("route.final=%v, want DIRECT", got) }
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
