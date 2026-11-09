//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"context"
	"net"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"strings"
	"testing"
	"time"
)

func newFakeSingBox(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sing-box")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil { t.Fatal(err) }
	return path
}

func writeValidSingBoxConfigForTest(t *testing.T, path string) {
	t.Helper()
	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080",
		CSQTTAddress: "127.0.0.1:19081", FinalOutbound: singBoxDirectTag,
	})
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, encoded, 0600); err != nil { t.Fatal(err) }
}

func TestSingBoxManagerLoadConfigSortsExternalSOCKS5Outbounds(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	raw := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"socks-zeta","server":"zeta.example","server_port":1080},{"type":"socks","tag":"socks-alpha","server":"alpha.example","server_port":1081},{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":19081}],"route":{"final":"direct","rules":[]}}`
	if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
	manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
	for attempt := 0; attempt < 5; attempt++ {
		loaded, err := manager.LoadConfig()
		if err != nil { t.Fatalf("LoadConfig: %v", err) }
		if len(loaded.SOCKS5) != 2 { t.Fatalf("loaded %d external outbounds, want 2: %#v", len(loaded.SOCKS5), loaded.SOCKS5) }
		if !loaded.CSQTTEnabled || !loaded.CSQTTRule.Enabled { t.Fatalf("legacy CSQTT outbound did not restore enabled system card: %#v", loaded) }
		if loaded.SOCKS5[0].ID != "alpha" || loaded.SOCKS5[1].ID != "zeta" {
			t.Fatalf("external SOCKS5 outbounds are not stable and sorted: %#v", loaded.SOCKS5)
		}
	}
}

func TestSingBoxManagerLoadConfigRequiresExplicitConfiguredDefault(t *testing.T) {
	cases := []struct {
		name string
		route string
	}{
		{name: "missing route", route: `{}`},
		{name: "missing final", route: `{"rules":[]}`},
		{name: "empty final", route: `{"final":"","rules":[]}`},
		{name: "unknown final", route: `{"final":"socks-missing","rules":[]}`},
		{name: "non-string final", route: `{"final":123,"rules":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.json")
			raw := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"}],"route":` + tc.route + `}`
			if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
			manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
			if _, err := manager.LoadConfig(); err == nil {
				t.Fatal("persisted config without a valid explicit default outbound was accepted")
			}
		})
	}
}

func TestSingBoxManagerLoadConfigRejectsMalformedExternalSOCKS5(t *testing.T) {
	cases := []struct {
		name string
		outbound string
	}{
		{name: "empty host", outbound: `{"type":"socks","tag":"socks-alpha","server":"","server_port":1080}`},
		{name: "invalid host", outbound: `{"type":"socks","tag":"socks-alpha","server":"bad host","server_port":1080}`},
		{name: "fractional port", outbound: `{"type":"socks","tag":"socks-alpha","server":"proxy.example","server_port":1080.5}`},
		{name: "invalid id", outbound: `{"type":"socks","tag":"socks-bad/id","server":"proxy.example","server_port":1080}`},
		{name: "incomplete credentials", outbound: `{"type":"socks","tag":"socks-alpha","server":"proxy.example","server_port":1080,"username":"user"}`},
		{name: "wrong outbound type", outbound: `{"type":"direct","tag":"socks-alpha","server":"proxy.example","server_port":1080}`},
		{name: "non-string credentials", outbound: `{"type":"socks","tag":"socks-alpha","server":"proxy.example","server_port":1080,"username":123,"password":"secret"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.json")
			raw := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"},` + tc.outbound + `],"route":{"final":"direct","rules":[]}}`
			if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
			manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
			if _, err := manager.LoadConfig(); err == nil {
				t.Fatal("malformed persisted external SOCKS5 outbound was accepted")
			}
		})
	}
}

func TestSingBoxManagerLoadConfigRejectsUnsafeCSQTTOutbound(t *testing.T) {
	cases := []struct { name, server string; port int }{
		{name: "non-loopback address", server: "192.168.1.20", port: 12500},
		{name: "public address", server: "203.0.113.10", port: 12500},
		{name: "same endpoint as inbound", server: "127.0.0.1", port: 19080},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.json")
			raw := fmt.Sprintf(`{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"csqtt","server":%q,"server_port":%d}],"route":{"final":"direct","rules":[]}}`, tc.server, tc.port)
			if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
			manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
			if _, err := manager.LoadConfig(); err == nil {
				t.Fatal("unsafe persisted CSQTT outbound was accepted")
			}
		})
	}
}

func TestSingBoxListenIPAssignedMatchesExactInterfaceAddress(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.5.2"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("fd00::2"), Mask: net.CIDRMask(64, 128)},
	}
	if !singBoxListenIPAssigned(net.ParseIP("192.168.5.2"), addrs) { t.Fatal("assigned IPv4 address was not recognized") }
	if !singBoxListenIPAssigned(net.ParseIP("fd00::2"), addrs) { t.Fatal("assigned IPv6 address was not recognized") }
	if singBoxListenIPAssigned(net.ParseIP("192.168.5.3"), addrs) { t.Fatal("different host in the same subnet was incorrectly accepted") }
}

func TestSingBoxManagerOwnsIndependentProcessLifecycle(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "child.pid")
	binary := newFakeSingBox(t, "if [ \"$1\" = check ]; then exit 0; fi\nsleep 60 & echo $! > "+shellQuoteForTest(childPIDFile)+"\nwait")
	config := filepath.Join(dir, "config.json")
	writeValidSingBoxConfigForTest(t, config)
	manager := NewSingBoxManager(binary, config, filepath.Join(dir, "logs", "sing-box.log"))
	manager.stopTimeout = 300 * time.Millisecond
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	status := manager.Status()
	if !status.Running || status.PID == 0 { t.Fatalf("unexpected running status: %+v", status) }
	if err := manager.Start(context.Background()); err == nil { t.Fatal("second start must be rejected") }
	deadline := time.Now().Add(2*time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil { _, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &childPID); if childPID > 0 { break } }
		time.Sleep(10*time.Millisecond)
	}
	if childPID == 0 { t.Fatal("fake sing-box child did not start") }
	if err := manager.Stop(); err != nil { t.Fatal(err) }
	if got := manager.Status(); got.Running || got.PID != 0 { t.Fatalf("manager did not clear process state: %+v", got) }
	waitProcessGone(t, childPID)
	if _, err := os.Stat(manager.logPath); err != nil { t.Fatalf("separate log file missing: %v", err) }
}

