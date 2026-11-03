//go:build linux

package main

import (
	"context"
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
