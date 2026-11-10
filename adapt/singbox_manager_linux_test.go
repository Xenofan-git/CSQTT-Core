//go:build linux

package main

import (
	"encoding/json"
	"context"
	"net"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		CSQTTAddress: "127.0.0.1:19081",
	})
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, encoded, 0600); err != nil { t.Fatal(err) }
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
		ListenAddress: "127.0.0.1:19080", CSQTTAddress: "127.0.0.1:19081",
		DirectDomainSuffixes: []string{".example.org"},
		Rules: []singBoxRouteRule{{ID:"route-a", DomainKeywords:[]string{"stream"}, DomainRegexes:[]string{"video"}, IPCIDRs:[]string{"198.51.100.0/24"}, Outbound:singBoxCSQTTTag, Enabled:true}},
	}
	if err := manager.SaveConfig(input); err != nil { t.Fatalf("SaveConfig valid config: %v", err) }
	before, err := os.ReadFile(config); if err != nil { t.Fatal(err) }
	if len(before) == 0 { t.Fatal("empty config persisted") }
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("LoadConfig: %v", err) }
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
 raw:=`{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19180}],"outbounds":[{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":12500}],"route":{"rule_set":[{"type":"local","tag":"used","format":"binary","path":"`+path+`"}],"rules":[{"action":"route","rule_set":["used"],"outbound":"csqtt"}]}}`
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
 raw:="{\"inbounds\":[{\"type\":\"socks\",\"tag\":\"csqtt-route-in\",\"listen\":\"127.0.0.1\",\"listen_port\":19180}],\"outbounds\":[{\"type\":\"socks\",\"tag\":\"csqtt\",\"server\":\"127.0.0.1\",\"server_port\":12500}],\"route\":{\"rule_set\":[{\"type\":\"local\",\"tag\":\"configured\",\"format\":\"binary\",\"path\":\""+path+"\"}],\"rules\":[]}}"
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
		ListenAddress: "127.0.0.1:19080", CSQTTAddress: "127.0.0.1:19081",
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
			encoded,err:=buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19080",CSQTTAddress:"127.0.0.1:19081",RuleSets:[]singBoxRuleSet{tc.set}})
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
 encoded, err := buildSingBoxConfig(singBoxConfigInput{ListenAddress:"127.0.0.1:19080", CSQTTAddress:"127.0.0.1:19081"})
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


func TestSingBoxManagerPanelStatePreservesDisabledRulesAndOutbounds(t *testing.T) {
	dir:=t.TempDir();config:=filepath.Join(dir,"config.json")
	manager:=NewSingBoxManager(newFakeSingBox(t,"if [ \"$1\" = check ]; then exit 0; fi\nexit 0"),config,filepath.Join(dir,"sing-box.log"))
	input:=singBoxConfigInput{
		ListenAddress:"127.0.0.1:19080",CSQTTAddress:"127.0.0.1:19081",CSQTTEnabled:false,CSQTTExplicitlyDisabled:true,FinalOutbound:singBoxDirectTag,
		SOCKS5:[]singBoxSOCKS5Outbound{
			{ID:"active",Server:"192.0.2.10",ServerPort:1080,Enabled:true},
			{ID:"disabled",Server:"192.0.2.11",ServerPort:1081,Username:"saved-user",Password:"saved-password",Enabled:false},
		},
		Rules:[]singBoxRouteRule{
			{ID:singBoxDirectRuleID,Domains:[]string{"disabled-direct.test"},Outbound:singBoxDirectTag,Enabled:false},
			{ID:"disabled-user",Domains:[]string{"disabled-user.test"},Outbound:"socks-active",Enabled:false},
			{ID:singBoxCSQTTRuleID,Outbound:singBoxCSQTTTag,Enabled:false},
			{ID:"active-user",Domains:[]string{"active-user.test"},Outbound:"socks-active",Enabled:true},
		},
	}
	if err:=manager.SaveConfig(input);err!=nil{t.Fatalf("SaveConfig: %v",err)}
	loaded,err:=manager.LoadConfig();if err!=nil{t.Fatalf("LoadConfig: %v",err)}
	if !reflect.DeepEqual(loaded,input){t.Fatalf("full panel state did not round-trip:\nloaded=%#v\nwant=%#v",loaded,input)}
	statePath:=manager.panelStatePath();info,err:=os.Stat(statePath);if err!=nil{t.Fatalf("panel state sidecar missing: %v",err)}
	if info.Mode().Perm()!=0600{t.Fatalf("panel state permissions=%o, want 0600",info.Mode().Perm())}
	data,err:=os.ReadFile(config);if err!=nil{t.Fatal(err)}
	var raw map[string]any;if err:=json.Unmarshal(data,&raw);err!=nil{t.Fatal(err)}
	outbounds:=raw["outbounds"].([]any);var disabledCSQTT,disabledSOCKS bool
	for _,item:=range outbounds{entry:=item.(map[string]any);if entry["tag"]==singBoxCSQTTDisabledTag{disabledCSQTT=true};if entry["tag"]=="socks-disabled"{disabledSOCKS=true}}
	if !disabledCSQTT||disabledSOCKS{t.Fatalf("disabled connection state leaked into active routing config: %#v",outbounds)}
}

