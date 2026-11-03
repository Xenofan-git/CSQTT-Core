//go:build linux

package main

import (
	"context"
	"net"
	"fmt"
	"os"
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
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil { t.Fatal(err) }
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
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil { t.Fatal(err) }
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
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil { t.Fatal(err) }
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
	input := singBoxConfigInput{ListenAddress: "127.0.0.1:19080", CSQTTAddress: "127.0.0.1:19081"}
	if err := manager.SaveConfig(input); err != nil { t.Fatalf("SaveConfig valid config: %v", err) }
	before, err := os.ReadFile(config); if err != nil { t.Fatal(err) }
	if len(before) == 0 { t.Fatal("empty config persisted") }
	invalid := input
	invalid.DirectDomains = []string{"bad domain"}
	if err := manager.SaveConfig(invalid); err == nil { t.Fatal("invalid config accepted") }
	after, err := os.ReadFile(config); if err != nil { t.Fatal(err) }
	if string(after) != string(before) { t.Fatal("invalid config replaced the last known-good config") }
	info, err := os.Stat(config); if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0600 { t.Fatalf("config permissions = %o, want 0600", info.Mode().Perm()) }
}