func TestSingBoxManagerFailsClosedWhenConfigCheckFails(t *testing.T) {
	dir := t.TempDir()
	binary := newFakeSingBox(t, "if [ \"$1\" = check ]; then echo invalid-config >&2; exit 23; fi\nexit 0")
	config := filepath.Join(dir, "config.json")
	writeValidSingBoxConfigForTest(t, config)
	manager := NewSingBoxManager(binary, config, filepath.Join(dir, "sing-box.log"))
	err := manager.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid-config") { t.Fatalf("expected check error, got %v", err) }
	if got := manager.Status(); got.Running || got.PID != 0 { t.Fatalf("process started despite failed check: %+v", got) }
}

func TestSingBoxManagerRejectsMissingConfigWithoutStarting(t *testing.T) {
	binary := newFakeSingBox(t, "exit 0")
	manager := NewSingBoxManager(binary, filepath.Join(t.TempDir(), "missing.json"), filepath.Join(t.TempDir(), "sing-box.log"))
	if err := manager.Start(context.Background()); err == nil { t.Fatal("missing config must be rejected") }
	if manager.Status().Running { t.Fatal("manager started without a config") }
}

func TestSingBoxManagerStopCancelsInFlightStart(t *testing.T) {
	dir := t.TempDir()
	checkStarted := filepath.Join(dir, "check.started")
	runStarted := filepath.Join(dir, "run.started")
	binary := newFakeSingBox(t, "if [ \"$1\" = check ]; then touch "+shellQuoteForTest(checkStarted)+"; exec sleep 60; fi\ntouch "+shellQuoteForTest(runStarted)+"\nexec sleep 60")
	config := filepath.Join(dir, "config.json")
	writeValidSingBoxConfigForTest(t, config)
	manager := NewSingBoxManager(binary, config, filepath.Join(dir, "sing-box.log"))

	startResult := make(chan error, 1)
	go func() { startResult <- manager.Start(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(checkStarted); err == nil { break }
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(checkStarted); err != nil { t.Fatal("config check did not start") }

	stopResult := make(chan error, 1)
	go func() { stopResult <- manager.Stop() }()
	select {
	case err := <-startResult:
		if err == nil { t.Fatal("Start unexpectedly succeeded after Stop cancelled it") }
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after Stop cancelled config check")
	}
	select {
	case err := <-stopResult:
		if err != nil { t.Fatalf("Stop failed: %v", err) }
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not wait for in-flight Start")
	}
	if _, err := os.Stat(runStarted); err == nil { t.Fatal("sing-box run process started after Stop") }
	if got := manager.Status(); got.Running || got.PID != 0 { t.Fatalf("unexpected process after cancelled start: %+v", got) }
}

func TestSingBoxManagerSaveConfigIsAtomicAndValidated(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	binary := newFakeSingBox(t, "if [ \"$1\" = check ]; then grep -q 'invalid-marker' \"$3\" && { echo invalid >&2; exit 9; }; exit 0; fi\nexit 0")
	manager := NewSingBoxManager(binary, config, filepath.Join(dir, "sing-box.log"))
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080", CSQTTAddress: "127.0.0.1:19081", FinalOutbound: singBoxDirectTag,
		DirectDomainSuffixes: []string{".example.org"},
		Rules: []singBoxRouteRule{{ID:"route-a", DomainKeywords:[]string{"stream"}, DomainRegexes:[]string{"video"}, IPCIDRs:[]string{"198.51.100.0/24"}, Outbound:singBoxCSQTTTag, Enabled:true}},
	}
	if err := manager.SaveConfig(input); err != nil { t.Fatalf("SaveConfig valid config: %v", err) }
	before, err := os.ReadFile(config); if err != nil { t.Fatal(err) }
	if len(before) == 0 { t.Fatal("empty config persisted") }
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("LoadConfig: %v", err) }
	if !loaded.CSQTTEnabled || !loaded.CSQTTRule.Enabled { t.Fatalf("legacy CSQTT listener was not migrated to an enabled system card: %#v", loaded) }
	if len(loaded.DirectDomainSuffixes) != 1 || loaded.DirectDomainSuffixes[0] != ".example.org" { t.Fatalf("Direct suffix did not round-trip: %#v", loaded.DirectDomainSuffixes) }
	if len(loaded.Rules) != 1 || len(loaded.Rules[0].DomainKeywords) != 1 || loaded.Rules[0].DomainKeywords[0] != "stream" || len(loaded.Rules[0].DomainRegexes) != 1 || len(loaded.Rules[0].IPCIDRs) != 1 { t.Fatalf("extended route matchers did not round-trip: %#v", loaded.Rules) }
	invalid := input
	invalid.DirectDomains = []string{"bad domain"}
	if err := manager.SaveConfig(invalid); err == nil { t.Fatal("invalid config accepted") }
	after, err := os.ReadFile(config); if err != nil { t.Fatal(err) }
	if string(after) != string(before) { t.Fatal("invalid config replaced the last known-good config") }
	info, err := os.Stat(config); if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0600 { t.Fatalf("config permissions = %o, want 0600", info.Mode().Perm()) }
}


func TestSingBoxManagerRuleSetPathsStayInsideManagedDirectory(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	managed := filepath.Join(dir, "rule-sets")
	if err := os.MkdirAll(managed, 0700); err != nil { t.Fatal(err) }
	inside := filepath.Join(managed, "trusted.srs")
	if err := os.WriteFile(inside, []byte("fixture"), 0600); err != nil { t.Fatal(err) }
	if err := manager.validateRuleSetPaths([]singBoxRuleSet{{Type:"local", Tag:"trusted", Format:"binary", Path:inside}}); err != nil { t.Fatalf("managed regular file rejected: %v", err) }
	outside := filepath.Join(dir, "outside.srs")
	if err := os.WriteFile(outside, []byte("fixture"), 0600); err != nil { t.Fatal(err) }
	if err := manager.validateRuleSetPaths([]singBoxRuleSet{{Type:"local", Tag:"outside", Format:"binary", Path:outside}}); err == nil { t.Fatal("Rule Set outside managed directory was accepted") }
	link := filepath.Join(managed, "linked.srs")
	if err := os.Symlink(outside, link); err != nil { t.Fatal(err) }
	if err := manager.validateRuleSetPaths([]singBoxRuleSet{{Type:"local", Tag:"linked", Format:"binary", Path:link}}); err == nil { t.Fatal("symlinked Rule Set file was accepted") }
}

