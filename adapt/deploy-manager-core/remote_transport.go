package main

// OpenSSH owns authentication and host-key verification. This small SFTP v3
// client uses its subsystem channel, with bounded packets and no shell fallback.
import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type remoteTransport struct {
	request Request
	local   string
	env     []string
}

func newRemoteTransport(r Request) (*remoteTransport, error) {
	if err := validate(r); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "csqtt-transport-")
	if err != nil {
		return nil, errors.New("cannot prepare SSH transport")
	}
	t := &remoteTransport{request: r, local: dir}
	// Do not inherit a caller's askpass/password. Passwords never enter argv.
	for _, value := range os.Environ() {
		name := strings.SplitN(value, "=", 2)[0]
		if name != "SSH_ASKPASS" && name != "SSH_ASKPASS_REQUIRE" && name != "CSQTT_DEPLOY_SSH_PASSWORD" {
			t.env = append(t.env, value)
		}
	}
	if r.UsePassword {
		path := filepath.Join(dir, "askpass")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' \"$CSQTT_DEPLOY_SSH_PASSWORD\"\n"), 0700); err != nil {
			t.close()
			return nil, errors.New("cannot prepare SSH authentication")
		}
		t.env = append(t.env, "SSH_ASKPASS="+path, "SSH_ASKPASS_REQUIRE=force", "CSQTT_DEPLOY_SSH_PASSWORD="+r.Password)
	}
	return t, nil
}

func (t *remoteTransport) close() error { return os.RemoveAll(t.local) }

