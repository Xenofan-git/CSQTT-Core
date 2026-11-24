//go:build linux

package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploySSHPasswordSavedPerHostAndUsedInInstallAndUninstall(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	host := "72.56.81.131"
	if err := saveDeploySSHPassword(host, "example-SSH-123"); err != nil {
		t.Fatal(err)
	}
	path, err := deploySSHPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("SSH file permissions")
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm()&0077 != 0 {
		t.Fatal("SSH directory permissions")
	}
	if value, ok, err := getDeploySSHPassword(host); err != nil || !ok || value != "example-SSH-123" {
		t.Fatal("readback failed")
	}
	if _, ok, err := getDeploySSHPassword("72.56.81.132"); err != nil || ok {
		t.Fatal("cross host credential leak")
	}
	if err := saveDeployCredentials(host, deploySavedCredentials{MainPassword: "tunnel123", WebLogin: "admin", WebPassword: "web123"}); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"protocol": "CSQTT", "host": host, "usePassword": true, "password": ""}
	raw, _ := json.Marshal(request)
	result, err := enrichDeployRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	var contents map[string]any
	if err := json.Unmarshal(result, &contents); err != nil {
		t.Fatal(err)
	}
	if contents["password"] != "example-SSH-123" || contents["mainPassword"] != "tunnel123" {
		t.Fatal("install injection mismatch")
	}
	request["uninstall"] = true
	raw, _ = json.Marshal(request)
	result, err = enrichDeployRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	contents = map[string]any{}
	_ = json.Unmarshal(result, &contents)
	if contents["password"] != "example-SSH-123" {
		t.Fatal("uninstall SSH password missing")
	}
	if _, ok := contents["mainPassword"]; ok {
		t.Fatal("uninstall should not inject server auth")
	}
	request["host"] = "72.56.81.132"
	raw, _ = json.Marshal(request)
	if _, err = enrichDeployRequest(raw); err == nil {
		t.Fatal("other host unexpectedly authorized")
	}
	request["usePassword"] = false
	raw, _ = json.Marshal(request)
	if _, err = enrichDeployRequest(raw); err != nil {
		t.Fatal("key mode requires no SSH password")
	}
	if err := deleteDeploySSHPassword(host); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := getDeploySSHPassword(host); err != nil || ok {
		t.Fatal("delete not persisted")
	}
	if _, ok, err := getDeployCredentials(host); err != nil || !ok {
		t.Fatal("SSH deletion removed server auth")
	}
}

func TestDeploySSHPasswordRejectsInvalidDataAndSymlinks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, host := range []string{"", "../path", "https://server.example/path"} {
		if err := saveDeploySSHPassword(host, "Example123"); err == nil {
			t.Fatalf("unsafe host accepted")
		}
	}
	for _, pass := range []string{"", strings.Repeat("x", 1025), "line\nbreak"} {
		if err := saveDeploySSHPassword("server.example", pass); err == nil {
			t.Fatal("unsafe password accepted")
		}
	}
	path, err := deploySSHPath()
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := getDeploySSHPassword("server.example"); err == nil {
		t.Fatal("symlink followed")
	}
}

func TestDeploySSHPasswordAPIAuthOriginAndNoSecretReadback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LALUNE_PANEL_PASSWORD", "example-test-panel")
	handler := panelSecurity(panelAPIHandler(apiHandlerWithSingBox(nil, nil)))
	perform := func(method, path, body string, auth, lan, origin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://nanopi:1061/api"+path, strings.NewReader(body))
		if auth {
			req.SetBasicAuth("admin", "example-test-panel")
		}
		if lan {
			req.RemoteAddr = "192.168.4.12:1122"
		} else {
			req.RemoteAddr = "203.0.113.88:1122"
		}
		if origin {
			req.Header.Set("Origin", "http://nanopi:1061")
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		return result
	}
	payload := `{"host":"72.56.81.131","password":"example-test-ssh"}`
	for _, test := range []struct {
		auth, lan, origin bool
		code              int
	}{
		{false, true, true, 401}, {true, false, true, 403}, {true, true, false, 403}, {true, true, true, 200},
	} {
		got := perform("PUT", "/deploy/ssh-credentials", payload, test.auth, test.lan, test.origin)
		if got.Code != test.code {
			t.Fatalf("PUT got %d expected %d", got.Code, test.code)
		}
	}
	result := perform("GET", "/deploy/ssh-credentials?host=72.56.81.131", "", true, true, false)
	if result.Code != 200 {
		t.Fatal(result.Code)
	}
	var metadata map[string]any
	if err := json.Unmarshal(result.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata["configured"] != true {
		t.Fatal("readback exposes fields beyond configured")
	}
	if strings.Contains(result.Body.String(), "example-test-ssh") {
		t.Fatal("secret exposed in GET")
	}
	result = perform("DELETE", "/deploy/ssh-credentials?host=72.56.81.131", "", true, true, true)
	if result.Code != 200 {
		t.Fatal(result.Code)
	}
	result = perform("GET", "/deploy/ssh-credentials?host=72.56.81.131", "", true, true, false)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "false") {
		t.Fatal("password deletion failed")
	}
}
