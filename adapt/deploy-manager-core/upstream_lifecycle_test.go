package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test253LifecycleScopeAndRequests(t *testing.T) {
	r := Request{Protocol: "CSQTT", Host: "192.0.2.77", User: "root", KeyPath: "/fixture", Action: "update"}
	if err := validate(r); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"purge", "reinstall", "Update"} {
		r.Action = a
		if validate(r) == nil {
			t.Fatal("unsupported action accepted")
		}
	}
	r.Action = "update"
	r.Uninstall = true
	if validate(r) == nil {
		t.Fatal("conflicting actions accepted")
	}
	r.Uninstall = false
	t.Setenv("CSQTT_CORE_ALLOW_MUTATION", "")
	t.Setenv("CSQTT_CORE_DEPLOY_LAB_TARGET", "")
	t.Setenv("CSQTT_CORE_DEPLOY_LAB_MACHINE_ID", "")
	if _, err := requireLifecycleScope(r); err == nil {
		t.Fatal("missing scope allowed")
	}
	t.Setenv("CSQTT_CORE_ALLOW_MUTATION", "isolated-lab-only")
	t.Setenv("CSQTT_CORE_DEPLOY_LAB_TARGET", "192.0.2.77:22")
	t.Setenv("CSQTT_CORE_DEPLOY_LAB_MACHINE_ID", strings.Repeat("a", 32))
	if _, err := requireLifecycleScope(r); err != nil {
		t.Fatal(err)
	}
	r.SSHPort = 2222
	if _, err := requireLifecycleScope(r); err == nil {
		t.Fatal("different endpoint allowed")
	}
}

func Test253LifecycleRejectsBeforeSSH(t *testing.T) {
	t.Setenv("CSQTT_CORE_ALLOW_MUTATION", "")
	r := Request{Protocol: "CSQTT", Host: "192.0.2.77", User: "root", KeyPath: "/fixture", Action: "update"}
	data, _ := json.Marshal(r)
	var out bytes.Buffer
	if err := runRemoteLifecycle(context.Background(), bytes.NewReader(data), &out, t.TempDir()); err != ErrIsolationRequired {
		t.Fatalf("gate: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("claimed progress before scope verification")
	}
}

func Test253LifecyclePostPreservation(t *testing.T) {
	before := serverEvidence{Binary: "old", Unit: "old-unit", Environment: "env", Database: "db", Config: "settings"}
	after := before
	after.Binary = expectedServerSHA
	after.Unit = "new-unit"
	inv := "CSQTT_INV|server|place=core|runtime=systemd|state=running|owner=core_candidate\nCSQTT_INV|port|proto=udp|port=47000|state=busy|owner=core_candidate\nCSQTT_INV|port|proto=tcp|port=47002|state=busy|owner=core_candidate\n"
	if err := checkLifecyclePost(lifecycleUpdate, "installed", inv, inv, before, after); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"database", "config", "environment", "binary"} {
		changed := after
		switch field {
		case "database":
			changed.Database = "lost"
		case "config":
			changed.Config = "lost"
		case "environment":
			changed.Environment = "changed"
		case "binary":
			changed.Binary = "wrong"
		}
		if checkLifecyclePost(lifecycleUpdate, "installed", inv, inv, before, changed) == nil {
			t.Fatalf("accepted changed %s", field)
		}
	}
	if checkLifecyclePost(lifecycleUpdate, "restored", inv, inv, before, after) == nil {
		t.Fatal("candidate mistaken for restored runtime")
	}
	if err := checkLifecyclePost(lifecycleUpdate, "restored", inv, inv, before, before); err != nil {
		t.Fatal(err)
	}
	removed := before
	removed.Binary = ""
	removed.Unit = ""
	absent := "CSQTT_INV|server|place=core|runtime=systemd|state=absent|owner=none\nCSQTT_INV|interface|name=csqtt-core0|state=absent|owner=unverified\nCSQTT_INV|port|proto=udp|port=47000|state=free|owner=none\nCSQTT_INV|port|proto=tcp|port=47002|state=free|owner=none\n"
	if err := checkLifecyclePost(lifecycleRemove, "uninstalled", inv, absent, before, removed); err != nil {
		t.Fatal(err)
	}
	if checkLifecycleState(lifecycleInstall, absent, removed) == nil {
		t.Fatal("fresh install would overwrite retained settings")
	}
}

func Test253InstallerExecStreamingBoundary(t *testing.T) {
	dir := t.TempDir()
	// Synthetic ssh consumes the actual generated command without executing it.
	script := "#!/bin/sh\ncat > \"$CSQTT_TEST_COMMAND\"\nprintf 'CSQTT_PROGRESS|0.60|secret-remote-value\\nCSQTT_DEPLOY_OK\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "command")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("CSQTT_TEST_COMMAND", target)
	t.Setenv("CSQTT_CORE_ALLOW_MUTATION", "isolated-lab-only")
	t.Setenv("CSQTT_CORE_DEPLOY_LAB_TARGET", "192.0.2.77:22")
	t.Setenv("CSQTT_CORE_DEPLOY_LAB_MACHINE_ID", strings.Repeat("a", 32))
	r := Request{Protocol: "CSQTT", Host: "192.0.2.77", User: "root", KeyPath: "/fixture", Action: "update"}
	tr, err := newRemoteTransport(r)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.close()
	var out bytes.Buffer
	result, err := tr.execInstallerResult(context.Background(), "/tmp/csqtt-deploy-"+strings.Repeat("b", 32), lifecycleUpdate, &out)
	if err != nil || result.exitStatus != 0 {
		t.Fatalf("exec: %v %+v", err, result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "bash ./deploy-csqtt-core-lifecycle.sh update") || !strings.Contains(string(data), lifecycleInstallerSHA) || !strings.Contains(string(data), expectedServerSHA) {
		t.Fatal("missing action/digest check")
	}
	if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "CSQTT_DEPLOY_OK") || !strings.Contains(out.String(), "CSQTT_PROGRESS|") {
		t.Fatal("raw transcript exposed or premature success")
	}
	if _, err = tr.execInstallerResult(context.Background(), "/tmp/a;bad", lifecycleUpdate, &out); err == nil {
		t.Fatal("unsafe path allowed")
	}
}