func TestSingBoxManagerRejectsSymlinkedRuleSetDirectory(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	outside := filepath.Join(dir, "outside")
	if err := os.Mkdir(outside, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(outside, "rules.srs"), []byte("fixture"), 0600); err != nil { t.Fatal(err) }
	if err := os.Symlink(outside, filepath.Join(dir, "rule-sets")); err != nil { t.Fatal(err) }
	if err := manager.validateRuleSetPaths([]singBoxRuleSet{{Type:"local", Tag:"rules", Format:"binary", Path:filepath.Join(dir, "rule-sets", "rules.srs")}}); err == nil { t.Fatal("symlinked managed directory was accepted") }
}


func TestSingBoxManagerStartRejectsTamperedPersistedRuleSetPathBeforeBinary(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	outside := filepath.Join(dir, "outside.srs")
	if err := os.WriteFile(outside, []byte("fixture"), 0600); err != nil { t.Fatal(err) }

	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080",
		CSQTTAddress: "127.0.0.1:19081",
		FinalOutbound: singBoxDirectTag,
		RuleSets: []singBoxRuleSet{{Type: "local", Tag: "tampered", Format: "binary", Path: outside}},
	})
	if err != nil { t.Fatalf("build persisted tampered config fixture: %v", err) }
	// Write directly to disk to model post-save/manual config tampering and
	// deliberately bypass SaveConfig's path-containment validation.
	if err := os.WriteFile(config, encoded, 0600); err != nil { t.Fatal(err) }

	marker := filepath.Join(dir, "binary-invoked")
	binary := newFakeSingBox(t, "touch "+shellQuoteForTest(marker)+"\nexit 0")
	manager := NewSingBoxManager(binary, config, filepath.Join(dir, "sing-box.log"))
	if err := manager.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "unsafe Rule Set path") {
		t.Fatalf("expected persisted Rule Set path rejection, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sing-box binary was invoked before rejecting tampered config (stat error: %v)", err)
	}
	if got := manager.Status(); got.Running || got.PID != 0 {
		t.Fatalf("manager started despite unsafe persisted Rule Set path: %+v", got)
	}
}

func ruleSetValidationFakeBinary(t *testing.T, mode string) string {
	t.Helper()
	script := "if [ \"$1\" = rule-set ] && [ \"$2\" = compile ]; then\n" +
		"  if grep -q INVALID \"$5\"; then echo invalid-source >&2; exit 17; fi\n" +
		"  if [ \"" + mode + "\" = timeout-source ]; then sleep 2; fi\n" +
		"  printf 'compiled-srs' > \"$4\"\n  exit 0\nfi\n" +
		"if [ \"$1\" = check ] && [ \"$2\" = -c ]; then\n" +
		"  staged=$(sed -n 's/.*\"path\":\"\\([^\"]*\\)\".*/\\1/p' \"$3\")\n" +
		"  if [ -z \"$staged\" ]; then echo missing-staged-path >&2; exit 18; fi\n" +
		"  if grep -q INVALID \"$staged\"; then echo invalid-binary >&2; exit 19; fi\n" +
		"  if [ \"" + mode + "\" = timeout-binary ]; then sleep 2; fi\n" +
		"  exit 0\nfi\necho unexpected-command >&2\nexit 20"
	return newFakeSingBox(t, script)
}

func TestSingBoxManagerUploadRuleSetRejectsInvalidSourceBeforeActivation(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, "normal"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	_, err := manager.UploadRuleSet("bad-source", "rules.json", []byte("{\"version\":1,\"rules\":[INVALID]}"))
	if err == nil || !strings.Contains(err.Error(), "invalid-source") {
		t.Fatalf("expected pinned compiler rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rule-sets", "bad-source.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid source was activated (stat error: %v)", err)
	}
}

func TestSingBoxManagerUploadRuleSetRejectsInvalidBinaryBeforeActivation(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, "normal"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	_, err := manager.UploadRuleSet("bad-binary", "rules.srs", []byte("INVALID-SRS"))
	if err == nil || !strings.Contains(err.Error(), "invalid-binary") {
		t.Fatalf("expected sing-box binary check rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rule-sets", "bad-binary.srs")); !os.IsNotExist(err) {
		t.Fatalf("invalid binary was activated (stat error: %v)", err)
	}
}

func TestSingBoxManagerUploadRuleSetFailedReplacementPreservesActiveFile(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, "normal"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	active, err := manager.UploadRuleSet("stable", "rules.json", []byte("{\"version\":1,\"rules\":[]}"))
	if err != nil {
		t.Fatalf("activate initial valid Rule Set: %v", err)
	}
	before, err := os.ReadFile(active.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.UploadRuleSet("stable", "rules.json", []byte("{\"version\":1,\"rules\":[INVALID]}"))
	if err == nil {
		t.Fatal("invalid replacement unexpectedly succeeded")
	}
	after, err := os.ReadFile(active.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed replacement changed active file: before=%q after=%q", before, after)
	}
}

func TestSingBoxManagerUploadRuleSetActivatesValidSourceAndBinary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		file       string
		data       []byte
		wantFormat string
	}{
		{name: "source", file: "rules.json", data: []byte("{\"version\":1,\"rules\":[{\"domain\":[\"example.com\"]}]}"), wantFormat: "source"},
		{name: "binary", file: "rules.srs", data: []byte("VALID-SRS"), wantFormat: "binary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, "normal"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
			result, err := manager.UploadRuleSet("valid-"+tc.name, tc.file, tc.data)
			if err != nil {
				t.Fatalf("upload valid Rule Set: %v", err)
			}
			if result.Type != "local" || result.Format != tc.wantFormat {
				t.Fatalf("unexpected Rule Set metadata: %+v", result)
			}
			got, err := os.ReadFile(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(tc.data) {
				t.Fatalf("active payload changed: got %q want %q", got, tc.data)
			}
		})
	}
}

func TestSingBoxManagerUploadRuleSetValidationTimeoutDoesNotActivate(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		data []byte
		mode string
	}{
		{name: "source", file: "rules.json", data: []byte("{\"version\":1,\"rules\":[]}"), mode: "timeout-source"},
		{name: "binary", file: "rules.srs", data: []byte("VALID-SRS"), mode: "timeout-binary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, tc.mode), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
			manager.checkTimeout = 50 * time.Millisecond
			_, err := manager.UploadRuleSet("timeout-"+tc.name, tc.file, tc.data)
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("expected validation timeout, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "rule-sets", "timeout-"+tc.name+filepath.Ext(tc.file))); !os.IsNotExist(err) {
				t.Fatalf("timed-out Rule Set was activated (stat error: %v)", err)
			}
		})
	}
}