func TestSingBoxManagerRejectsPanelStateWhenActiveConfigChanges(t *testing.T) {
	dir:=t.TempDir();config:=filepath.Join(dir,"config.json")
	manager:=NewSingBoxManager(newFakeSingBox(t,"if [ \"$1\" = check ]; then exit 0; fi\nexit 0"),config,filepath.Join(dir,"sing-box.log"))
	input:=singBoxConfigInput{ListenAddress:"127.0.0.1:19080",FinalOutbound:singBoxDirectTag}
	if err:=manager.SaveConfig(input);err!=nil{t.Fatal(err)}
	data,err:=os.ReadFile(config);if err!=nil{t.Fatal(err)}
	if err:=os.WriteFile(config,append(data,' '),0600);err!=nil{t.Fatal(err)}
	if _,err:=manager.LoadConfig();err==nil||!strings.Contains(err.Error(),"does not match the active config"){
		t.Fatalf("mismatched panel state was not rejected: %v",err)
	}
}

func TestSingBoxManagerLoadSavePreservesDNSAndExplicitDirectRoute(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "if [ \"$1\" = check ]; then exit 0; fi\nexit 0"), config, filepath.Join(dir, "sing-box.log"))
	input := singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080",
		CSQTTAddress: "127.0.0.1:19081",
		DNSMode: "udp", DNSServer: "127.0.0.1", DNSPort: 5353, DNSStrategy: "ipv6_only",
		DirectDomains: []string{"bypass.example"},
		Rules: []singBoxRouteRule{{ID: "route-direct", Domains: []string{"route-direct.example"}, Outbound: singBoxDirectTag, Enabled: true}},
	}
	if err := manager.SaveConfig(input); err != nil { t.Fatalf("SaveConfig: %v", err) }
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("LoadConfig: %v", err) }
	if loaded.DNSMode != "udp" || loaded.DNSServer != "127.0.0.1" || loaded.DNSPort != 5353 || loaded.DNSStrategy != "ipv6_only" {
		t.Fatalf("DNS settings did not round-trip: %#v", loaded)
	}
	if len(loaded.DirectDomains) != 1 || loaded.DirectDomains[0] != "bypass.example" {
		t.Fatalf("Direct exclusions changed: %#v", loaded.DirectDomains)
	}
	if len(loaded.Rules) != 1 || loaded.Rules[0].Outbound != singBoxDirectTag || len(loaded.Rules[0].Domains) != 1 || loaded.Rules[0].Domains[0] != "route-direct.example" {
		t.Fatalf("explicit Route -> DIRECT rule was folded into Direct exclusions: %#v", loaded.Rules)
	}
	if err := manager.SaveConfig(loaded); err != nil { t.Fatalf("SaveConfig after reload: %v", err) }
	data, err := os.ReadFile(config)
	if err != nil { t.Fatal(err) }
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil { t.Fatal(err) }
	rules := raw["route"].(map[string]any)["rules"].([]any)
	var bypass, explicit bool
	for _, item := range rules {
		rule := item.(map[string]any)
		if rule["outbound"] == singBoxDirectBypassTag && stringSlice(rule["domain"])[0] == "bypass.example" { bypass = true }
		if rule["outbound"] == singBoxDirectTag && stringSlice(rule["domain"])[0] == "route-direct.example" { explicit = true }
	}
	if !bypass || !explicit { t.Fatalf("Direct and Route DIRECT were not kept distinct: %#v", rules) }
}

func TestSingBoxManagerMigratesLegacyDirectExclusions(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), config, filepath.Join(dir, "sing-box.log"))
	legacy := `{"inbounds":[{"type":"socks","tag":"csqtt-route-in","listen":"127.0.0.1","listen_port":19080}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"csqtt","server":"127.0.0.1","server_port":19081,"version":"5"}],"route":{"rules":[{"inbound":["csqtt-route-in"],"action":"sniff"},{"inbound":["csqtt-route-in"],"action":"route","domain":["legacy-bypass.example"],"outbound":"direct"}],"final":"csqtt","rule_set":[]}}`
	if err := os.WriteFile(config, []byte(legacy), 0600); err != nil { t.Fatal(err) }
	loaded, err := manager.LoadConfig()
	if err != nil { t.Fatalf("load legacy config: %v", err) }
	if len(loaded.DirectDomains) != 1 || loaded.DirectDomains[0] != "legacy-bypass.example" || len(loaded.Rules) != 1 || loaded.Rules[0].ID != singBoxCSQTTRuleID || !loaded.Rules[0].Enabled || loaded.FinalOutbound != singBoxDirectTag {
		t.Fatalf("legacy Direct exclusion / CSQTT fallback migration failed: %#v", loaded)
	}
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
