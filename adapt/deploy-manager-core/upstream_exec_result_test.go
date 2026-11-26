package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A synthetic local executable accepts only the existing read-only calls.
func Test253ReadOnlyExecResult(t *testing.T) {
	dir := t.TempDir()
	fixture := `#!/bin/sh
for arg do last=$arg; done
case "$last" in
 "uname -m"|"bash -s inventory") ;;
 *) exit 98 ;;
esac
printf '%s' "$FIXTURE_OUT"
printf '%s' "$FIXTURE_ERR" >&2
if [ "$FIXTURE_BIG" = yes ]; then /usr/bin/head -c 70000 /dev/zero; fi
if [ "$FIXTURE_HANG" = yes ]; then /bin/sleep 5; fi
exit "$FIXTURE_EXIT"
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, status, stdout, stderr, big, hang string
		known                                   bool
		code                                    int
	}{
		{"success", "0", "aarch64\n", "warning\n", "", "", true, 0},
		{"remote error", "20", "aarch64\n", "fixture diagnostic", "", "", true, 20},
		{"ssh failure", "255", "aarch64\n", "fixture diagnostic", "", "", false, -1},
		{"overflow", "0", "", "", "yes", "", false, -1},
		{"timeout", "0", "aarch64\n", "", "", "yes", false, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &remoteTransport{request: Request{Host: "192.0.2.1", User: "root", KeyPath: "/fixture"}, env: append(os.Environ(),
				"FIXTURE_OUT="+tc.stdout, "FIXTURE_ERR="+tc.stderr, "FIXTURE_EXIT="+tc.status,
				"FIXTURE_BIG="+tc.big, "FIXTURE_HANG="+tc.hang)}
			limit := 5 * time.Second
			if tc.hang == "yes" {
				limit = 150 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			result, err := tr.execReadOnlyResult(ctx, readOnlyMachine, nil)
			if (err == nil) != tc.known || result.exitStatus != tc.code {
				t.Fatalf("unexpected result status=%d err=%v", result.exitStatus, err)
			}
			if tc.known {
				if result.stdout != tc.stdout || result.stderr != tc.stderr {
					t.Fatal("streams were mixed or lost")
				}
			} else if result.stdout != "" || result.stderr != "" {
				t.Fatal("unconfirmed output returned")
			}
		})
	}
	tr := &remoteTransport{}
	if _, err := tr.execReadOnlyResult(context.Background(), 0, nil); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if _, err := tr.execReadOnlyResult(context.Background(), readOnlyMachine, []byte("fixture")); err == nil {
		t.Fatal("unexpected input accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.execReadOnlyResult(ctx, readOnlyInventory, nil); err == nil {
		t.Fatal("canceled operation accepted")
	}
}

func Test253ReadOnlyExecInventoryStreams(t *testing.T) {
	dir := t.TempDir()
	fixture := `#!/bin/sh
for arg do last=$arg; done
[ "$last" = 'bash -s inventory' ] || exit 98
printf '%s\n' 'CSQTT_INV|end' >&2
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	tr := &remoteTransport{request: Request{Host: "192.0.2.1", User: "root", KeyPath: "/fixture"}, env: os.Environ()}
	var output strings.Builder
	if err := tr.inventory(context.Background(), []byte("fixture only"), &output); err == nil || output.Len() != 0 {
		t.Fatal("stderr was accepted as inventory")
	}
}
