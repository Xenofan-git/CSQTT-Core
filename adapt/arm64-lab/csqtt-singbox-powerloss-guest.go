//go:build ignore

// Build by explicitly passing this file to go build alongside the current
// SingBoxManager sources. The ignore tag excludes it from ordinary builds.
// This executable is an intentionally narrow, disposable AArch64 systemd
// guest fixture built from the ACTUAL CSQTT SingBoxManager sources. It is NOT
// a production daemon. It cannot alter host routes, sysctls or interfaces.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	labRoot    = "/home/csqttlab/powerloss"
	labListen  = "127.0.0.1:19180"
	echoListen = "127.0.0.1:19181"
)

func labGuard() error {
	if runtime.GOARCH != "arm64" || os.Getenv("CSQTT_POWERLOSS_LAB") != "isolated-aarch64-qemu" {
		return errors.New("refusing non-disposable AArch64 guest")
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if host != "csqtt-core8-isolated" {
		return fmt.Errorf("unexpected guest hostname %q", host)
	}
	if os.Geteuid() == 0 {
		return errors.New("test daemon must run unprivileged")
	}
	if err := os.MkdirAll(labRoot, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(labRoot)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe lab root permissions")
	}
	return nil
}

func manager() *SingBoxManager {
	m := NewSingBoxManager("/usr/local/libexec/csqtt-powerloss-sing-box",
		filepath.Join(labRoot, "config.json"), filepath.Join(labRoot, "sing-box.log"))
	m.checkTimeout = 30 * time.Second
	return m
}

func initial() singBoxConfigInput {
	return singBoxConfigInput{ListenAddress: labListen, FinalOutbound: singBoxDirectTag}
}

func writeCheckpoint(stage string) {
	if stage != os.Getenv("CSQTT_POWERLOSS_STAGE") {
		return
	}
	dest := filepath.Join(labRoot, "checkpoint")
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	if _, err = f.WriteString(stage + "\n"); err != nil {
		panic(err)
	}
	if err = f.Sync(); err != nil {
		panic(err)
	}
	if err = f.Close(); err != nil {
		panic(err)
	}
	dir, err := os.Open(labRoot)
	if err != nil {
		panic(err)
	}
	if err = dir.Sync(); err != nil {
		panic(err)
	}
	if err = dir.Close(); err != nil {
		panic(err)
	}
	fmt.Println("POWERLOSS_GUEST_CHECKPOINT_DURABLE", stage)
	// Never return: parent will cut power to disposable QEMU VM from the host.
	select {}
}

func runEcho(ctx context.Context) error {
	ln, err := net.Listen("tcp", echoListen)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) { defer conn.Close(); _, _ = io.Copy(conn, conn) }(c)
		}
	}()
	return nil
}

func probe() error {
	c, err := net.DialTimeout("tcp", labListen, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	var greeting [2]byte
	if _, err = io.ReadFull(c, greeting[:]); err != nil {
		return err
	}
	if greeting != [2]byte{5, 0} {
		return fmt.Errorf("invalid SOCKS5 greeting %v", greeting)
	}
	req := []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 0}
	req[8] = byte(19181 >> 8)
	req[9] = byte(19181 & 255)
	if _, err = c.Write(req); err != nil {
		return err
	}
	var hdr [4]byte
	if _, err = io.ReadFull(c, hdr[:]); err != nil {
		return err
	}
	if hdr[1] != 0 {
		return fmt.Errorf("SOCKS5 connect failed %d", hdr[1])
	}
	n := 0
	switch hdr[3] {
	case 1:
		n = 6
	case 4:
		n = 18
	case 3:
		var size [1]byte
		if _, err = io.ReadFull(c, size[:]); err != nil {
			return err
		}
		n = int(size[0]) + 2
	default:
		return fmt.Errorf("invalid SOCKS5 ATYP %d", hdr[3])
	}
	if _, err = io.CopyN(io.Discard, c, int64(n)); err != nil {
		return err
	}
	payload := []byte("CSQTT_CORE_REAL_ARM64_QEMU_POWERCUT_TCP")
	if _, err = c.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(c, got); err != nil {
		return err
	}
	if string(got) != string(payload) {
		return fmt.Errorf("echo mismatch %q", got)
	}
	fmt.Println("CSQTT_POWERLOSS_GUEST_SOCKS5_TCP_ECHO_PASS")
	return nil
}

