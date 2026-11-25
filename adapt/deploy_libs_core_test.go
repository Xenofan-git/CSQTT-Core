package libs

import (
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