func TestSingBoxManagerUploadRuleSetPassesPinnedBinary(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binary == "" {
		t.Skip("requires pinned sing-box binary from CI")
	}
	dir := t.TempDir()
	manager := NewSingBoxManager(binary, filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	sourceData := []byte("{\"version\":1,\"rules\":[{\"domain\":[\"example.com\"]}]}")
	source, err := manager.UploadRuleSet("pinned-source", "rules.json", sourceData)
	if err != nil {
		t.Fatalf("pinned sing-box rejected valid source Rule Set: %v", err)
	}
	if got, err := os.ReadFile(source.Path); err != nil || string(got) != string(sourceData) {
		t.Fatalf("source Rule Set was not preserved: %v", err)
	}

	binaryPath := filepath.Join(dir, "fixture.srs")
	compile := exec.Command(binary, "rule-set", "compile", "--output", binaryPath, source.Path)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("create pinned binary Rule Set fixture: %v: %s", err, output)
	}
	binaryData, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := manager.UploadRuleSet("pinned-binary", "rules.srs", binaryData)
	if err != nil {
		t.Fatalf("pinned sing-box rejected valid binary Rule Set using minimal route config: %v", err)
	}
	if got, err := os.ReadFile(uploaded.Path); err != nil || string(got) != string(binaryData) {
		t.Fatalf("binary Rule Set was not preserved: %v", err)
	}

	if _, err := manager.UploadRuleSet("pinned-invalid-source", "invalid.json", []byte("{\"version\":1,\"rules\":[INVALID]}")); err == nil {
		t.Fatal("pinned sing-box accepted malformed JSON Rule Set")
	}
	if _, err := os.Stat(filepath.Join(dir, "rule-sets", "pinned-invalid-source.json")); !os.IsNotExist(err) {
		t.Fatalf("malformed JSON Rule Set was activated (stat error: %v)", err)
	}

	if _, err := manager.UploadRuleSet("pinned-invalid", "invalid.srs", []byte("not-a-sing-box-rule-set")); err == nil {
		t.Fatal("pinned sing-box accepted invalid binary Rule Set")
	}
	if _, err := os.Stat(filepath.Join(dir, "rule-sets", "pinned-invalid.srs")); !os.IsNotExist(err) {
		t.Fatalf("invalid binary Rule Set was activated (stat error: %v)", err)
	}
}

func TestSingBoxManagerDeleteRuleSetRejectsReferencedSet(t *testing.T) {
 dir:=t.TempDir();manager:=NewSingBoxManager("unused",filepath.Join(dir,"config.json"),filepath.Join(dir,"sing-box.log"))
 if err:=os.MkdirAll(filepath.Join(dir,"rule-sets"),0700);err!=nil{t.Fatal(err)}
 path:=filepath.Join(dir,"rule-sets","used.srs");if err:=os.WriteFile(path,[]byte("fixture"),0600);err!=nil{t.Fatal(err)}
 raw:=`{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19180}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":12500}],"route":{"final":"direct","rule_set":[{"type":"local","tag":"used","format":"binary","path":"`+path+`"}],"rules":[{"action":"route","rule_set":["used"],"outbound":"csqtt"}]}}`
 if err:=os.WriteFile(manager.configPath,[]byte(raw),0600);err!=nil{t.Fatal(err)}
 if err:=manager.DeleteRuleSet("used");err==nil||!strings.Contains(err.Error(),"referenced"){t.Fatalf("expected referenced guard, got %v",err)}
 if _,err:=os.Stat(path);err!=nil{t.Fatalf("referenced Rule Set was deleted: %v",err)}
}
func TestSingBoxManagerUploadRuleSetRejectsConcurrentLifecycle(t *testing.T) {
	dir:=t.TempDir()
	manager:=NewSingBoxManager("unused",filepath.Join(dir,"config.json"),filepath.Join(dir,"sing-box.log"))
	manager.configuring=true
	if _,err:=manager.UploadRuleSet("demo","demo.json",[]byte(`{"version":3,"rules":[]}`));err==nil||!strings.Contains(err.Error(),"configuring"){
		t.Fatalf("expected upload to be rejected during configuration, got %v",err)
	}
	if _,err:=os.Stat(filepath.Join(dir,"rule-sets"));!os.IsNotExist(err){
		t.Fatalf("upload created managed directory while rejected: %v",err)
	}
}

func TestSingBoxManagerDeleteRuleSetRejectsConfiguredSet(t *testing.T) {
 dir:=t.TempDir();manager:=NewSingBoxManager("unused",filepath.Join(dir,"config.json"),filepath.Join(dir,"sing-box.log"))
 if err:=os.MkdirAll(filepath.Join(dir,"rule-sets"),0700);err!=nil{t.Fatal(err)}
 path:=filepath.Join(dir,"rule-sets","configured.srs");if err:=os.WriteFile(path,[]byte("fixture"),0600);err!=nil{t.Fatal(err)}
 raw:="{\"inbounds\":[{\"type\":\"socks\",\"tag\":\"csqtt-route-in\",\"listen\":\"127.0.0.1\",\"listen_port\":19180}],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"},{\"type\":\"socks\",\"tag\":\"csqtt\",\"server\":\"127.0.0.1\",\"server_port\":12500}],\"route\":{\"final\":\"direct\",\"rule_set\":[{\"type\":\"local\",\"tag\":\"configured\",\"format\":\"binary\",\"path\":\""+path+"\"}],\"rules\":[]}}"
 if err:=os.WriteFile(manager.configPath,[]byte(raw),0600);err!=nil{t.Fatal(err)}
 if err:=manager.DeleteRuleSet("configured");err==nil||!strings.Contains(err.Error(),"still registered"){t.Fatalf("expected configured registry guard, got %v",err)}
 if _,err:=os.Stat(path);err!=nil{t.Fatalf("configured Rule Set was deleted: %v",err)}
}

func TestSingBoxManagerDeleteRuleSetRejectsUnsafeFileAndDeletesUnreferenced(t *testing.T) {
 dir:=t.TempDir();manager:=NewSingBoxManager("unused",filepath.Join(dir,"config.json"),filepath.Join(dir,"sing-box.log"));setsDir:=filepath.Join(dir,"rule-sets")
 if err:=os.MkdirAll(setsDir,0700);err!=nil{t.Fatal(err)}
 path:=filepath.Join(setsDir,"free.json");if err:=os.WriteFile(path,[]byte("{}"),0600);err!=nil{t.Fatal(err)}
 if err:=manager.DeleteRuleSet("free");err!=nil{t.Fatal(err)}
 if _,err:=os.Stat(path);!os.IsNotExist(err){t.Fatalf("file remains: %v",err)}
 if err:=os.Symlink(filepath.Join(dir,"outside"),filepath.Join(setsDir,"unsafe.srs"));err!=nil{t.Fatal(err)}
 if err:=manager.DeleteRuleSet("unsafe");err==nil{t.Fatal("expected symlink rejection")}
}

