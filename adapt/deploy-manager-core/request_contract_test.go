package main

import (
	"strings"
	"testing"
)

func TestDecodeDeployRequestStrict(t *testing.T) {
	valid := `{"protocol":"CSQTT","host":"192.0.2.1","sshPort":22,"user":"root","usePassword":true,"password":"fixture","listenPort":0,"uninstall":false}`
	for _, raw := range []string{valid, ` {"Protocol":"CSQTT","Host":"192.0.2.1"} `} {
		r, err := decodeDeployRequest(strings.NewReader(raw))
		if err != nil || r.Protocol != "CSQTT" || r.Host != "192.0.2.1" {
			t.Fatal("valid request rejected")
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{`, valid + `{}`, valid + `x`, valid + strings.Repeat(" ", 16384),
		`{"host":"192.0.2.1","Host":"192.0.2.2"}`, `{"host":"192.0.2.1","host":"192.0.2.2"}`,
		`{"password":null}`, `{"sshPort":"22"}`, `{"host":{}}`, `{"unknown":1}`,
		`{"password":"secret-fixture","sshPort":"bad"}`,
	} {
		r, err := decodeDeployRequest(strings.NewReader(raw))
		if err == nil || r != (Request{}) || strings.Contains(err.Error(), "secret-fixture") {
			t.Fatal("invalid input accepted or leaked")
		}
	}
}

func TestCanonicalInventoryRecord(t *testing.T) {
	actual := map[string]string{"owner": "unverified", "state": "present", "name": "csqtt-core0"}
	line, err := canonicalInventoryRecord("interface", actual)
	if err != nil || line != "CSQTT_INV|interface|name=csqtt-core0|state=present|owner=unverified" {
		t.Fatal("wrong canonical record")
	}
	for _, bad := range []map[string]string{
		{"state": "present", "name": "csqtt-core0"},
		{"owner": "core_candidate", "state": "present", "name": "csqtt-core0"},
		{"owner": "unverified", "state": "present", "name": "csqtt-core0", "extra": "fixture"},
		{"owner": "unverified", "state": "present", "wrong": "csqtt-core0"},
	} {
		if _, err := canonicalInventoryRecord("interface", bad); err == nil {
			t.Fatal("invalid interface record accepted")
		}
	}
	if _, err := canonicalInventoryRecord("unknown", actual); err == nil {
		t.Fatal("unknown record accepted")
	}
}

func TestInventoryOnlyRejectsUninstallBeforeAssets(t *testing.T) {
	err := run(strings.NewReader(`{"uninstall":true}`), &strings.Builder{}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "удаление недоступно") {
		t.Fatal("uninstall must be rejected before asset validation")
	}
}
