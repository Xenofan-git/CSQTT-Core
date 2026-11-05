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
	got, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500"})
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
 original:=`{"log":{"level":"info"},"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19180}],"outbounds":[{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":12500,"version":"5"},{"type":"direct","tag":"direct"}],"route":{"rules":[{"inbound":["csqtt-route-in"],"action":"route","outbound":"csqtt","logical":{"mode":"or","rules":[{"domain_suffix":["example.org"]},{"protocol":["quic"]}]},"network":["tcp","udp"],"invert":true}],"final":"csqtt","rule_set":[]}}`
 if err:=os.WriteFile(config,[]byte(original),0600);err!=nil{t.Fatal(err)}
 loaded,err:=manager.LoadConfig();if err!=nil{t.Fatal(err)};if len(loaded.Rules)!=1||len(loaded.Rules[0].Advanced)!=3{t.Fatalf("advanced fields not loaded: %#v",loaded.Rules)}
 if err:=manager.SaveConfig(loaded);err!=nil{t.Fatal(err)};data,err:=os.ReadFile(config);if err!=nil{t.Fatal(err)}
 var got map[string]any;if err:=json.Unmarshal(data,&got);err!=nil{t.Fatal(err)};rules:=got["route"].(map[string]any)["rules"].([]any);var advanced map[string]any
 for _,candidate:=range rules{rule:=candidate.(map[string]any);if rule["logical"]!=nil{advanced=rule;break}}
 if advanced==nil||advanced["network"]==nil||advanced["invert"]!=true{t.Fatalf("advanced fields lost: %#v",rules)}
 logical:=advanced["logical"].(map[string]any);if logical["mode"]!="or"||len(logical["rules"].([]any))!=2{t.Fatalf("logical matcher changed: %#v",logical)}
}

func TestSingBoxAdvancedRouteFieldsRejectControlPlaneOverrides(t *testing.T) {
 for _,key:=range []string{"action","outbound","inbound","auto_route","final"}{input:=singBoxConfigInput{ListenAddress:"127.0.0.1:19180",CSQTTAddress:"127.0.0.1:12500",Rules:[]singBoxRouteRule{{ID:"safe",Domains:[]string{"example.org"},Outbound:singBoxCSQTTTag,Enabled:true,Advanced:map[string]json.RawMessage{key:json.RawMessage(`"unsafe"`)}}}};if _,err:=buildSingBoxConfig(input);err==nil{t.Fatalf("unsafe advanced field %q accepted",key)}}
}