package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type packetWriteCloser struct{ bytes.Buffer }

func (w *packetWriteCloser) Close() error { return nil }
func packet(body []byte) []byte           { return append(wireUint(uint32(len(body))), body...) }

func TestRemoteSFTPBoundedProtocol(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		good bool
	}{
		{"success", append([]byte{101}, append(wireUint(1), wireUint(0)...)...), true},
		{"wrong-id", append([]byte{101}, append(wireUint(2), wireUint(0)...)...), false},
		{"short-status", append([]byte{101}, wireUint(1)...), false},
		{"unexpected-type", append([]byte{103}, wireUint(1)...), false},
		{"remote-denial", append([]byte{101}, append(wireUint(1), wireUint(3)...)...), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.body) == 9 {
				tc.body = append(tc.body, make([]byte, 8)...)
			}
			w := &packetWriteCloser{}
			s := &remoteSFTP{in: w, out: io.NopCloser(bytes.NewReader(packet(tc.body))), cancel: func() {}}
			_, err := s.request(context.Background(), 13, 101, wireText("/tmp/fixture"))
			if (err == nil) != tc.good {
				t.Fatalf("unexpected protocol outcome %v", err)
			}
		})
	}
	for _, b := range [][]byte{wireUint(sftpMaxPacket + 1), wireUint(0), append(wireUint(50), 1, 2)} {
		s := &remoteSFTP{out: io.NopCloser(bytes.NewReader(b))}
		if _, err := s.receive(); err == nil {
			t.Fatal("invalid or unbounded packet accepted")
		}
	}
	for _, b := range [][]byte{{}, wireUint(100)} {
		if _, _, err := readWireString(b); err == nil {
			t.Fatal("invalid string accepted")
		}
	}
}

func TestRemoteStageRejectsBeforeConnection(t *testing.T) {
	for _, request := range []string{
		"{", `{"uninstall":true}`,
		`{"protocol":"CSQTT","host":"127.0.0.1","user":"root"}`,
		`{"protocol":"CSQTT","host":"203.0.113.10","user":"root","keyPath":"/missing","mainPassword":"a","webUser":"a","webPassword":"a","deviceId":"a"}`,
	} {
		var output bytes.Buffer
		if err := runRemoteStage(context.Background(), strings.NewReader(request), &output, t.TempDir()); err == nil {
			t.Fatal("invalid request or unpinned assets accepted")
		}
		if strings.Contains(output.String(), "CSQTT_STAGE_VERIFIED") {
			t.Fatal("false transfer success")
		}
	}
}

// Opt-in NEW adapter acceptance over a real remote SSH connection. No local
// SFTP fixture, installer, service, system credential or configuration is used.
// The existing SSH identity remains at the caller's specified path.
func TestRemoteTransportRealEndpoint(t *testing.T) {
	host, key := os.Getenv("CSQTT_REMOTE_TRANSPORT_HOST"), os.Getenv("CSQTT_REMOTE_TRANSPORT_KEY")
	if host == "" || key == "" {
		t.Skip("set remote host and existing SSH key for network acceptance")
	}
	r := Request{Protocol: "CSQTT", Host: host, User: "root", KeyPath: key, MainPassword: "fixture", WebUser: "fixture", WebPassword: "fixture", DeviceID: "transport-fixture"}
	transport, err := newRemoteTransport(r)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := transport.openSFTP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err = s.workspace(ctx); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	defer func() {
		if s.dir != "" {
			cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if err := s.cleanup(cleanup); err != nil {
				t.Error(err)
			}
		}
	}()
	peer, err := s.peer("deploy-csqtt-core-250.sh")
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("CSQTT remote transport acceptance; inert text only.\n"), 5120)
	if err = verifyStagedFixture(ctx, peer, data); err != nil {
		t.Fatal(err)
	}
	// Read the committed final file through the actual SFTP channel.
	h, err := s.open(ctx, peer.final, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader := &remoteFileReader{s: s, ctx: ctx, handle: h}
	actual, err := io.ReadAll(io.LimitReader(reader, int64(len(data))+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || !bytes.Equal(actual, data) {
		t.Fatal("published remote bytes differ")
	}
	if _, err = s.request(ctx, 17, 105, wireText(peer.temporary)); !errors.Is(err, sftpStatus(2)) {
		t.Fatal("temporary file remains")
	}
	if err = s.cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.request(ctx, 17, 105, wireText(dir)); !errors.Is(err, sftpStatus(2)) {
		t.Fatal("remote workspace remains")
	}
	t.Logf("remote SFTP: %d bytes verified after rename; temporary file and directory absent", len(data))
	// Exercise the exact two-object orchestration called by --stage-only,
	// with inert test bytes instead of any executable/pinned release payload.
	if err = stageRemoteAssets(ctx, transport, data, append([]byte("second object\n"), data...)); err != nil {
		t.Fatal(err)
	}
	t.Log("manager staging orchestration: both inert objects verified and cleaned")
}

func TestRemoteTransportLostSessionCleanup(t *testing.T) {
	host, key := os.Getenv("CSQTT_REMOTE_TRANSPORT_HOST"), os.Getenv("CSQTT_REMOTE_TRANSPORT_KEY")
	if host == "" || key == "" {
		t.Skip("set remote host and existing SSH key for network acceptance")
	}
	r := Request{Protocol: "CSQTT", Host: host, User: "root", KeyPath: key, MainPassword: "fixture", WebUser: "fixture", WebPassword: "fixture", DeviceID: "transport-fixture"}
	transport, err := newRemoteTransport(r)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := transport.openSFTP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err = s.workspace(ctx); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	peer, err := s.peer("deploy-csqtt-core-250.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.putTemporary(ctx, strings.NewReader("inert interrupted-transfer fixture")); err != nil {
		t.Fatal(err)
	}
	// Kill only this test's SSH child, after writing a temporary object.
	s.cancel()
	cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err = cleanupRemoteStage(cleanup, transport, s); err != nil {
		t.Fatal(err)
	}
	verify, err := transport.openSFTP(cleanup)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.close()
	if _, err = verify.request(cleanup, 17, 105, wireText(dir)); !errors.Is(err, sftpStatus(2)) {
		t.Fatal("remote workspace remains after interrupted connection")
	}
	t.Log("lost SSH session: cleanup reconnected only to remove owned staging files; directory absence verified")
}
