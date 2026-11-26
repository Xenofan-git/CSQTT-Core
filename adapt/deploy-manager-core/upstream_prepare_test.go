package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test253UpstreamMachineSelection(t *testing.T) {
	for _, tc := range []struct {
		out string
		ok  bool
	}{
		{"aarch64\n", true}, {" ARM64\r\n", true},
		{"x86_64\n", false}, {"amd64", false}, {"armv7l", false},
		{"", false}, {"riscv64", false}, {"aarch64\nx86_64", false},
	} {
		if (requireARM64Machine(tc.out) == nil) != tc.ok {
			t.Fatalf("unexpected result for %q", tc.out)
		}
	}
}

// Synthetic ssh executable: only the fixed read-only command is accepted.
// No SSH connection and no server payload are used.
func Test253UpstreamRemoteArchitecture(t *testing.T) {
	d := t.TempDir()
	script := "#!/bin/sh\nfor arg do last=$arg; done\n[ \"$last\" = 'uname -m' ] || exit 98\nprintf '%s\\n' \"$CSQTT_ARCH_FIXTURE\"\nexit \"$CSQTT_ARCH_EXIT\"\n"
	if err := os.WriteFile(filepath.Join(d, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+":"+os.Getenv("PATH"))
	for _, tc := range []struct {
		machine, exit string
		ok            bool
	}{
		{"aarch64", "0", true}, {"x86_64", "0", false}, {"aarch64", "1", false}, {"unknown", "0", false},
	} {
		transport := &remoteTransport{request: Request{Host: "192.0.2.1", User: "root", KeyPath: "/fixture"}, env: append(os.Environ(), "CSQTT_ARCH_FIXTURE="+tc.machine, "CSQTT_ARCH_EXIT="+tc.exit)}
		if (transport.requireRemoteARM64(context.Background()) == nil) != tc.ok {
			t.Fatalf("unexpected fixture result for %s/%s", tc.machine, tc.exit)
		}
	}
}

func Test253UpstreamEnvironmentEncoding(t *testing.T) {
	for _, tc := range []struct{ in, out string }{
		{"plain", "\"plain\""},
		{"a b", "\"a b\""},
		{"a\\b\"c", "\"a\\\\b\\\"c\""},
		{"a\r\nb", "\"a  b\""},
		{"тест", "\"тест\""},
	} {
		if systemdEnvironmentValue253(tc.in) != tc.out {
			t.Fatal("upstream environment encoding mismatch")
		}
	}
}

func Test253UpstreamDeployConfig(t *testing.T) {
	r := Request{Host: "192.0.2.1", WebUser: "FixtureUser", WebPassword: "FixtureWeb", MainPassword: "FixtureTunnel"}
	install, err := prepareUpstreamDeployConfig(lifecycleInstall, r, nil, nil, false)
	if err != nil || string(install.webEnvironment) != "CSQTT_WEB_USER=\"FixtureUser\"\nCSQTT_WEB_PASS=\"FixtureWeb\"\nCSQTT_PUBLIC_IP=\"192.0.2.1\"\n" || string(install.deployOverrides) != "{\"main_password\":\"FixtureTunnel\"}" {
		t.Fatal("original install configuration format differs")
	}
	existing := []byte("CSQTT_WEB_USER=old\nCSQTT_WEB_PASS=oldpass\n")
	overrides := []byte("{\"main_password\":\"oldpass\"}")
	updated, err := prepareUpstreamDeployConfig(lifecycleUpdate, r, existing, overrides, true)
	if err != nil || !bytes.Equal(updated.webEnvironment, existing) || !bytes.Equal(updated.deployOverrides, overrides) || strings.Contains(string(updated.webEnvironment), "Fixture") {
		t.Fatal("update replaced existing configuration")
	}
	consumed, err := prepareUpstreamDeployConfig(lifecycleUpdate, r, existing, nil, true)
	if err != nil || string(consumed.deployOverrides) != "{\"main_password\":\"\"}\n" {
		t.Fatal("consumed overrides not preserved")
	}
	if _, err = prepareUpstreamDeployConfig(lifecycleUpdate, r, existing, nil, false); err == nil {
		t.Fatal("unverified database accepted")
	}
	removed, err := prepareUpstreamDeployConfig(lifecycleRemove, r, nil, nil, false)
	if err != nil || len(removed.webEnvironment) != 0 || len(removed.deployOverrides) != 0 {
		t.Fatal("uninstall must not prepare credentials")
	}
	for _, bad := range []Request{{}, {Host: "192.0.2.1", WebUser: "a", WebPassword: "b", MainPassword: "c", DockerInstall: true}, {Host: "bad", WebUser: "a", WebPassword: "b", MainPassword: "c"}} {
		if _, err = prepareUpstreamDeployConfig(lifecycleInstall, bad, nil, nil, false); err == nil {
			t.Fatal("invalid install config accepted")
		}
	}
	if _, err = prepareUpstreamDeployConfig("reinstall", r, nil, nil, false); err == nil {
		t.Fatal("unreviewed destructive reinstall accepted")
	}
}