func (t *remoteTransport) command(ctx context.Context, subsystem bool, command string) *exec.Cmd {
	port := t.request.SSHPort
	if port == 0 {
		port = 22
	}
	args := []string{"-F", "/dev/null", "-T", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=12", "-o", "ConnectionAttempts=1", "-o", "NumberOfPasswordPrompts=1", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=2", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-p", strconv.Itoa(port)}
	if t.request.UsePassword {
		args = append(args, "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "PubkeyAuthentication=no")
	} else {
		args = append(args, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-i", t.request.KeyPath)
	}
	if subsystem {
		args = append(args, "-s")
	}
	args = append(args, t.request.User+"@"+t.request.Host, command)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Env = t.env
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd
}

// CSQTTR 2.5.3 performDeploy checks uname -m before asset selection.
// The Core distribution has only a pinned ARM64 server asset.
func (t *remoteTransport) requireRemoteARM64(ctx context.Context) error {
	result, err := t.execReadOnlyResult(ctx, readOnlyMachine, nil)
	if err != nil || result.exitStatus != 0 {
		return errors.New("не удалось проверить архитектуру VPS по SSH")
	}
	return requireARM64Machine(result.stdout)
}

// Inventory uses the same strict endpoint/authentication settings as staging.
func (t *remoteTransport) inventory(ctx context.Context, script []byte, writer io.Writer) error {
	result, err := t.execReadOnlyResult(ctx, readOnlyInventory, script)
	if err != nil || result.exitStatus != 0 {
		return errors.New("SSH inventory failed (remote output hidden)")
	}
	records, err := validatedInventory(result.stdout)
	if err != nil {
		return err
	}
	for _, line := range records {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}

const sftpMaxPacket = 128 * 1024
const sftpChunk = 32 * 1024

func wireUint(v uint32) []byte   { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func wireString(v []byte) []byte { return append(wireUint(uint32(len(v))), v...) }
func wireText(v string) []byte   { return wireString([]byte(v)) }
func readWireString(b []byte) ([]byte, []byte, error) {
	if len(b) < 4 {
		return nil, nil, errors.New("short SFTP string")
	}
	n := uint64(binary.BigEndian.Uint32(b))
	if n > uint64(len(b)-4) {
		return nil, nil, errors.New("invalid SFTP string size")
	}
	return b[4 : 4+int(n)], b[4+int(n):], nil
}

type sftpStatus uint32

func (s sftpStatus) Error() string { return "SFTP operation refused" }

type remoteSFTP struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	out      io.ReadCloser
	id       uint32
	mu       sync.Mutex
	cancel   context.CancelFunc
	finished chan error
	dir      string
	files    []string
}

func (t *remoteTransport) openSFTP(ctx context.Context) (*remoteSFTP, error) {
	child, cancel := context.WithCancel(ctx)
	cmd := t.command(child, true, "sftp")
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard // Never expose remote banners or credential-bearing diagnostics.
	if err := cmd.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, errors.New("cannot start SSH SFTP")
	}
	s := &remoteSFTP{cmd: cmd, in: in, out: out, cancel: cancel, finished: make(chan error, 1)}
	go func() { s.finished <- cmd.Wait() }()
	if err := s.send(append([]byte{1}, wireUint(3)...)); err != nil {
		s.close()
		return nil, err
	}
	packet, err := s.receive()
	if err != nil || len(packet) < 5 || packet[0] != 2 || binary.BigEndian.Uint32(packet[1:5]) != 3 {
		s.close()
		return nil, errors.New("SSH SFTP v3 handshake failed")
	}
	return s, nil
}

func (s *remoteSFTP) close() {
	s.in.Close()
	s.cancel()
	s.out.Close()
	<-s.finished
}

func (s *remoteSFTP) send(body []byte) error {
	if len(body) == 0 || len(body) > sftpMaxPacket {
		return errors.New("invalid SFTP packet")
	}
	packet := append(wireUint(uint32(len(body))), body...)
	for len(packet) > 0 {
		n, err := s.in.Write(packet)
		if err != nil {
			return errors.New("SFTP connection lost")
		}
		if n == 0 {
			return io.ErrNoProgress
		}
		packet = packet[n:]
	}
	return nil
}
func (s *remoteSFTP) receive() ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(s.out, header); err != nil {
		return nil, errors.New("SFTP connection lost")
	}
	n := binary.BigEndian.Uint32(header)
	if n < 1 || n > sftpMaxPacket {
		return nil, errors.New("invalid SFTP packet size")
	}
	packet := make([]byte, int(n))
	if _, err := io.ReadFull(s.out, packet); err != nil {
		return nil, errors.New("truncated SFTP packet")
	}
	return packet, nil
}

// A canceled request closes the channel: an abandoned reply must never be
// mistaken for the next operation. Mutating packets are never retried.
func (s *remoteSFTP) request(ctx context.Context, kind, expected byte, payload []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()
	s.id++
	body := append([]byte{kind}, wireUint(s.id)...)
	if err := s.send(append(body, payload...)); err != nil {
		s.cancel()
		return nil, err
	}
	reply, err := s.receive()
	if err != nil {
		s.cancel()
		return nil, err
	}
	if len(reply) < 5 || binary.BigEndian.Uint32(reply[1:5]) != s.id {
		s.cancel()
		return nil, errors.New("SFTP response ID mismatch")
	}
	if reply[0] == 101 {
		if len(reply) < 9 {
			s.cancel()
			return nil, errors.New("short SFTP status")
		}
		_, rest, parseErr := readWireString(reply[9:])
		if parseErr == nil {
			_, rest, parseErr = readWireString(rest)
		}
		if parseErr != nil || len(rest) != 0 {
			s.cancel()
			return nil, errors.New("malformed SFTP status")
		}
		code := binary.BigEndian.Uint32(reply[5:9])
		if code != 0 {
			return nil, sftpStatus(code)
		}
		if expected != 101 {
			return nil, errors.New("unexpected SFTP success status")
		}
		return nil, nil
	}
	if reply[0] != expected {
		s.cancel()
		return nil, errors.New("unexpected SFTP response")
	}
	return reply[5:], nil
}

func (s *remoteSFTP) workspace(ctx context.Context) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	dir := fmt.Sprintf("/tmp/csqtt-deploy-%x", token)
	// ATTR_PERMISSIONS=4, 0700; MKDIR is exclusive, no existing path reused.
	payload := append(wireText(dir), append(wireUint(4), wireUint(0700)...)...)
	if _, err := s.request(ctx, 14, 101, payload); err != nil {
		return errors.New("remote staging directory creation failed or outcome unknown: " + dir)
	}
	s.dir = dir
	return nil
}

func (s *remoteSFTP) open(ctx context.Context, path string, flags uint32) ([]byte, error) {
	payload := append(wireText(path), wireUint(flags)...)
	payload = append(payload, append(wireUint(4), wireUint(0600)...)...)
	reply, err := s.request(ctx, 3, 102, payload)
	if err != nil {
		return nil, err
	}
	handle, rest, err := readWireString(reply)
	if err != nil || len(rest) != 0 || len(handle) == 0 || len(handle) > 256 {
		return nil, errors.New("invalid SFTP handle")
	}
	return handle, nil
}
func (s *remoteSFTP) closeHandle(ctx context.Context, h []byte) error {
	_, err := s.request(ctx, 4, 101, wireString(h))
	return err
}
func offsetPayload(h []byte, offset uint64) []byte {
	p := wireString(h)
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, offset)
	return append(p, b...)
}

type remoteStagingPeer struct {
	s                *remoteSFTP
	temporary, final string
}

