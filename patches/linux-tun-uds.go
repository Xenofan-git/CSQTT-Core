//go:build linux
// +build linux

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	linuxTUNSETIFF = 0x400454ca
	linuxIFFTUN    = 0x0001
	linuxIFFNOPI   = 0x1000
)

type linuxTunIfreq struct {
	Name  [16]byte
	Flags uint16
	_     [22]byte
}

func openLinuxTUN(name string) (*os.File, error) {
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var ifr linuxTunIfreq
	copy(ifr.Name[:], name)
	ifr.Flags = linuxIFFTUN | linuxIFFNOPI
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), linuxTUNSETIFF, uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		_ = f.Close()
		return nil, errno
	}
	return f, nil
}

func sendLinuxTunFD(udsPath string, tun *os.File) error {
	var last error
	for i := 0; i < 100; i++ {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: udsPath, Net: "unix"})
		if err != nil {
			last = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, _, err = conn.WriteMsgUnix([]byte("TUN-FD\n"), syscall.UnixRights(int(tun.Fd())), nil)
		if err != nil {
			_ = conn.Close()
			last = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		ack := make([]byte, 32)
		n, err := conn.Read(ack)
		_ = conn.Close()
		if err != nil {
			last = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !strings.HasPrefix(string(ack[:n]), "TUN-ACK") {
			return fmt.Errorf("unexpected TUN ACK: %q", string(ack[:n]))
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("UDS connection failed")
	}
	return fmt.Errorf("send TUN FD: %w", last)
}

func tunUDSPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("lalune-tun-%d.sock", os.Getpid()))
}

// Start is intentionally a no-op on Linux: the TUN FD is passed to the Rust core over UDS.
func (t *LinuxTun) Start(_ net.Conn, _ *bool) {}

func (t *LinuxTun) Setup() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tunFile != nil {
		return nil
	}
	f, err := openLinuxTUN("csqtt0")
	if err != nil {
		return fmt.Errorf("open csqtt0 TUN: %w", err)
	}
	t.tunFile = f
	return nil
}

func (t *LinuxTun) AttachToUDS(path string) error {
	t.mu.Lock()
	f := t.tunFile
	t.mu.Unlock()
	if f == nil {
		return fmt.Errorf("TUN FD is not initialized")
	}
	return sendLinuxTunFD(path, f)
}

func (t *LinuxTun) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tunFile != nil {
		_ = t.tunFile.Close()
		t.tunFile = nil
	}
}
