//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployCredentialsPersistForExactHost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	host := "72.56.81.131"
	v := deploySavedCredentials{MainPassword: "TestTunnel123", WebLogin: "admin2", WebPassword: "TestWeb456"}
	if err := saveDeployCredentials(host, v); err != nil {
		t.Fatal(err)
	}
	path, err := deployCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("secret file permissions: %o", st.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm()&0077 != 0 {
		t.Fatal("directory exposes secret file")
	}
	got, ok, err := getDeployCredentials(host)
	if err != nil || !ok || got != v {
		t.Fatalf("readback failed: ok=%v err=%v", ok, err)
	}
	_, ok, err = getDeployCredentials("72.56.81.132")
	if err != nil || ok {
		t.Fatal("cross-host credentials leak")
	}
	req := map[string]any{"protocol": "CSQTT", "host": host, "sshPort": 22}
	raw, _ := json.Marshal(req)
	enriched, err := enrichDeployRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(enriched, &result); err != nil {
		t.Fatal(err)
	}
	if result["mainPassword"] != v.MainPassword || result["webUser"] != v.WebLogin || result["webPassword"] != v.WebPassword {
		t.Fatal("saved secrets not injected")
	}
	req["host"] = "72.56.81.132"
	raw, _ = json.Marshal(req)
	if _, err := enrichDeployRequest(raw); err == nil {
		t.Fatal("missing host should fail closed")
	}
	req["uninstall"] = true
	raw, _ = json.Marshal(req)
	if _, err := enrichDeployRequest(raw); err != nil {
		t.Fatal(err)
	}
	if err := deleteDeployCredentials(host); err != nil {
		t.Fatal(err)
	}
	_, ok, err = getDeployCredentials(host)
	if err != nil || ok {
		t.Fatal("deleted credential still found")
	}
}
func TestDeployCredentialsRejectWeakInputAndUnsafeFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, host := range []string{"", "https://foo.example/", "../../escape", "http://foo", strings.Repeat("x", 254)} {
		if err := saveDeployCredentials(host, deploySavedCredentials{MainPassword: "Correct123", WebLogin: "admin", WebPassword: "Other123"}); err == nil {
			t.Fatalf("accepted unsafe host %q", host)
		}
	}
	if err := saveDeployCredentials("safe.example", deploySavedCredentials{MainPassword: "bad name", WebLogin: "admin", WebPassword: "Other123"}); err == nil {
		t.Fatal("accepted spaces in credentials")
	}
	path, err := deployCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := getDeployCredentials("safe.example"); err == nil {
		t.Fatal("followed symlink instead of rejecting unsafe credential store")
	}
}

func TestDeployCredentialsAPIRequiresAuthLANAndRedactsSecrets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LALUNE_PANEL_PASSWORD", "fixture-panel-secret")
	handler := panelSecurity(panelAPIHandler(apiHandlerWithSingBox(nil, nil)))
	send := func(method, path string, content []byte, auth, local, origin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://nanopi:1061/api"+path, bytes.NewReader(content))
		if auth {
			req.SetBasicAuth("admin", "fixture-panel-secret")
		}
		if local {
			req.RemoteAddr = "192.168.4.123:23456"
		} else {
			req.RemoteAddr = "203.0.113.123:23456"
		}
		if origin {
			req.Header.Set("Origin", "http://nanopi:1061")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	body := []byte("{\"host\":\"72.56.81.131\",\"mainPassword\":\"TestTunnel123\",\"webLogin\":\"admin2\",\"webPassword\":\"TestWeb456\"}")
	if got := send("PUT", "/deploy/credentials", body, false, true, true); got.Code != 401 {
		t.Fatalf("missing auth status %d", got.Code)
	}
	if got := send("PUT", "/deploy/credentials", body, true, true, false); got.Code != 403 {
		t.Fatalf("missing origin status %d", got.Code)
	}
	if got := send("PUT", "/deploy/credentials", body, true, false, true); got.Code != 403 {
		t.Fatalf("public client status %d", got.Code)
	}
	if got := send("PUT", "/deploy/credentials", body, true, true, true); got.Code != 200 {
		t.Fatalf("save status %d body %s", got.Code, got.Body.String())
	}
	got := send("GET", "/deploy/credentials?host=72.56.81.131", nil, true, true, false)
	if got.Code != 200 {
		t.Fatalf("GET status %d", got.Code)
	}
	var response map[string]any
	if err := json.Unmarshal(got.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["configured"] != true || response["webLogin"] != "admin2" {
		t.Fatalf("metadata mismatch %v", response)
	}
	if _, ok := response["mainPassword"]; ok {
		t.Fatal("GET exposes tunnel password")
	}
	if _, ok := response["webPassword"]; ok {
		t.Fatal("GET exposes WEB password")
	}
	if strings.Contains(got.Body.String(), "TestTunnel123") || strings.Contains(got.Body.String(), "TestWeb456") {
		t.Fatal("GET returned secrets")
	}
	if got := send("DELETE", "/deploy/credentials?host=72.56.81.131", nil, true, true, true); got.Code != 200 {
		t.Fatalf("DELETE %d", got.Code)
	}
	got = send("GET", "/deploy/credentials?host=72.56.81.131", nil, true, true, false)
	if got.Code != 200 || strings.Contains(got.Body.String(), "\"configured\":true") {
		t.Fatal("delete did not revoke metadata")
	}
}

func TestUpdateDoesNotLoadApplicationCredentials(t *testing.T) {
	for _, action := range []string{"update", "uninstall"} {
		data, err := enrichDeployRequest(json.RawMessage(`{"protocol":"CSQTT","host":"192.0.2.77","usePassword":false,"action":"` + action + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"mainPassword", "webUser", "webPassword"} {
			if _, ok := fields[key]; ok {
				t.Fatal("preservation mode injected new application credentials")
			}
		}
	}
}