func TestSingBoxManagerUploadRuleSetRejectsNonCanonicalRegisteredPath(t *testing.T) {
	dir := t.TempDir()
	setsDir := filepath.Join(dir, "rule-sets")
	if err := os.MkdirAll(setsDir, 0700); err != nil { t.Fatal(err) }
	config := filepath.Join(dir, "config.json")
	nonCanonical := filepath.Join(setsDir, "other.json")
	if err := os.WriteFile(nonCanonical, []byte(`{"version":3,"rules":[]}`), 0600); err != nil { t.Fatal(err) }
	encoded, err := buildSingBoxConfig(singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080", CSQTTAddress: "127.0.0.1:19081", FinalOutbound: singBoxDirectTag,
		RuleSets: []singBoxRuleSet{{Type:"local", Tag:"stable", Format:"source", Path:nonCanonical}},
	})
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(config, encoded, 0600); err != nil { t.Fatal(err) }
	manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, "normal"), config, filepath.Join(dir, "sing-box.log"))
	if _, err := manager.UploadRuleSet("stable", "stable.json", []byte(`{"version":3,"rules":[]}`)); err == nil || !strings.Contains(err.Error(), "managed path") {
		t.Fatalf("expected non-canonical configured path rejection, got %v", err)
	}
	got, err := os.ReadFile(nonCanonical)
	if err != nil || string(got) != `{"version":3,"rules":[]}` { t.Fatalf("existing configured path changed: %q, %v", got, err) }
}

func TestSingBoxManagerUploadRuleSetRejectsStaleSiblingFormat(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(ruleSetValidationFakeBinary(t, "normal"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	setsDir := filepath.Join(dir, "rule-sets")
	if err := os.MkdirAll(setsDir, 0700); err != nil { t.Fatal(err) }
	stale := filepath.Join(setsDir, "ambiguous.srs")
	if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil { t.Fatal(err) }
	if _, err := manager.UploadRuleSet("ambiguous", "ambiguous.json", []byte(`{"version":3,"rules":[]}`)); err == nil || !strings.Contains(err.Error(), "stale sibling") {
		t.Fatalf("expected stale sibling rejection, got %v", err)
	}
	if got, err := os.ReadFile(stale); err != nil || string(got) != "stale" { t.Fatalf("stale sibling changed: %q, %v", got, err) }
}

func TestSingBoxManagerUploadRuleSetRejectsRegisteredRemoteAndFormatChange(t *testing.T) {
	for _, tc := range []struct{name string; set singBoxRuleSet; filename string; want string}{
		{name:"remote", set:singBoxRuleSet{Type:"remote",Tag:"shared",Format:"source",URL:"https://example.com/rules.json"}, filename:"shared.json", want:"different source"},
		{name:"format", set:singBoxRuleSet{Type:"local",Tag:"shared",Format:"binary",Path:"/tmp/rule-sets/shared.srs"}, filename:"shared.json", want:"different source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir:=t.TempDir()
			config:=filepath.Join(dir,"config.json")
			encoded,err:=buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19080",CSQTTAddress:"127.0.0.1:19081",FinalOutbound:singBoxDirectTag,RuleSets:[]singBoxRuleSet{tc.set}})
			if err!=nil { t.Fatal(err) }
			if err:=os.WriteFile(config,encoded,0600);err!=nil { t.Fatal(err) }
			manager:=NewSingBoxManager(ruleSetValidationFakeBinary(t,"normal"),config,filepath.Join(dir,"sing-box.log"))
			if _,err:=manager.UploadRuleSet("shared",tc.filename,[]byte(`{"version":3,"rules":[]}`));err==nil||!strings.Contains(err.Error(),tc.want) {
				t.Fatalf("expected source/format conflict, got %v",err)
			}
		})
	}
}

func TestSingBoxManagerDeleteRuleSetRejectsAdvancedNestedRouteReference(t *testing.T) {
 dir := t.TempDir()
 config := filepath.Join(dir, "config.json")
 manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), config, filepath.Join(dir, "sing-box.log"))
 ruleSetDir := filepath.Join(dir, "rule-sets")
 if err := os.MkdirAll(ruleSetDir, 0700); err != nil { t.Fatal(err) }
 path := filepath.Join(ruleSetDir, "hidden-ref.srs")
 if err := os.WriteFile(path, []byte("managed ruleset"), 0600); err != nil { t.Fatal(err) }
 encoded, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19080", CSQTTAddress:"127.0.0.1:19081",FinalOutbound:singBoxDirectTag})
 if err != nil { t.Fatal(err) }
 var raw map[string]any
 if err := json.Unmarshal(encoded, &raw); err != nil { t.Fatal(err) }
 route := raw["route"].(map[string]any)
 route["rules"] = append(route["rules"].([]any), map[string]any{
  "action":"reject", "any":[]any{map[string]any{"rule_set":[]any{"hidden-ref"}}},
 })
 encoded, err = json.Marshal(raw)
 if err != nil { t.Fatal(err) }
 if err := os.WriteFile(config, encoded, 0600); err != nil { t.Fatal(err) }
 if err := manager.DeleteRuleSet("hidden-ref"); err == nil || !strings.Contains(err.Error(), "referenced by a route rule") {
  t.Fatalf("expected nested advanced route reference rejection, got %v", err)
 }
 if got, err := os.ReadFile(path); err != nil || string(got) != "managed ruleset" {
  t.Fatalf("referenced Rule Set was changed: %q, %v", got, err)
 }
}

func TestSingBoxValueReferencesRuleSetRecursive(t *testing.T) {
 value := []any{map[string]any{"logical":map[string]any{"any":[]any{map[string]any{"rule_set":[]any{"target"}}}}}}
 if !singBoxValueReferencesRuleSet(value, "target") { t.Fatal("nested Rule Set reference was not detected") }
 if singBoxValueReferencesRuleSet(value, "missing") { t.Fatal("unrelated Rule Set tag was reported") }
}


