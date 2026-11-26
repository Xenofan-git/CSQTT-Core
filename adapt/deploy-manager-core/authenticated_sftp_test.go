package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A one-shot OpenSSH server connected only by stdio; no listener, no
// network destination, no installed server payload, no existing keys.
// All authorization and host-key files live in a private test directory.
// The server accepts only SFTP, never a shell or installer.
func Test253AuthenticatedSFTPFixture(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("CSQTT_LOCAL_AUTH_SFTP_FIXTURE") != "1" {
		t.Skip("requires explicit local root-only stdio SSH fixture")
	}
	for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/ssh-keygen", "/usr/bin/sftp", "/run/sshd"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture unavailable: %s", p)
		}
	}
	d := t.TempDir()
	for _, sub := range []string{"local", "peer"} {
		if err := os.Mkdir(filepath.Join(d, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, name := range []string{"host", "client", "wrong"} {
		c := exec.CommandContext(ctx, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(d, name))
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("key fixture: %v: %s", err, out)
		}
	}
	readPub := func(name string) string {
		b, err := os.ReadFile(filepath.Join(d, name+".pub"))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(b))
		if len(fields) < 2 {
			t.Fatal("invalid fixture public key")
		}
		return strings.Join(fields[:2], " ")
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("authorized_keys", "restrict,command=\"internal-sftp\" "+readPub("client")+"\n")
	write("known_hosts", "fixture.invalid "+readPub("host")+"\n")
	write("wrong_hosts", "fixture.invalid "+readPub("wrong")+"\n")
	write("sshd_config", fmt.Sprintf("HostKey %[1]s/host\nAuthorizedKeysFile %[1]s/authorized_keys\nStrictModes no\nUsePAM no\nPermitRootLogin forced-commands-only\nPubkeyAuthentication yes\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nAuthenticationMethods publickey\nAllowUsers root\nDisableForwarding yes\nPermitTTY no\nForceCommand internal-sftp\nSubsystem sftp internal-sftp\nLogLevel ERROR\n", d))
	if out, err := exec.CommandContext(ctx, "/usr/sbin/sshd", "-t", "-f", filepath.Join(d, "sshd_config")).CombinedOutput(); err != nil {
		t.Fatalf("fixture sshd config: %v: %s", err, out)
	}
	batch := func(hostFile, keyFile string) func(context.Context, string) error {
		return func(ctx context.Context, commands string) error {
			cmd := exec.CommandContext(ctx, "/usr/bin/sftp", "-q", "-b", "-", "-F", "/dev/null",
				"-o", "ProxyCommand=/usr/sbin/sshd -i -e -f "+filepath.Join(d, "sshd_config"),
				"-o", "UserKnownHostsFile="+filepath.Join(d, hostFile),
				"-o", "GlobalKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=yes",
				"-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-i", filepath.Join(d, keyFile), "root@fixture.invalid")
			cmd.Dir = d
			cmd.Stdin = strings.NewReader(strings.ReplaceAll(commands, "peer/", filepath.Join(d, "peer")+"/") + "\nquit\n")
			cmd.WaitDelay = 500 * time.Millisecond
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("local authenticated SFTP: %w: %s", err, out)
			}
			return nil
		}
	}
	for _, tc := range []struct{ name, host, key, fault string }{
		{"wrong_host", "wrong_hosts", "client", ""},
		{"wrong_client", "known_hosts", "wrong", ""},
		{"same_size_corruption", "known_hosts", "client", "corrupt"},
		{"success", "known_hosts", "client", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &localSFTPPeer{dir: d, fault: tc.fault, batchOverride: batch(tc.host, tc.key)}
			data := bytes.Repeat([]byte("CSQTT authenticated plain text fixture\n"), 1024)
			lifecycle := &localSFTPLifecycle{memoryLifecycle: memoryLifecycle{receipt: lifecycleReceipt{0, "CSQTT_DEPLOY_OK"}}, peer: peer, data: data}
			out, err := simulateDeployLifecycle(ctx, lifecycleInstall, lifecycle)
			if (err == nil) != (tc.name == "success") {
				t.Fatalf("unexpected result: %v", err)
			}
			if _, err := os.Stat(filepath.Join(d, "peer/fixture.part")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("temporary file remained")
			}
			final, err := os.ReadFile(filepath.Join(d, "peer/fixture.txt"))
			if tc.name == "success" {
				if err != nil || !bytes.Equal(final, data) || out.result != "installed" || !peer.committed {
					t.Fatal("authenticated SFTP fixture mismatch")
				}
			} else {
				if !errors.Is(err, os.ErrNotExist) || peer.committed || strings.Contains(strings.Join(lifecycle.calls, ","), "execute") {
					t.Fatal("untrusted transfer advanced")
				}
			}
		})
	}
}
