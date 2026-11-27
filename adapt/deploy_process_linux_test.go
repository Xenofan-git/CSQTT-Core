//go:build linux

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lalune-desktop/Libs"
)

// The actual pinned ARM64 manager is executed by the controller. SSH is
// deliberately a synthetic executable with no network capability, and
// every credential is a disposable fixture. This is NOT remote Deploy.
func TestDeployActualPinnedARM64ProcessContract(t *testing.T) {
	if os.Getenv("CSQTT_DEPLOY_PROCESS_FIXTURE") != "1" {
		t.Skip("requires separately staged pinned ARM64 helper and assets")
	}
	if libs.DeployMode() != "inventory-only" {
		t.Fatal("expected the real pinned ARM64 helper beside test executable")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("LALUNE_PANEL_PASSWORD", "fixture-panel-only")
	t.Setenv("PATH", dir)
	t.Setenv("CSQTT_FIXTURE_CALLS", filepath.Join(dir, "calls"))
	t.Setenv("CSQTT_FIXTURE_MODE", "valid")
	mock := `#!/bin/sh
set -eu
printf '%s\n' "$@" >> "$CSQTT_FIXTURE_CALLS"
last=
for a in "$@"; do last=$a; done
[ "$last" = 'bash -s inventory' ] || exit 91
[ "$CSQTT_DEPLOY_SSH_PASSWORD" = 'fixture-ssh-only' ] || exit 92
sum=$(/usr/bin/sha256sum)
[ "${sum%% *}" = '18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68' ] || exit 93
/bin/sleep 0.3
if [ "$CSQTT_FIXTURE_MODE" = invalid ]; then printf '%s\n' 'CSQTT_INV|end'; exit 0; fi
if [ "$CSQTT_FIXTURE_MODE" = reordered ]; then
 printf '%s\n' 'CSQTT_INV|server|owner=none|state=absent|runtime=systemd|place=core' 'CSQTT_INV|interface|owner=unverified|state=absent|name=csqtt-core0' 'CSQTT_INV|database|name=csqtt.db|state=absent' 'CSQTT_INV|port|owner=none|state=free|port=47000|proto=udp' 'CSQTT_INV|port|owner=none|state=free|port=47002|proto=tcp' 'CSQTT_INV|end'; exit 0
fi
if [ "$CSQTT_FIXTURE_MODE" = bad_owner ]; then
 printf '%s\n' 'CSQTT_INV|server|place=core|runtime=systemd|state=absent|owner=none' 'CSQTT_INV|interface|name=csqtt-core0|state=absent|owner=core_candidate' 'CSQTT_INV|database|state=absent|name=csqtt.db' 'CSQTT_INV|port|proto=udp|port=47000|state=free|owner=none' 'CSQTT_INV|port|proto=tcp|port=47002|state=free|owner=none' 'CSQTT_INV|end'; exit 0
fi
printf '%s\n' 'CSQTT_INV|server|place=core|runtime=systemd|state=absent|owner=none' 'CSQTT_INV|interface|name=csqtt-core0|state=absent|owner=unverified' 'CSQTT_INV|database|state=absent|name=csqtt.db' 'CSQTT_INV|port|proto=udp|port=47000|state=free|owner=none' 'CSQTT_INV|port|proto=tcp|port=47002|state=free|owner=none' 'CSQTT_INV|end'
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	host := "192.0.2.77"
	if err := saveDeploySSHPassword(host, "fixture-ssh-only"); err != nil {
		t.Fatal(err)
	}
	if err := saveDeployCredentials(host, deploySavedCredentials{MainPassword: "FixtureTunnel123", WebLogin: "FixtureAdmin", WebPassword: "FixtureWeb123"}); err != nil {
		t.Fatal(err)
	}
	handler := panelSecurity(panelAPIHandler(apiHandlerWithSingBox(nil, nil)))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://fixture:1061/api"+path, strings.NewReader(body))
		r.SetBasicAuth("admin", "fixture-panel-only")
		r.RemoteAddr = "192.168.4.12:1122"
		r.Header.Set("Origin", "http://fixture:1061")
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d", method, path, rec.Code)
		}
		return rec
	}
	post := func(body string, want bool) {
		t.Helper()
		rec := request(http.MethodPost, "/deploy", body)
		var v struct{ OK bool }
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.OK != want {
			t.Fatalf("POST /deploy accepted=%t want=%t err=%v", v.OK, want, err)
		}
	}
	status := func() map[string]any {
		t.Helper()
		rec := request(http.MethodGet, "/deploy/status", "")
		var v map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	waitResult := func(want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			v := status()
			if v["mode"] != "inventory-only" {
				t.Fatal("mode changed")
			}
			if v["deploying"] == false {
				if v["result"] != want {
					t.Fatalf("terminal result=%v want=%s", v, want)
				}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("manager did not terminate")
	}
	payload := `{"protocol":"CSQTT","host":"192.0.2.77","sshPort":22,"user":"root","usePassword":true,"deviceId":"fixture-device"}`
	post(payload, true)
	post(payload, false) // concurrent requests must not start a second helper
	waitResult("preflight")
	logText := request(http.MethodGet, "/deploy/log", "").Body.String()
	if !strings.Contains(logText, "CSQTT_INV|end") || !strings.Contains(logText, "НЕ запускалась") {
		t.Fatal("missing observed inventory completion")
	}
	for _, secret := range []string{"fixture-ssh-only", "FixtureTunnel123", "FixtureWeb123"} {
		if strings.Contains(logText, secret) {
			t.Fatal("credential leaked to API log")
		}
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "bash -s inventory") != 1 || strings.Contains(string(calls), "fixture-ssh-only") {
		t.Fatal("unexpected SSH invocation or password in argv")
	}
	t.Setenv("CSQTT_FIXTURE_MODE", "invalid")
	post(payload, true)
	waitResult("error")
	calls, err = os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "bash -s inventory") != 2 {
		t.Fatal("invalid test did not invoke exactly one helper")
	}
	// This extra contract is for the new candidate only, in a temporary
	// isolated test build. The older approved inventory helper is unchanged.
	if os.Getenv("CSQTT_DEPLOY_CANONICAL_FIXTURE") == "1" {
		t.Setenv("CSQTT_FIXTURE_MODE", "reordered")
		post(payload, true)
		waitResult("preflight")
		inv, ok := status()["inventory"].(map[string]any)
		if !ok || len(inv) != 5 || inv["server"] != "absent" || inv["udp"] != "free" || inv["tcp"] != "free" || inv["interface"] != "absent" || inv["database"] != "absent" {
			t.Fatalf("canonical report did not reach API: %v", inv)
		}
		t.Setenv("CSQTT_FIXTURE_MODE", "bad_owner")
		post(payload, true)
		waitResult("error")
		if _, ok := status()["inventory"]; ok {
			t.Fatal("untrusted owner reached inventory summary")
		}
		calls, err = os.ReadFile(filepath.Join(dir, "calls"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(calls), "bash -s inventory") != 4 {
			t.Fatal("unexpected candidate helper call count")
		}
	}
	post(`{"protocol":"CSQTT","host":"192.0.2.77","sshPort":22,"user":"root","usePassword":true,"uninstall":true}`, false)
	waitResult("error")
	after, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || string(after) != string(calls) {
		t.Fatal("uninstall invoked helper")
	}
	t.Log("actual pinned ARM64 child, authenticated API, busy guard, valid/invalid inventory, redaction and uninstall refusal PASS; SSH simulated")
}

// Full authenticated API -> bridge -> pinned ARM64 helper -> stdio SFTP.
// The caller supplies the synthetic SSH fixture. No live installer is run.
func TestDeployActualLifecycleProcessContract(t *testing.T) {
	if os.Getenv("CSQTT_DEPLOY_LIFECYCLE_PROCESS_FIXTURE") != "1" {
		t.Skip("requires isolated candidate process fixture")
	}
	if libs.DeployMode() != "install" {
		t.Fatal("pinned laboratory helper was not selected")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("LALUNE_PANEL_PASSWORD", "fixture-panel-only")
	handler := panelSecurity(panelAPIHandler(apiHandlerWithSingBox(nil, nil)))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://fixture:1061/api"+path, strings.NewReader(body))
		r.SetBasicAuth("admin", "fixture-panel-only")
		r.RemoteAddr = "192.168.4.12:1122"
		r.Header.Set("Origin", "http://fixture:1061")
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d", method, path, rec.Code)
		}
		return rec
	}
	posted := request(http.MethodPost, "/deploy", os.Getenv("CSQTT_LIFECYCLE_REQUEST"))
	var ack struct{ OK bool }
	if err := json.Unmarshal(posted.Body.Bytes(), &ack); err != nil || !ack.OK {
		t.Fatalf("operation not started: %s", posted.Body.String())
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var state map[string]any
		if err := json.Unmarshal(request(http.MethodGet, "/deploy/status", "").Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state["deploying"] == false {
			if state["result"] != os.Getenv("CSQTT_LIFECYCLE_EXPECTED_RESULT") {
				t.Fatalf("unexpected API result %v; %s", state, libs.DeployLog())
			}
			if os.Getenv("CSQTT_LIFECYCLE_EXPECT_ROLLBACK") == "1" && state["rollback"] != "restored" {
				t.Fatal("rollback not independently confirmed")
			}
			for _, secret := range []string{"fixturemain", "fixtureweb"} {
				if strings.Contains(request(http.MethodGet, "/deploy/log", "").Body.String(), secret) {
					t.Fatal("credential leaked")
				}
			}
			t.Log("authenticated API and pinned ARM64 helper lifecycle passed; remote command simulated")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("lifecycle helper did not finish")
}
