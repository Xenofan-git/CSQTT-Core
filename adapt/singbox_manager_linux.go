//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// SingBoxManager owns only the sing-box process. It intentionally has no
// reference to App, LinuxRunner, TUN, or CSQTT Core.
type SingBoxManager struct {
	mu sync.Mutex
	binary string
	configPath string
	logPath string
	checkTimeout time.Duration
	stopTimeout time.Duration
	cmd *exec.Cmd
	done chan struct{}
	logFile *os.File
	lastError string
	starting bool
	startDone chan struct{}
	startCancel context.CancelFunc
}

type SingBoxStatus struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid"`
	LogPath string `json:"log_path"`
	Error   string `json:"error,omitempty"`
}

func NewSingBoxManager(binary, configPath, logPath string) *SingBoxManager {
	return &SingBoxManager{
		binary: binary, configPath: configPath, logPath: logPath,
		checkTimeout: 10 * time.Second, stopTimeout: 5 * time.Second,
	}
}

func (m *SingBoxManager) checkConfig(ctx context.Context) error {
	if m.binary == "" || m.configPath == "" {
		return fmt.Errorf("sing-box binary and config path are required")
	}
	info, err := os.Stat(m.configPath)
	if err != nil { return fmt.Errorf("stat sing-box config: %w", err) }
	if !info.Mode().IsRegular() { return fmt.Errorf("sing-box config must be a regular file") }
	timeout := m.checkTimeout
	if timeout <= 0 { timeout = 10 * time.Second }
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, m.binary, "check", "-c", m.configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if checkCtx.Err() != nil { return fmt.Errorf("sing-box config check timed out: %w", checkCtx.Err()) }
		return fmt.Errorf("sing-box config check failed: %w: %s", err, string(output))
	}
	return nil
}

func (m *SingBoxManager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd != nil || m.starting {
		m.mu.Unlock()
		return fmt.Errorf("sing-box is already running, starting, or stopping")
	}
	checkCtx, cancel := context.WithCancel(ctx)
	startDone := make(chan struct{})
	m.starting, m.startDone, m.startCancel = true, startDone, cancel
	m.mu.Unlock()

	finishStart := func(err error) {
		cancel()
		m.mu.Lock()
		m.starting, m.startDone, m.startCancel = false, nil, nil
		if err != nil { m.lastError = err.Error() }
		m.mu.Unlock()
		close(startDone)
	}
	fail := func(err error) error { finishStart(err); return err }

	if err := m.checkConfig(checkCtx); err != nil { return fail(err) }
	if err := checkCtx.Err(); err != nil { return fail(fmt.Errorf("sing-box start cancelled: %w", err)) }
	if err := os.MkdirAll(filepath.Dir(m.logPath), 0700); err != nil {
		return fail(fmt.Errorf("create sing-box log directory: %w", err))
	}
	logFile, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil { return fail(fmt.Errorf("open sing-box log: %w", err)) }

	cmd := exec.Command(m.binary, "run", "-c", m.configPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = logFile, logFile

	m.mu.Lock()
	if err := checkCtx.Err(); err != nil {
		m.mu.Unlock()
		_ = logFile.Close()
		return fail(fmt.Errorf("sing-box start cancelled: %w", err))
	}
	if err := cmd.Start(); err != nil {
		m.lastError = err.Error()
		m.mu.Unlock()
		_ = logFile.Close()
		return fail(fmt.Errorf("start sing-box: %w", err))
	}
	done := make(chan struct{})
	m.cmd, m.done, m.logFile = cmd, done, logFile
	m.lastError = ""
	m.starting, m.startDone, m.startCancel = false, nil, nil
	m.mu.Unlock()
	close(startDone)

	go func() {
		waitErr := cmd.Wait()
		_ = logFile.Close()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd, m.done, m.logFile = nil, nil, nil
			if waitErr != nil { m.lastError = waitErr.Error() }
		}
		m.mu.Unlock()
		close(done)
	}()
	return nil
}
func (m *SingBoxManager) Stop() error {
	for {
		m.mu.Lock()
		if m.starting {
			startDone, cancel := m.startDone, m.startCancel
			m.mu.Unlock()
			if cancel != nil { cancel() }
			<-startDone
			continue
		}
		cmd, done := m.cmd, m.done
		timeout := m.stopTimeout
		m.mu.Unlock()
		if cmd == nil { return nil }
		if timeout <= 0 { timeout = 5 * time.Second }

		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return fmt.Errorf("terminate sing-box process group: %w", err)
		}
		timer := time.NewTimer(timeout)
		select {
		case <-done:
			if !timer.Stop() { <-timer.C }
			return nil
		case <-timer.C:
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return fmt.Errorf("force-kill sing-box process group: %w", err)
		}
		select {
		case <-done:
			return nil
		case <-time.After(timeout):
			return fmt.Errorf("sing-box process did not exit after SIGKILL")
		}
	}
}
func (m *SingBoxManager) Status() SingBoxStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := SingBoxStatus{LogPath: m.logPath, Error: m.lastError}
	if m.cmd != nil && m.cmd.Process != nil {
		status.Running = true
		status.PID = m.cmd.Process.Pid
	}
	return status
}

func (m *SingBoxManager) CopyLogs(w io.Writer) error {
	f, err := os.Open(m.logPath)
	if err != nil { return err }
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
