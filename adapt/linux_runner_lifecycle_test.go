//go:build linux

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	libs "lalune-desktop/Libs"
)

type runnerLifecycleProbeTun struct {
	stopped  bool
	cleaned  bool
}

func (t *runnerLifecycleProbeTun) Setup() error { return nil }
func (t *runnerLifecycleProbeTun) Start(net.Conn, *bool) {}
func (t *runnerLifecycleProbeTun) Stop() { t.stopped = true }
func (t *runnerLifecycleProbeTun) SetupRoutes(string, string) error { return nil }
func (t *runnerLifecycleProbeTun) CleanupRoutes() { t.cleaned = true }

func TestLinuxRunnerLifecycleReservationRejectsConcurrentStart(t *testing.T) {
	r := &LinuxRunner{}
	if !r.beginCoreStart() { t.Fatal("first start reservation was rejected") }
	if r.beginCoreStart() { t.Fatal("concurrent start reservation was accepted") }
	r.markCoreStarted(12345)
	r.cancelCoreStart()
	if r.beginCoreStart() { t.Fatal("start was accepted while a core PID is active") }
	r.markCoreStopped(54321)
	if r.beginCoreStart() { t.Fatal("stale waiter cleared a different active PID") }
	r.markCoreStopped(12345)
	if !r.beginCoreStart() { t.Fatal("start was not released after matching process exit") }
	r.cancelCoreStart()
}

func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH { return }
		if err != nil && err != syscall.EPERM { return }
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive after process-group termination", pid)
}

func waitRunnerStopped(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		pid := app.clientPID
		app.mu.Unlock()
		if !app.core.IsConnected() && pid == 0 { return }
		time.Sleep(20 * time.Millisecond)
	}
	app.mu.Lock()
	pid := app.clientPID
	app.mu.Unlock()
	t.Fatalf("runner did not finish cleanup: connected=%v clientPID=%d", app.core.IsConnected(), pid)
}

func TestLinuxRunnerStartupTimeoutKillsCoreProcessTree(t *testing.T) {
	commandLog := installTunCommandShims(t, "")
	isolateDirectFilesystem(t)
	logFile := filepath.Join(os.TempDir(), "lalune_core_logs.txt")
	_ = os.Remove(logFile)
	t.Cleanup(func() { _ = os.Remove(logFile) })

	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "timeout-child.pid")
	startedFile := filepath.Join(dir, "timeout-core-started")
	corePath := filepath.Join(dir, "fake-core")
	script := "#!/bin/sh\necho started > " + shellQuoteForTest(startedFile) + "\nsleep 60 & echo $! > " + shellQuoteForTest(childPIDFile) + "\nwait\n"
	if err := os.WriteFile(corePath, []byte(script), 0700); err != nil { t.Fatal(err) }

	app := tunTestApp()
	app.core.SetConnected(true)
	bridge := libs.NewBridge(app.core, app.tun, nil)
	runner := &LinuxRunner{app: app, startupTimeout: 5 * time.Second}
	runner.startCoreWithSudo([]string{corePath}, 52230, bridge)

	deadline := time.Now().Add(3 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 { break }
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		coreLog, _ := os.ReadFile(logFile)
		_, startedErr := os.Stat(startedFile)
		t.Fatalf("fake core did not create child PID (started marker error: %v); sudo/ip log:\n%s\ncore log:\n%s\nrunner logs:\n%s", startedErr, readTunCommandLog(t, commandLog), string(coreLog), app.core.GetLogsJson())
	}
	waitRunnerStopped(t, app)
	waitProcessGone(t, childPID)
}

func TestLinuxRunnerEarlyCoreExitKillsRemainingProcessTree(t *testing.T) {
	installTunCommandShims(t, "")
	isolateDirectFilesystem(t)

	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "early-exit-child.pid")
	corePath := filepath.Join(dir, "fake-core")
	script := "#!/bin/sh\nsleep 60 & echo $! > " + shellQuoteForTest(childPIDFile) + "\nexit 17\n"
	if err := os.WriteFile(corePath, []byte(script), 0700); err != nil { t.Fatal(err) }

	app := tunTestApp()
	app.core.SetConnected(true)
	bridge := libs.NewBridge(app.core, app.tun, nil)
	runner := &LinuxRunner{app: app, startupTimeout: 30 * time.Second}
	runner.startCoreWithSudo([]string{corePath}, 52230, bridge)

	deadline := time.Now().Add(3 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 { break }
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 { t.Fatal("fake core did not create early-exit child PID") }

	waitRunnerStopped(t, app)
	if logs := app.core.GetLogsJson(); !strings.Contains(logs, "Процесс завершился до готовности TUNCONF/traffic") {
		t.Fatalf("early process exit was not reported; runner logs: %s", logs)
	}
	waitProcessGone(t, childPID)
}

func TestLinuxRunnerSetupRoutesFailureKillsCoreProcessTree(t *testing.T) {
	installTunCommandShims(t, "ip tuntap add")
	isolateDirectFilesystem(t)
	logFile := filepath.Join(os.TempDir(), "lalune_core_logs.txt")
	_ = os.Remove(logFile)
	t.Cleanup(func() { _ = os.Remove(logFile) })

	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "setup-failure-child.pid")
	corePath := filepath.Join(dir, "fake-core")
	script := "#!/bin/sh\nprintf 'TUNCONF:192.0.2.10:8.8.8.8\\nАктивных: 1\\n' >> " + shellQuoteForTest(logFile) + "\nsleep 60 & echo $! > " + shellQuoteForTest(childPIDFile) + "\nwait\n"
	if err := os.WriteFile(corePath, []byte(script), 0700); err != nil { t.Fatal(err) }

	app := tunTestApp()
	app.core.SetConnected(true)
	bridge := libs.NewBridge(app.core, app.tun, nil)
	runner := &LinuxRunner{app: app, startupTimeout: 4 * time.Second}
	runner.startCoreWithSudo([]string{corePath}, 52230, bridge)

	deadline := time.Now().Add(3 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 { break }
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 { t.Fatal("fake core did not create child PID") }
	waitRunnerStopped(t, app)
	waitProcessGone(t, childPID)
}

func TestAbortCoreStartupKillsProcessGroupAndMarksDisconnected(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "abort-child.pid")
	script := "sleep 60 & echo $! > " + shellQuoteForTest(childPIDFile) + "; wait"
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil { t.Fatalf("start child fixture: %v", err) }
	deadline := time.Now().Add(2 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 { break }
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		_ = terminateCoreProcessGroup(cmd.Process.Pid)
		_ = cmd.Wait()
		t.Fatal("child PID fixture was not created")
	}
	core := libs.NewAppCore()
	core.SetConnected(true)
	bridge := libs.NewBridge(core, &runnerLifecycleProbeTun{}, nil)
	abortCoreStartup(bridge, cmd, "test abort")
	if core.IsConnected() { t.Fatal("connected state remained true after abort") }
	if err := cmd.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok { t.Fatalf("wait for killed fixture: %v", err) }
	}
	waitProcessGone(t, childPID)
}
