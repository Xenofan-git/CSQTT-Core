package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// All paths below are relative to a private, local t.TempDir.
// OpenSSH -D starts the SFTP subsystem directly, not via SSH or TCP.
type localSFTPPeer struct {
	dir       string
	sftp      string
	server    string
	fault     string
	committed bool
}

func (p *localSFTPPeer) batch(ctx context.Context, commands string) error {
	cmd := exec.CommandContext(ctx, p.sftp, "-q", "-b", "-", "-D", p.server)
	cmd.Dir = p.dir
	cmd.Stdin = strings.NewReader(commands + "\nquit\n")
	cmd.WaitDelay = 500 * time.Millisecond
	if output, err := cmd.CombinedOutput(); err != nil {
		return errors.New("local SFTP batch failed: " + string(output))
	}
	return nil
}
func (p *localSFTPPeer) putTemporary(ctx context.Context, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if p.fault == "truncate" {
		data = data[:len(data)-1]
	}
	if p.fault == "corrupt" {
		data[0] ^= 1
	}
	if p.fault == "oversize" {
		data = append(data, 0)
	}
	if err = os.WriteFile(filepath.Join(p.dir, "local/input.txt"), data, 0600); err != nil {
		return err
	}
	if err = p.batch(ctx, "put local/input.txt peer/fixture.part"); err != nil {
		return err
	}
	if p.fault == "put_error" {
		return errors.New("fixture connection interrupted")
	}
	return nil
}
func (p *localSFTPPeer) readTemporary(ctx context.Context) (io.ReadCloser, error) {
	if p.fault == "read_error" {
		return nil, errors.New("fixture read interrupted")
	}
	if err := p.batch(ctx, "get peer/fixture.part local/readback.txt"); err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(p.dir, "local/readback.txt"))
}
func (p *localSFTPPeer) commitTemporary(ctx context.Context) error {
	if p.fault == "commit_error" {
		return errors.New("fixture commit rejected")
	}
	if err := p.batch(ctx, "rename peer/fixture.part peer/fixture.txt"); err != nil {
		return err
	}
	p.committed = true
	return nil
}
func (p *localSFTPPeer) discardTemporary(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(p.dir, "peer/fixture.part")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return p.batch(ctx, "rm peer/fixture.part")
}

// Only the staging step uses the real local SFTP protocol; the other
// lifecycle callbacks remain in-memory and cannot execute an installer.
type localSFTPLifecycle struct {
	memoryLifecycle
	peer *localSFTPPeer
	data []byte
}

func (s *localSFTPLifecycle) stage(ctx context.Context, action lifecycleAction) error {
	if err := s.memoryLifecycle.stage(ctx, action); err != nil {
		return err
	}
	return verifyStagedFixture(ctx, s.peer, s.data)
}

func Test253LocalSFTPStaging(t *testing.T) {
	sftp, err := exec.LookPath("sftp")
	if err != nil {
		t.Skip("OpenSSH sftp unavailable")
	}
	server := "/usr/lib/openssh/sftp-server"
	if _, err := os.Stat(server); err != nil {
		t.Skip("local OpenSSH SFTP subsystem unavailable")
	}
	data := bytes.Repeat([]byte("CSQTT plain text transfer fixture\n"), 8192)
	for _, fault := range []string{"", "truncate", "corrupt", "oversize", "put_error", "read_error", "commit_error", "cancel"} {
		name := fault
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			for _, sub := range []string{"local", "peer"} {
				if err := os.Mkdir(filepath.Join(d, sub), 0700); err != nil {
					t.Fatal(err)
				}
			}
			p := &localSFTPPeer{dir: d, sftp: sftp, server: server, fault: fault}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if fault == "cancel" {
				cancel()
			}
			lifecycle := &localSFTPLifecycle{
				memoryLifecycle: memoryLifecycle{receipt: lifecycleReceipt{0, "CSQTT_DEPLOY_OK"}},
				peer:            p, data: data,
			}
			out, err := simulateDeployLifecycle(ctx, lifecycleInstall, lifecycle)
			if (err == nil) != (fault == "") {
				t.Fatalf("unexpected result: %v", err)
			}
			if fault == "" {
				if out.result != "installed" || strings.Join(lifecycle.calls, ",") != "check,stage,execute,postcheck_installed" {
					t.Fatal("successful local staging did not advance fixture lifecycle")
				}
			} else {
				if out.result != "unknown" || strings.Contains(strings.Join(lifecycle.calls, ","), "execute") {
					t.Fatal("failed staging advanced to execution")
				}
			}
			if _, err := os.Stat(filepath.Join(d, "peer/fixture.part")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("temporary file remained")
			}
			final, err := os.ReadFile(filepath.Join(d, "peer/fixture.txt"))
			if fault == "" {
				if err != nil || !p.committed || !bytes.Equal(final, data) {
					t.Fatal("committed payload differs")
				}
			} else if !errors.Is(err, os.ErrNotExist) || p.committed {
				t.Fatal("unverified payload published")
			}
		})
	}
}

// A synthetic peer verifies cleanup failure propagation without network I/O.
type cleanupFailurePeer struct{}

func (cleanupFailurePeer) putTemporary(context.Context, io.Reader) error { return errors.New("put") }
func (cleanupFailurePeer) readTemporary(context.Context) (io.ReadCloser, error) {
	return nil, errors.New("not reached")
}
func (cleanupFailurePeer) commitTemporary(context.Context) error  { return errors.New("not reached") }
func (cleanupFailurePeer) discardTemporary(context.Context) error { return errors.New("cleanup") }
func Test253StagingCleanupFailure(t *testing.T) {
	err := verifyStagedFixture(context.Background(), cleanupFailurePeer{}, []byte("fixture"))
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatal("cleanup failure not reported")
	}
}
