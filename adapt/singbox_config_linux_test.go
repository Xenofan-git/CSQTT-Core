//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSingBoxConfigKeepsDirectAndOrderedRouteRulesSeparate(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "192.0.2.1:12500",
		CSQTTAddress: "127.0.0.1:12501",
		DirectDomains: []string{"LOCAL.example", "local.example."},
		DirectIPCIDRs: []string{"192.0.2.7/24", "2001:db8::1/64"},
		SOCKS5: []singBoxSOCKS5Outbound{{ID:"proxy-a", Server:"198.51.100.2", ServerPort:1080, Enabled:true}},
		Rules: []singBoxRouteRule{
			{ID:"first", Domains:[]string{"one.example"}, Outbound:"socks-proxy-a", Enabled:true},
			{ID:"second", DomainSuffixes:[]string{".example.net"}, Outbound:singBoxCSQTTTag, Enabled:true},
			{ID:"disabled", Domains:[]string{"disabled.example"}, Outbound:singBoxDirectTag, Enabled:false},
		},
	})
	if err != nil { t.Fatal(err) }

	var config map[string]any
	if err := json.Unmarshal(got, &config); err != nil { t.Fatal(err) }
	inbounds := config["inbounds"].([]any)
	inbound := inbounds[0].(map[string]any)
	if inbound["type"] != "socks" || inbound["listen"] != "192.0.2.1" || inbound["listen_port"] != float64(12500) {
		t.Fatalf("unexpected inbound: %#v", inbound)
	}
	if _, exists := config["dns"]; exists {
		t.Fatal("DNS must remain an explicit next-stage design, not be silently redirected")
	}
	outbounds := config["outbounds"].([]any)
	if len(outbounds) != 3 { t.Fatalf("got %d outbounds, want direct + CSQTT + one user SOCKS5", len(outbounds)) }
	rules := config["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 5 { t.Fatalf("got %d rules, want sniff + Direct + 2 enabled routes + reject", len(rules)) }
	direct := rules[1].(map[string]any)
	if direct["outbound"] != singBoxDirectTag { t.Fatalf("Direct bypass is not first: %#v", direct) }
	if direct["domain"].([]any)[0] != "local.example" { t.Fatalf("domain normalization/dedup failed: %#v", direct) }
	if direct["ip_cidr"].([]any)[0] != "192.0.2.0/24" { t.Fatalf("CIDR normalization failed: %#v", direct) }
	first := rules[2].(map[string]any)
	second := rules[3].(map[string]any)
	if first["outbound"] != "socks-proxy-a" || first["domain"].([]any)[0] != "one.example" { t.Fatalf("first user route lost ordering: %#v", first) }
	if second["outbound"] != singBoxCSQTTTag || second["domain_suffix"].([]any)[0] != ".example.net" { t.Fatalf("second user route malformed: %#v", second) }
	last := rules[4].(map[string]any)
	if last["action"] != "reject" { t.Fatalf("unmatched traffic must fail closed: %#v", last) }
	encoded := string(got)
	for _, forbidden := range []string{"auto_route", "auto_redirect", "\"type\": \"tun\""} {
		if strings.Contains(encoded, forbidden) { t.Fatalf("config contains forbidden global capture field %q", forbidden) }
	}
}

func TestBuildSingBoxConfigRejectsInvalidOrAmbiguousInputs(t *testing.T) {
	tests := []struct{name string; input singBoxConfigInput}{
		{"missing inbound address", singBoxConfigInput{CSQTTAddress:"127.0.0.1:12501"}},
		{"same inbound and CSQTT endpoint", singBoxConfigInput{ListenAddress:"127.0.0.1:12501", CSQTTAddress:"127.0.0.1:12501"}},
		{"invalid domain", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12501", DirectDomains:[]string{"https://bad.example"}}},
		{"invalid CIDR", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12501", DirectIPCIDRs:[]string{"not-a-cidr"}}},
		{"unknown outbound", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12501", Rules:[]singBoxRouteRule{{ID:"r1",Domains:[]string{"a.example"},Outbound:"missing",Enabled:true}}}},
		{"partial credentials", singBoxConfigInput{ListenAddress:"127.0.0.1:12500", CSQTTAddress:"127.0.0.1:12501", SOCKS5:[]singBoxSOCKS5Outbound{{ID:"p1",Server:"198.51.100.1",ServerPort:1080,Username:"u",Enabled:true}}}},
	}
	for _, tt := range tests { t.Run(tt.name,func(t *testing.T){ if _,err:=buildSingBoxConfig(tt.input); err==nil { t.Fatal("expected validation error") } }) }
}

func TestBuildSingBoxConfigDoesNotImplicitlyRouteUnmatchedTraffic(t *testing.T) {
	got, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:12500",CSQTTAddress:"127.0.0.1:12501"})
	if err != nil { t.Fatal(err) }
	var config map[string]any
	if err:=json.Unmarshal(got,&config); err!=nil { t.Fatal(err) }
	rules:=config["route"].(map[string]any)["rules"].([]any)
	last:=rules[len(rules)-1].(map[string]any)
	if last["action"]!="reject" { t.Fatalf("unexpected catch-all: %#v",last) }
	if _,ok:=config["route"].(map[string]any)["final"];ok { t.Fatal("route.final would introduce an implicit outbound fallback") }
}

func TestBuildSingBoxConfigPassesPinnedBinaryCheck(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binary == "" {
		t.Skip("CSQTT_SING_BOX_BINARY is not set; real sing-box validation runs in pinned CI")
	}

	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:12500",
		CSQTTAddress:  "127.0.0.1:12501",
		DirectDomains: []string{"example.com"},
		DirectIPCIDRs: []string{"192.0.2.0/24", "2001:db8::/32"},
		SOCKS5: []singBoxSOCKS5Outbound{{
			ID: "proxy-a", Server: "198.51.100.2", ServerPort: 1080, Enabled: true,
		}},
		Rules: []singBoxRouteRule{{
			ID: "route-a", DomainSuffixes: []string{".example.net"},
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