func TestSingBoxManagerRoundTripsExternalDefaultWithoutCSQTT(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "if [ \"$1\" = check ]; then exit 0; fi\nexit 0"), config, filepath.Join(dir, "sing-box.log"))
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080",
		FinalOutbound: "socks-hydra",
		SOCKS5: []singBoxSOCKS5Outbound{{
			ID: "hydra", Server: "192.0.2.20", ServerPort: 1080,
			Username: "route-user", Password: "route-pass", Enabled: true,
		}},
	}
	if err := manager.SaveConfig(input); err != nil {
		t.Fatalf("SaveConfig external-only config: %v", err)
	}
	loaded, err := manager.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig external-only config: %v", err)
	}
	if loaded.CSQTTEnabled || loaded.CSQTTAddress != "" {
		t.Fatalf("CSQTT should remain disabled, got enabled=%v address=%q", loaded.CSQTTEnabled, loaded.CSQTTAddress)
	}
	if loaded.FinalOutbound != "socks-hydra" {
		t.Fatalf("default outbound = %q, want socks-hydra", loaded.FinalOutbound)
	}
	if len(loaded.SOCKS5) != 1 || loaded.SOCKS5[0].ID != "hydra" || loaded.SOCKS5[0].Server != "192.0.2.20" || loaded.SOCKS5[0].ServerPort != 1080 {
		t.Fatalf("external SOCKS5 did not round-trip: %#v", loaded.SOCKS5)
	}
	if loaded.SOCKS5[0].Username != "route-user" || loaded.SOCKS5[0].Password != "route-pass" {
		t.Fatalf("SOCKS5 credentials did not round-trip: %#v", loaded.SOCKS5[0])
	}
}


// TestSingBoxManagerRecoversAfterUnexpectedExit verifies that the manager's
// waiter releases the previous process generation after a crash, preserves the
// failure in Status, and permits a clean subsequent Start.
func TestSingBoxManagerRecoversAfterUnexpectedExit(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "run-count")
	script := "if [ \"$1\" = check ]; then exit 0; fi\n" +
		"count=0\n" +
		"if [ -f " + shellQuoteForTest(counter) + " ]; then read count < " + shellQuoteForTest(counter) + "; fi\n" +
		"count=$((count + 1))\n" +
		"echo $count > " + shellQuoteForTest(counter) + "\n" +
		"if [ \"$count\" -eq 1 ]; then echo simulated-crash >&2; exit 17; fi\n" +
		"exec sleep 60"
	binary := newFakeSingBox(t, script)
	config := filepath.Join(dir, "config.json")
	writeValidSingBoxConfigForTest(t, config)
	manager := NewSingBoxManager(binary, config, filepath.Join(dir, "sing-box.log"))

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status()
		if !status.Running && status.Error != "" {
			if !strings.Contains(status.Error, "exit status 17") {
				t.Fatalf("unexpected crash status: %+v", status)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status := manager.Status(); status.Running || !strings.Contains(status.Error, "exit status 17") {
		t.Fatalf("manager did not publish unexpected exit: %+v", status)
	}

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start after unexpected exit: %v", err)
	}
	status := manager.Status()
	if !status.Running || status.PID == 0 {
		t.Fatalf("manager did not start a new process generation: %+v", status)
	}
	deadline = time.Now().Add(2 * time.Second)
	var data []byte
	var err error
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(counter)
		if err == nil && strings.TrimSpace(string(data)) == "2" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("run count = %q, err=%v; want second run", data, err)
	}
	if status.Error != "" {
		t.Fatalf("stale crash error remained after successful restart: %+v", status)
	}
	if err := manager.Stop(); err != nil {
		t.Fatalf("Stop after recovery: %v", err)
	}
	if status := manager.Status(); status.Running || status.PID != 0 {
		t.Fatalf("manager retained restarted process after Stop: %+v", status)
	}
}


// TestSingBoxManagerRecoversRealSingBoxAfterForcedExit exercises the manager
// against the pinned sing-box binary: start, complete a real SOCKS5 CONNECT,
// kill the process unexpectedly, wait for waiter cleanup, restart, and relay
// another real payload through the same inbound endpoint.
func TestSingBoxManagerRecoversRealSingBoxAfterForcedExit(t *testing.T) {
	binaryPath := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
	if binaryPath == "" {
		t.Skip("requires pinned sing-box binary from CI")
	}

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

	listenAddress := reserveSingBoxTestAddress(t)
	input := singBoxConfigInput{ListenAddress: listenAddress, FinalOutbound: singBoxDirectTag}
	config, err := buildSingBoxConfig(input)
	if err != nil { t.Fatal(err) }
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sing-box-recovery.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil { t.Fatal(err) }
	manager := NewSingBoxManager(binaryPath, configPath, filepath.Join(dir, "sing-box.log"))

	waitInbound := func() {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			conn, dialErr := net.DialTimeout("tcp", listenAddress, 150*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				return
			}
			if !manager.Status().Running { break }
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("sing-box inbound did not become ready; status=%+v", manager.Status())
	}
	connectAndEcho := func(payload string) {
		t.Helper()
		conn, dialErr := net.DialTimeout("tcp", listenAddress, time.Second)
		if dialErr != nil { t.Fatalf("dial sing-box inbound: %v", dialErr) }
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil { t.Fatal(err) }
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil { t.Fatal(err) }
		if greeting != [2]byte{5, 0} { t.Fatalf("SOCKS greeting = %v", greeting) }

		target := echo.Addr().(*net.TCPAddr)
		request := []byte{5, 1, 0, 1, 127, 0, 0, 1, byte(target.Port >> 8), byte(target.Port)}
		if _, err := conn.Write(request); err != nil { t.Fatal(err) }
		var reply [10]byte
		if _, err := io.ReadFull(conn, reply[:]); err != nil { t.Fatalf("SOCKS CONNECT: %v", err) }
		if reply[1] != 0 { t.Fatalf("SOCKS CONNECT reply = %v", reply) }
		data := []byte(payload)
		if _, err := conn.Write(data); err != nil { t.Fatal(err) }
		got := make([]byte, len(data))
		if _, err := io.ReadFull(conn, got); err != nil { t.Fatalf("echo payload: %v", err) }
		if string(got) != payload { t.Fatalf("echo = %q, want %q", got, payload) }
	}

	if err := manager.Start(context.Background()); err != nil { t.Fatalf("initial Start: %v", err) }
	waitInbound()
	first := manager.Status()
	if !first.Running || first.PID <= 0 { t.Fatalf("initial process not running: %+v", first) }
	connectAndEcho("before-forced-exit")

	process, err := os.FindProcess(first.PID)
	if err != nil { t.Fatalf("find sing-box PID %d: %v", first.PID, err) }
	if err := process.Kill(); err != nil { t.Fatalf("force-kill sing-box PID %d: %v", first.PID, err) }

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status()
		if !status.Running && status.PID == 0 && status.Error != "" { break }
		time.Sleep(20 * time.Millisecond)
	}
	crashed := manager.Status()
	if crashed.Running || crashed.PID != 0 || crashed.Error == "" {
		t.Fatalf("manager did not observe forced process exit: %+v", crashed)
	}

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("restart after forced exit: %v; previous status: %+v", err, crashed)
	}
	restarted := manager.Status()
	if !restarted.Running || restarted.PID <= 0 || restarted.PID == first.PID {
		t.Fatalf("manager did not create a new process generation: first=%+v restarted=%+v", first, restarted)
	}
	if restarted.Error != "" { t.Fatalf("stale crash error after restart: %+v", restarted) }
	waitInbound()
	connectAndEcho("after-forced-exit")

	if err := manager.Stop(); err != nil { t.Fatalf("Stop after recovery: %v", err) }
	if status := manager.Status(); status.Running || status.PID != 0 {
		t.Fatalf("manager retained process after final Stop: %+v", status)
	}
}


