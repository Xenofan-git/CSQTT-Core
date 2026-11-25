package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + capture + "'\nprintf '%s\\n' 'CSQTT_INV|server|place=core state=absent owner=none' 'CSQTT_INV|port|proto=udp port=47000 state=free owner=none' 'CSQTT_INV|port|proto=tcp port=47002 state=free owner=none' 'CSQTT_INV|end'\n"
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