func (s *remoteSFTP) peer(name string) (*remoteStagingPeer, error) {
	if s.dir == "" || (name != "deploy-csqtt-core-250.sh" && name != "csqtt-core8-linux-arm64") {
		return nil, errors.New("invalid remote asset")
	}
	p := &remoteStagingPeer{s: s, temporary: s.dir + "/" + name + ".part", final: s.dir + "/" + name}
	s.files = append(s.files, p.temporary, p.final)
	return p, nil
}
func (p *remoteStagingPeer) putTemporary(ctx context.Context, r io.Reader) error {
	// WRITE | CREAT | EXCL. Never overwrite an existing remote file.
	h, err := p.s.open(ctx, p.temporary, 2|8|32)
	if err != nil {
		return err
	}
	var offset uint64
	buf := make([]byte, sftpChunk)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			payload := append(offsetPayload(h, offset), wireString(buf[:n])...)
			if _, err = p.s.request(ctx, 6, 101, payload); err != nil {
				return err
			}
			offset += uint64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return p.s.closeHandle(ctx, h)
}

type remoteFileReader struct {
	s      *remoteSFTP
	ctx    context.Context
	handle []byte
	offset uint64
}

func (r *remoteFileReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := len(b)
	if n > sftpChunk {
		n = sftpChunk
	}
	reply, err := r.s.request(r.ctx, 5, 103, append(offsetPayload(r.handle, r.offset), wireUint(uint32(n))...))
	if err != nil {
		if errors.Is(err, sftpStatus(1)) {
			return 0, io.EOF
		}
		return 0, err
	}
	data, rest, err := readWireString(reply)
	if err != nil || len(rest) != 0 || len(data) == 0 || len(data) > n {
		return 0, errors.New("invalid SFTP read data")
	}
	copy(b, data)
	r.offset += uint64(len(data))
	return len(data), nil
}
func (r *remoteFileReader) Close() error { return r.s.closeHandle(r.ctx, r.handle) }
func (p *remoteStagingPeer) readTemporary(ctx context.Context) (io.ReadCloser, error) {
	h, err := p.s.open(ctx, p.temporary, 1)
	if err != nil {
		return nil, err
	}
	return &remoteFileReader{s: p.s, ctx: ctx, handle: h}, nil
}
func (p *remoteStagingPeer) commitTemporary(ctx context.Context) error {
	_, err := p.s.request(ctx, 18, 101, append(wireText(p.temporary), wireText(p.final)...))
	return err
}
func (p *remoteStagingPeer) discardTemporary(ctx context.Context) error {
	return p.s.remove(ctx, p.temporary)
}
func (s *remoteSFTP) remove(ctx context.Context, path string) error {
	_, err := s.request(ctx, 13, 101, wireText(path))
	if errors.Is(err, sftpStatus(2)) {
		return nil
	}
	return err
}

// Cleanup never recurses, follows no directories, and only names this session's
// generated files. A lost connection is reported; cleanup is not silently claimed.
func (s *remoteSFTP) cleanup(ctx context.Context) error {
	if s.dir == "" {
		return nil
	}
	var errs []error
	for _, path := range s.files {
		if err := s.remove(ctx, path); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := s.request(ctx, 15, 101, wireText(s.dir)); err != nil && !errors.Is(err, sftpStatus(2)) {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.New("remote staging cleanup incomplete: " + s.dir)
	}
	s.dir = ""
	return nil
}

// Called by the manager's --stage-only entry point. Bytes are already pinned
// and ELF-checked before the connection is opened. No installer is executed.
func stageRemoteAssets(ctx context.Context, t *remoteTransport, installer, server []byte) (err error) {
	s, err := t.openSFTP(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	if err = s.workspace(ctx); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), stagingCleanupTimeout)
		defer cancel()
		err = errors.Join(err, cleanupRemoteStage(cleanup, t, s))
	}()
	for _, asset := range []struct {
		name string
		data []byte
	}{{"deploy-csqtt-core-250.sh", installer}, {"csqtt-core8-linux-arm64", server}} {
		p, peerErr := s.peer(asset.name)
		if peerErr != nil {
			return peerErr
		}
		if err = verifyStagedFixture(ctx, p, asset.data); err != nil {
			return err
		}
	}
	return nil
}

// Only cleanup may reconnect. Transfers and renames are never repeated after
// connection loss. The fresh channel removes the already known private paths.
func cleanupRemoteStage(ctx context.Context, t *remoteTransport, s *remoteSFTP) error {
	if err := s.cleanup(ctx); err == nil {
		return nil
	}
	fresh, err := t.openSFTP(ctx)
	if err != nil {
		return errors.New("remote staging cleanup incomplete: " + s.dir)
	}
	defer fresh.close()
	fresh.dir = s.dir
	fresh.files = append([]string(nil), s.files...)
	if err = fresh.cleanup(ctx); err != nil {
		return err
	}
	s.dir = ""
	return nil
}
