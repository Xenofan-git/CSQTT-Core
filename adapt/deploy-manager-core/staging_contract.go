package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"time"
)

// A transport-neutral contract for a single staged fixture. The interface
// has no server addresses, commands or credentials. It is implemented by
// the remote OpenSSH/SFTP adapter and by isolated test fixtures. The manager
// exposes remote staging via --stage-only without running an installer.
const stagingCleanupTimeout = 5 * time.Second

type stagingPeer interface {
	putTemporary(context.Context, io.Reader) error
	readTemporary(context.Context) (io.ReadCloser, error)
	commitTemporary(context.Context) error
	discardTemporary(context.Context) error
}

// Accept a transfer only after exact size and content verification, as a
// stronger version of upstream 2.5.3's SFTP lstat-size check. The peer must
// have an exclusive, private temporary object; commit publishes it only
// after verification. A failed/ambiguous commit is never retried here.
func verifyStagedFixture(ctx context.Context, peer stagingPeer, data []byte) (err error) {
	if peer == nil || len(data) == 0 || len(data) > 100_000_000 {
		return errors.New("invalid staging fixture")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Keep the hash and the bytes passed to the writer consistent.
	snapshot := append([]byte(nil), data...)
	want := sha256.Sum256(snapshot)
	attempted := false
	committed := false
	defer func() {
		if attempted && !committed {
			// Cleanup uses a bounded independent context even when the caller
			// has canceled. Cleanup only applies to the temporary object.
			cleanup, cancel := context.WithTimeout(context.Background(), stagingCleanupTimeout)
			defer cancel()
			if cleanupErr := peer.discardTemporary(cleanup); cleanupErr != nil {
				err = errors.Join(err, errors.New("temporary staging cleanup failed"))
			}
		}
	}()
	attempted = true
	if err = peer.putTemporary(ctx, bytes.NewReader(snapshot)); err != nil {
		return errors.New("temporary transfer failed")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r, err := peer.readTemporary(ctx)
	if err != nil {
		return errors.New("temporary readback failed")
	}
	h := sha256.New()
	n, readErr := io.Copy(h, io.LimitReader(r, int64(len(snapshot))+1))
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || n != int64(len(snapshot)) || !bytes.Equal(h.Sum(nil), want[:]) {
		return errors.New("staged size or SHA-256 mismatch")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = peer.commitTemporary(ctx); err != nil {
		return errors.New("staging commit outcome unknown")
	}
	committed = true
	return nil
}