func serve() error {
	m := manager()
	if err := m.Start(context.Background()); err != nil {
		return fmt.Errorf("start/recover: %w", err)
	}
	defer m.Stop()
	if err := m.waitSOCKSReady(labListen); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runEcho(ctx); err != nil {
		return err
	}
	// A full VM power cut can leave a stale Unix socket pathname on disk.
	// Refuse a concurrent live controller, and remove only an unowned socket.
	control := filepath.Join(labRoot, "control.sock")
	if st, err := os.Lstat(control); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return errors.New("control pathname is not a socket")
		}
		live, connErr := net.DialTimeout("unix", control, 500*time.Millisecond)
		if connErr == nil {
			_ = live.Close()
			return errors.New("control socket already owned by another process")
		}
		if err := os.Remove(control); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", control)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close(); _ = os.Remove(filepath.Join(labRoot, "control.sock")) }()
	if err := os.Chmod(filepath.Join(labRoot, "control.sock"), 0600); err != nil {
		return err
	}
	// All control requests only come from this disposable guest filesystem.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				request := make([]byte, 64)
				n, err := conn.Read(request)
				if err != nil {
					return
				}
				if strings.TrimSpace(string(request[:n])) != "apply" {
					_, _ = conn.Write([]byte("UNKNOWN\n"))
					return
				}
				_, _ = conn.Write([]byte("APPLY_ACCEPTED\n"))
				m.applyCheckpointHook = writeCheckpoint
				next := initial()
				next.DirectDomains = []string{"candidate-only.powerloss.test"}
				_, err = m.ApplyConfig(next)
				fmt.Println("POWERLOSS_UNEXPECTED_APPLY_RETURN", err)
			}(c)
		}
	}()
	fmt.Printf("POWERLOSS_GUEST_SERVICE_READY pid=%d\n", m.Status().PID)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	fmt.Println("POWERLOSS_GUEST_GRACEFUL_STOP")
	return nil
}

func diagnostic() error {
	config, err := os.ReadFile(filepath.Join(labRoot, "config.json"))
	if err != nil {
		return err
	}
	panel, err := os.ReadFile(filepath.Join(labRoot, "config.json.panel.json"))
	if err != nil {
		return err
	}
	cHash := sha256.Sum256(config)
	pHash := sha256.Sum256(panel)
	fmt.Printf("POWERLOSS_GUEST_DISK_HASH config=%s panel=%s\n",
		hex.EncodeToString(cHash[:]), hex.EncodeToString(pHash[:]))
	if _, err = os.Lstat(filepath.Join(labRoot, "config.json.live-apply.json")); errors.Is(err, os.ErrNotExist) {
		fmt.Println("POWERLOSS_GUEST_LIVE_APPLY_JOURNAL_ABSENT")
	} else if err != nil {
		return err
	} else {
		fmt.Println("POWERLOSS_GUEST_LIVE_APPLY_JOURNAL_PRESENT")
	}
	return nil
}

func main() {
	if err := labGuard(); err != nil {
		fmt.Fprintln(os.Stderr, "POWERLOSS_GUEST_REJECTED:", err)
		os.Exit(75)
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fixture init|serve|probe|diagnose|apply")
		os.Exit(64)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = manager().SaveConfig(initial())
	case "serve":
		err = serve()
	case "probe":
		err = probe()
	case "diagnose":
		err = diagnostic()
	case "apply":
		var c net.Conn
		c, err = net.DialTimeout("unix", filepath.Join(labRoot, "control.sock"), 3*time.Second)
		if err == nil {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			_, err = c.Write([]byte("apply\n"))
			if err == nil {
				var reply [32]byte
				var n int
				n, err = c.Read(reply[:])
				if err == nil && !strings.Contains(string(reply[:n]), "APPLY_ACCEPTED") {
					err = fmt.Errorf("bad control response %q", reply[:n])
				}
			}
		}
	default:
		err = errors.New("unknown command")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "POWERLOSS_GUEST_FAILURE:", err)
		// Retrying a bounded cold-boot resource delay is different from
		// silently ignoring a corrupt or externally modified recovery journal.
		if strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "deadline exceeded") ||
			strings.Contains(err.Error(), "listener") {
			os.Exit(1) // systemd restarts, bounded by StartLimitBurst
		}
		os.Exit(75) // unsafe on-disk state: RestartPreventExitStatus
	}
}