func TestSingBoxManagerLoadConfigRejectsInvalidOrDuplicateOutboundTags(t *testing.T) {
	cases := []struct {
		name string
		outbounds string
	}{
		{name: "duplicate direct tag", outbounds: `[{"type":"direct","tag":"direct"},{"type":"socks","tag":"direct","server":"127.0.0.1","server_port":1080}]`},
		{name: "duplicate external tag", outbounds: `[{"type":"direct","tag":"direct"},{"type":"socks","tag":"socks-alpha","server":"proxy-a.example","server_port":1080},{"type":"socks","tag":"socks-alpha","server":"proxy-b.example","server_port":1080}]`},
		{name: "missing tag", outbounds: `[{"type":"direct"}]`},
		{name: "non-object outbound", outbounds: `["direct"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.json")
			raw := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":` + tc.outbounds + `, "route":{"final":"direct","rules":[]}}`
			if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
			manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
			if _, err := manager.LoadConfig(); err == nil {
				t.Fatal("persisted config with invalid or duplicate outbound tags was accepted")
			}
		})
	}
}


func TestSingBoxManagerSaveLoadPreservesDisabledSystemAndUserCards(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), configPath, filepath.Join(dir, "sing-box.log"))
	directEnabled := false
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
		FinalOutbound: singBoxDirectTag,
		DirectEnabled: &directEnabled,
		DirectDomains: []string{"direct.example"},
		CSQTTAddress: "",
		CSQTTEnabled: false,
		CSQTTRule: singBoxRouteRule{Enabled: false, Domains: []string{"csqtt.example"}},
		RouteOrder: []string{"user-disabled", "csqtt", "user-active"},
		Rules: []singBoxRouteRule{
			{ID: "user-disabled", Enabled: false, Domains: []string{"disabled.example"}, Outbound: singBoxDirectTag},
			{ID: "user-active", Enabled: true, Domains: []string{"active.example"}, Outbound: singBoxDirectTag},
		},
	}
	if err := manager.SaveConfig(input); err != nil { t.Fatalf("SaveConfig: %v", err) }
	// Simulate a process restart: restore the full model using a fresh manager
	// and only the persisted config plus its generation-bound sidecar.
	restartedManager := NewSingBoxManager(manager.binary, configPath, manager.logPath)
	loaded, err := restartedManager.LoadConfig()
	if err != nil { t.Fatalf("LoadConfig after manager restart: %v", err) }
	if loaded.DirectEnabled == nil || *loaded.DirectEnabled { t.Fatalf("Direct enabled state did not round-trip: %#v", loaded.DirectEnabled) }
	if loaded.CSQTTEnabled || loaded.CSQTTRule.Enabled { t.Fatalf("disabled CSQTT state did not round-trip: %#v", loaded.CSQTTRule) }
	if len(loaded.Rules) != 2 || loaded.Rules[0].Enabled || loaded.Rules[1].ID != "user-active" {
		t.Fatalf("disabled user rule or rule order was lost: %#v", loaded.Rules)
	}
	if got := strings.Join(loaded.RouteOrder, ","); got != "user-disabled,csqtt,user-active" {
		t.Fatalf("route priority did not round-trip: %q", got)
	}
	stateFiles, err := filepath.Glob(configPath + ".panel.*.json")
	if err != nil || len(stateFiles) != 1 { t.Fatalf("expected one hash-bound panel sidecar, got %v (%v)", stateFiles, err) }
	stateInfo, err := os.Stat(stateFiles[0])
	if err != nil { t.Fatalf("panel state sidecar missing: %v", err) }
	if stateInfo.Mode().Perm() != 0600 { t.Fatalf("panel state permissions = %o, want 0600", stateInfo.Mode().Perm()) }
}

func TestSingBoxManagerRejectsStalePanelStateAfterExternalConfigChange(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), configPath, filepath.Join(dir, "sing-box.log"))
	input := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag, Rules: []singBoxRouteRule{{ID: "saved", Enabled: true, Domains: []string{"saved.example"}, Outbound: singBoxDirectTag}}}
	if err := manager.SaveConfig(input); err != nil { t.Fatal(err) }
	// Model an external edit to a panel-managed config. The runtime JSON alone
	// cannot reconstruct disabled cards or the exact system-card priority.
	encoded, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag})
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(configPath, encoded, 0600); err != nil { t.Fatal(err) }
	if _, err := manager.LoadConfig(); err == nil || !strings.Contains(err.Error(), "changed outside the panel") {
		t.Fatalf("expected fail-closed stale panel-state error, got %v", err)
	}
}


func TestSingBoxManagerLegacyImportRejectsMisorderedOrDuplicateSniffActions(t *testing.T) {
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180",
		CSQTTAddress: "127.0.0.1:19181",
		CSQTTEnabled: true,
		FinalOutbound: singBoxDirectTag,
		CSQTTRule: singBoxRouteRule{Enabled: true, Domains: []string{"first.example"}},
		RouteOrder: []string{"csqtt"},
	}
	encoded, err := buildSingBoxConfig(input)
	if err != nil { t.Fatalf("build source config: %v", err) }
	var base map[string]any
	if err := json.Unmarshal(encoded, &base); err != nil { t.Fatal(err) }
	baseRoute := base["route"].(map[string]any)
	baseRules := baseRoute["rules"].([]any)
	if len(baseRules) != 2 { t.Fatalf("expected leading sniff and CSQTT route, got %#v", baseRules) }

	cases := []struct {
		name string
		rules []any
	}{
		{name: "sniff after route", rules: []any{baseRules[1], baseRules[0]}},
		{name: "duplicate sniff", rules: []any{baseRules[0], baseRules[0], baseRules[1]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.json")
			raw := make(map[string]any, len(base))
			for key, value := range base { raw[key] = value }
			route := make(map[string]any)
			for key, value := range baseRoute { route[key] = value }
			route["rules"] = tc.rules
			raw["route"] = route
			data, err := json.Marshal(raw)
			if err != nil { t.Fatal(err) }
			if err := os.WriteFile(config, data, 0600); err != nil { t.Fatal(err) }
			manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
			if _, err := manager.LoadConfig(); err == nil || !strings.Contains(err.Error(), "sniff action must appear only once at the beginning") {
				t.Fatalf("expected lossy sniff-order import rejection, got %v", err)
			}
		})
	}
}

