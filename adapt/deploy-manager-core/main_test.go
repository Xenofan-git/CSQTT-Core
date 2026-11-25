package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func goodRequest() Request {
	return Request{Protocol: "CSQTT", Host: "72.56.81.131", SSHPort: 22, User: "root", UsePassword: true, Password: "example-test-ssh", MainPassword: "tunnel123", WebUser: "admin", WebPassword: "web456", DeviceID: "test-device-123"}
}
func TestValidateDeploymentTargetAndAuthentication(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "192.168.1.1", "10.0.0.1", "169.254.1.1", "", "https://example.test", "example.test", "0.0.0.0"} {
		req := goodRequest()
		req.Host = host
		if validate(req) == nil {
			t.Fatalf("unsafe target allowed %q", host)
		}
	}
	for _, host := range []string{"72.56.81.131", "100.70.73.58"} {
		req := goodRequest()
		req.Host = host
		if err := validate(req); err != nil {
			t.Fatalf("expected valid host %s: %s", host, err)
		}
	}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Protocol = "OpenFlux" },
		func(r *Request) { r.MainPassword = "bad secret" },
		func(r *Request) { r.WebPassword = "" },
		func(r *Request) { r.DeviceID = "bad/device" },
		func(r *Request) { r.CorePort = 46000; r.WarpPort = 46002; r.ManualPorts = true },
		func(r *Request) { r.DockerInstall = true },
		func(r *Request) { r.SSHPort = 65536 },
		func(r *Request) { r.User = "pi" },
	} {
		req := goodRequest()
		mutate(&req)
		if validate(req) == nil {
			t.Fatalf("unsafe request accepted: %+v", req)
		}
	}
}
func TestDeployManagerVerifiesPinnedScriptAndArm64Server(t *testing.T) {
	assets := os.Getenv("CSQTT_DEPLOY_FIXTURE_ASSETS")
	if assets == "" {
		t.Skip("set CSQTT_DEPLOY_FIXTURE_ASSETS to pinned isolated fixture")
	}
	if err := checkAssets(assets); err != nil {
		t.Fatalf("pinned assets: %v", err)
	}
	dir := t.TempDir()
	source := filepath.Join(assets, "deploy-csqtt-core-250.sh")
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy-csqtt-core-250.sh"), content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "csqtt-core8-linux-arm64"), []byte("not-an-ELF"), 0600); err != nil {
		t.Fatal(err)
	}
	if checkAssets(dir) == nil {
		t.Fatal("unverified server ELF was allowed")
	}
	if err := os.Remove(filepath.Join(dir, "deploy-csqtt-core-250.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(dir, "deploy-csqtt-core-250.sh")); err != nil {
		t.Fatal(err)
	}
	if checkAssets(dir) == nil {
		t.Fatal("symlink installer allowed")
	}
}
func TestInventoryOnlyNeverStartsDeploy(t *testing.T) {
	assets := os.Getenv("CSQTT_DEPLOY_FIXTURE_ASSETS")
	if assets == "" {
		t.Skip("set CSQTT_DEPLOY_FIXTURE_ASSETS to pinned isolated fixture")
	}
	mockDir := t.TempDir()
	capture := filepath.Join(mockDir, "captured-args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + capture + "'\nprintf '%s\\n' 'CSQTT_INV|server|place=core|runtime=systemd|state=absent|owner=none' 'CSQTT_INV|interface|name=csqtt-core0|state=absent|owner=unverified' 'CSQTT_INV|database|state=absent|name=csqtt.db' 'CSQTT_INV|port|proto=udp|port=47000|state=free|owner=none' 'CSQTT_INV|port|proto=tcp|port=47002|state=free|owner=none' 'CSQTT_INV|end'\n"
	if err := os.WriteFile(filepath.Join(mockDir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	req := `{"protocol":"CSQTT","host":"72.56.81.131","sshPort":22,"user":"root","usePassword":true,"password":"example-test-ssh","mainPassword":"tunnel123","webUser":"admin","webPassword":"web456","deviceId":"test-device-123"}`
	var output bytes.Buffer
	err := run(strings.NewReader(req), &output, assets)
	if !errors.Is(err, ErrIsolationRequired) {
		t.Fatalf("expected fail-closed exit73: %v", err)
	}
	if !strings.Contains(output.String(), "CSQTT_INV|end") {
		t.Fatalf("no genuine inventory: %s", output.String())
	}
	args, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "example-test-ssh") || strings.Contains(output.String(), "example-test-ssh") {
		t.Fatal("SSH password leaked via argv/output")
	}
	if !strings.Contains(string(args), "bash -s inventory") {
		t.Fatal("unexpected SSH command invoked")
	}
	if strings.Contains(string(args), "install") || strings.Contains(string(args), "uninstall") {
		t.Fatal("mutating SSH command invoked")
	}
	for _, name := range []string{"scp", "rsync", "curl"} {
		if _, err := os.Stat(filepath.Join(mockDir, name)); err == nil {
			t.Fatalf("unexpected tool %s", name)
		}
	}
}

// This fixture matches the actual tagged output verified by SSH on Debian13 ARM64.
func actualARM64Inventory() string {
	return strings.Join([]string{
		"CSQTT_INV|server|place=core|runtime=systemd|state=running|owner=core_candidate",
		"CSQTT_INV|interface|name=csqtt-core0|state=present|owner=unverified",
		"CSQTT_INV|database|state=present|name=csqtt.db",
		"CSQTT_INV|port|proto=udp|port=47000|state=busy|owner=core_candidate",
		"CSQTT_INV|port|proto=tcp|port=47002|state=busy|owner=core_candidate",
		"CSQTT_INV|end",
	}, "\n")
}
func TestInventoryValidationFailsClosed(t *testing.T) {
	good := actualARM64Inventory()
	lines, err := validatedInventory(good)
	if err != nil || len(lines) != 6 {
		t.Fatalf("ARM64 inventory rejected: %v", err)
	}
	cases := map[string]string{
		"empty":                  "",
		"missing_end":            strings.Replace(good, "\nCSQTT_INV|end", "", 1),
		"missing_tcp":            strings.Replace(good, "\nCSQTT_INV|port|proto=tcp|port=47002|state=busy|owner=core_candidate", "", 1),
		"duplicate_udp":          strings.Replace(good, "\nCSQTT_INV|end", "\nCSQTT_INV|port|proto=udp|port=47000|state=free|owner=none\nCSQTT_INV|end", 1),
		"foreign_owner":          strings.Replace(good, "owner=core_candidate\nCSQTT_INV|end", "owner=foreign\nCSQTT_INV|end", 1),
		"unknown_owner":          strings.Replace(good, "owner=core_candidate\nCSQTT_INV|end", "owner=unknown\nCSQTT_INV|end", 1),
		"error_record":           strings.Replace(good, "\nCSQTT_INV|end", "\nCSQTT_INV|error|reason=udp_socket_query_failed\nCSQTT_INV|end", 1),
		"terminal_extra":         good + "\nCSQTT_INV|port|proto=udp|port=47000|state=free|owner=none",
		"dangerous_escape":       strings.Replace(good, "csqtt-core0", "csqtt-core0\x1b[31m", 1),
		"line_too_long":          strings.Repeat("A", 515) + "\n" + good,
		"oversized":              good + strings.Repeat("A", 65536),
		"docker_not_approved":    strings.Replace(good, "runtime=systemd", "runtime=docker", 1),
		"unexpected_port":        strings.Replace(good, "port=47002", "port=46002", 1),
		"banner":                 "Welcome to VPS\n" + good,
		"tampered_running_owner": strings.Replace(good, "state=running|owner=core_candidate", "state=running|owner=foreign", 1),
		"stopped_foreign_owner":  strings.Replace(good, "state=running|owner=core_candidate", "state=stopped|owner=foreign", 1),
		"stopped_unknown_owner":  strings.Replace(good, "state=running|owner=core_candidate", "state=stopped|owner=unknown", 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := validatedInventory(raw); err == nil {
				t.Fatal("untrusted inventory was accepted")
			}
		})
	}
}
func TestInventoryBufferIsBounded(t *testing.T) {
	var b boundedInventoryBuffer
	if n, err := b.Write([]byte("safe")); err != nil || n != 4 {
		t.Fatalf("short write: %d %v", n, err)
	}
	if _, err := b.Write(bytes.Repeat([]byte("x"), 65536)); err == nil || !b.truncated {
		t.Fatal("unbounded SSH output was accepted")
	}
	if b.Len() != 4 {
		t.Fatal("oversize write mutated previously captured output")
	}
}
func TestReadOnlyInventoryCancellation(t *testing.T) {
	dir := t.TempDir()
	installer := filepath.Join(dir, "deploy-csqtt-core-250.sh")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\nprintf 'CSQTT_INV|end\\n'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := goodRequest()
	var output bytes.Buffer
	if err := remoteInventoryWithContext(ctx, req, installer, &output); err == nil {
		t.Fatal("already-canceled inventory was not stopped")
	}
	if strings.Contains(output.String(), "CSQTT_INV|end") {
		t.Fatal("canceled command incorrectly accepted")
	}
}

// This optional source fixture is collected via an explicit read-only
// SSH inventory call to the dedicated ARM64 QEMU guest, never the VPS.
func TestRecordedARM64GuestInventory(t *testing.T) {
	file := os.Getenv("CSQTT_DEPLOY_VM_INVENTORY_FILE")
	if file == "" {
		t.Skip("no local read-only ARM64 VM inventory fixture")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := validatedInventory(string(data))
	if err != nil {
		t.Fatalf("actual guest report rejected: %v", err)
	}
	if len(lines) != 6 {
		t.Fatalf("expected 6 records, got %d", len(lines))
	}
}

// A hung SSH subprocess must be canceled promptly rather than leaving the
// progress window in an endless "in progress" state. No real SSH is contacted.
func TestReadOnlyInventoryHungSSHTimeout(t *testing.T) {
	dir := t.TempDir()
	installer := filepath.Join(dir, "deploy-csqtt-core-250.sh")
	if err := os.WriteFile(installer, []byte("# read-only fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(dir, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nsleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	var output bytes.Buffer
	err := remoteInventoryWithContext(ctx, goodRequest(), installer, &output)
	if err == nil {
		t.Fatal("hung SSH unexpectedly succeeded")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("SSH cancel took %s, expected under 2 seconds", elapsed)
	}
	if output.Len() != 0 {
		t.Fatal("hung SSH produced trusted inventory output")
	}
}
