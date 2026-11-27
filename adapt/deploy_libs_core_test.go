package libs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSQTTDeployBridgeRejectsInvalidRequestsBeforeStartingManager(t *testing.T) {
	invalid := []struct{ json, fragment string }{
		{"{", "ошибка разбора параметров"},
		{"{\"protocol\":\"CSQTT\",\"host\":\"\"}", "разрешён только CSQTT"},
		{"{\"protocol\":\"VLESS\",\"host\":\"72.56.81.131\"}", "разрешён только CSQTT"},
	}
	for _, tc := range invalid {
		if DeployProtocol(tc.json) {
			t.Fatalf("invalid request accepted")
		}
		if DeployBusy() {
			t.Fatal("busy remained true after invalid input")
		}
		if !strings.Contains(DeployLog(), tc.fragment) {
			t.Fatalf("missing error: %q", DeployLog())
		}
	}
}
func TestCSQTTDeployBridgeNoManagerDoesNotClaimSuccess(t *testing.T) {
	if resolveDeployManagerPath() != "" {
		t.Skip("external manager present in test directory")
	}
	valid := "{\"protocol\":\"CSQTT\",\"host\":\"72.56.81.131\",\"user\":\"root\"}"
	if DeployProtocol(valid) {
		t.Fatal("no manager but operation claimed started")
	}
	if DeployBusy() {
		t.Fatal("busy flag was not released")
	}
	if !strings.Contains(DeployLog(), "не найден deploy-manager") {
		t.Fatalf("unexpected log: %q", DeployLog())
	}
}

// A privileged manager is never selected from cwd or via a writable or
// symlinked file. These tests cannot initiate SSH or install anything.
func TestCSQTTDeployBridgeRejectsUnsafeHelperFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "deploy-manager")
	if err := os.WriteFile(good, []byte("fixture, never executed"), 0755); err != nil {
		t.Fatal(err)
	}
	if !validDeployManagerExecutable(good) {
		t.Fatal("regular executable rejected")
	}
	for name, mode := range map[string]os.FileMode{"world-writable": 0777, "group-writable": 0775, "non-executable": 0644} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("fixture, never executed"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if validDeployManagerExecutable(path) {
			t.Fatalf("unsafe file accepted: %s", name)
		}
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(good, alias); err != nil {
		t.Fatal(err)
	}
	if validDeployManagerExecutable(alias) {
		t.Fatal("symlink helper accepted")
	}
	if validDeployManagerExecutable(dir) {
		t.Fatal("directory accepted as executable")
	}
	if isVerifiedInventoryManager(good) {
		t.Fatal("unreviewed helper passed the SHA-256 pin")
	}
}

func TestCSQTTDeployBridgeRejectsUninstallInInventoryMode(t *testing.T) {
	if DeployProtocol(`{"protocol":"CSQTT","host":"192.0.2.77","uninstall":true}`) {
		t.Fatal("inventory-only bridge accepted uninstall")
	}
	busy, logText := DeploySnapshot()
	if busy || !strings.Contains(logText, "удаление недоступно") {
		t.Fatalf("unexpected terminal state: busy=%t log=%q", busy, logText)
	}
}

func TestDeployActionCompatibility(t *testing.T) {
	if _, err := deployRequestForMode(`{"action":"install","host":"192.0.2.77","Host":"192.0.2.78"}`, false); err == nil {
		t.Fatal("duplicate fields normalized away")
	}

	raw := `{"protocol":"CSQTT","host":"192.0.2.77","action":"install","password":"fixture-secret"}`
	forwarded, err := deployRequestForMode(raw, false)
	if err != nil || strings.Contains(forwarded, "action") || !strings.Contains(forwarded, "fixture-secret") {
		t.Fatal("inventory wire compatibility")
	}
	for _, action := range []string{"update", "uninstall", "purge"} {
		if _, err := deployRequestForMode(strings.Replace(raw, "install", action, 1), false); err == nil {
			t.Fatal("unsupported inventory action")
		}
	}
	if got, err := deployRequestForMode(strings.Replace(raw, "install", "update", 1), true); err != nil || !strings.Contains(got, "update") {
		t.Fatal("update not forwarded")
	}
}