func TestSingBoxManagerLegacyImportRejectsLossyDirectMatchersAndOrder(t *testing.T) {
	cases := []struct { name, rules string }{
		{name: "Direct domain keyword", rules: `[{"action":"route","outbound":"direct","domain_keyword":["video"]}]`},
		{name: "Direct domain regex", rules: `[{"action":"route","outbound":"direct","domain_regex":["video"]}]`},
		{name: "Direct Rule Set", rules: `[{"action":"route","outbound":"direct","rule_set":["blocked"]}]`},
		{name: "Direct after proxy route", rules: `[{"action":"route","outbound":"csqtt","domain":["first.example"]},{"action":"route","outbound":"direct","domain":["later.example"]}]`},
		{name: "unsupported reject action", rules: `[{"action":"reject","domain":["blocked.example"]}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.json")
			raw := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":19081}],"route":{"final":"direct","rules":` + tc.rules + `}}`
			if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
			manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
			if _, err := manager.LoadConfig(); err == nil {
				t.Fatal("lossy legacy route configuration was accepted")
			}
		})
	}
}

func TestSingBoxManagerLegacyImportPreservesCSQTTRoutePosition(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	raw := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":19081},{"type":"socks","tag":"socks-alpha","server":"alpha.example","server_port":1080}],"route":{"final":"direct","rules":[{"action":"route","outbound":"csqtt","domain":["first.example"]},{"action":"route","outbound":"socks-alpha","domain":["second.example"]}]}}`
	if err := os.WriteFile(config, []byte(raw), 0600); err != nil { t.Fatal(err) }
	manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("LoadConfig legacy route order: %v", err) }
	if got := strings.Join(loaded.RouteOrder, ","); got != "loaded-1,loaded-2" {
		t.Fatalf("legacy route order = %q, want original order", got)
	}
	generated, err := buildSingBoxConfig(loaded)
	if err != nil { t.Fatalf("rebuild imported config: %v", err) }
	var decoded map[string]any
	if err := json.Unmarshal(generated, &decoded); err != nil { t.Fatal(err) }
	rules := decoded["route"].(map[string]any)["rules"].([]any)
	if rules[1].(map[string]any)["outbound"] != "csqtt" || rules[2].(map[string]any)["outbound"] != "socks-alpha" {
		t.Fatalf("CSQTT/user rule order changed during import: %#v", rules)
	}
}


func TestSingBoxManagerConcurrentLoadsObserveCompleteConfigGenerations(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "if [ \"$1\" = check ]; then sleep 0.05; fi\nexit 0"), config, filepath.Join(dir, "sing-box.log"))
	oldInput := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag, DirectDomains: []string{"old.example"}}
	newInput := singBoxConfigInput{ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag, DirectDomains: []string{"new.example"}}
	if err := manager.SaveConfig(oldInput); err != nil { t.Fatalf("save initial generation: %v", err) }

	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	stop := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select { case <-stop: return; default: }
				loaded, err := manager.LoadConfig()
				if err != nil { select { case errCh <- err: default: }; return }
				if len(loaded.DirectDomains) != 1 || (loaded.DirectDomains[0] != "old.example" && loaded.DirectDomains[0] != "new.example") {
					select { case errCh <- fmt.Errorf("observed partial config generation: %#v", loaded.DirectDomains): default: }
					return
				}
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	if err := manager.SaveConfig(newInput); err != nil { close(stop); wg.Wait(); t.Fatalf("save next generation: %v", err) }
	time.Sleep(10 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh { t.Errorf("concurrent LoadConfig: %v", err) }
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("load committed generation: %v", err) }
	if len(loaded.DirectDomains) != 1 || loaded.DirectDomains[0] != "new.example" {
		t.Fatalf("final generation = %#v, want new.example", loaded.DirectDomains)
	}
}


func TestSingBoxManagerFailsClosedOnCorruptPanelSidecar(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), config, filepath.Join(dir, "sing-box.log"))
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag,
		Rules: []singBoxRouteRule{{ID: "saved", Enabled: false, Domains: []string{"disabled.example"}, Outbound: singBoxDirectTag}},
		RouteOrder: []string{"saved"},
	}
	if err := manager.SaveConfig(input); err != nil { t.Fatalf("SaveConfig: %v", err) }
	stateFiles, err := filepath.Glob(config + ".panel.*.json")
	if err != nil || len(stateFiles) != 1 { t.Fatalf("expected one panel sidecar, got %v (%v)", stateFiles, err) }
	if err := os.WriteFile(stateFiles[0], []byte("{corrupt"), 0600); err != nil { t.Fatal(err) }
	if _, err := manager.LoadConfig(); err == nil || !strings.Contains(err.Error(), "decode sing-box panel state") {
		t.Fatalf("corrupt sidecar was not rejected fail-closed: %v", err)
	}
}


func TestSingBoxManagerLoadsLegacyPanelSidecar(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag,
		RouteOrder: []string{"disabled"},
		Rules: []singBoxRouteRule{{ID: "disabled", Enabled: false, Domains: []string{"legacy-disabled.example"}, Outbound: singBoxDirectTag}},
	}
	encoded, err := buildSingBoxConfig(input)
	if err != nil { t.Fatalf("build legacy config: %v", err) }
	if err := os.WriteFile(config, encoded, 0600); err != nil { t.Fatal(err) }
	sum := sha256.Sum256(encoded)
	stateData, err := json.MarshalIndent(singBoxPanelState{ConfigSHA256: hex.EncodeToString(sum[:]), Input: input}, "", "  ")
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(config+".panel.json", append(stateData, '\n'), 0600); err != nil { t.Fatal(err) }
	manager := NewSingBoxManager("unused", config, filepath.Join(dir, "sing-box.log"))
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("LoadConfig legacy sidecar: %v", err) }
	if len(loaded.Rules) != 1 || loaded.Rules[0].Enabled || loaded.Rules[0].ID != "disabled" {
		t.Fatalf("legacy disabled-card state was not restored: %#v", loaded.Rules)
	}
}
