//go:build linux

package main

import (
	"fmt"
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

func startProcessGroupFixture(t *testing.T, childPIDFile string) *exec.Cmd {
	t.Helper()
	script := "sleep 60 & echo $! > " + shellQuoteForTest(childPIDFile) + "; wait"
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil { t.Fatalf("start process group fixture: %v", err) }
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(childPIDFile); err == nil { return cmd }
		time.Sleep(10 * time.Millisecond)
	}
	_ = terminateCoreProcessGroup(cmd.Process.Pid)
	_ = cmd.Wait()
	t.Fatal("child PID fixture was not created")
	return nil
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
	deadline := time.Now().Add(5 * time.Second)
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

func installRunnerFixtureSudo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sudo := "#!/bin/sh\nif [ \"$1\" = -v ]; then exit 0; fi\nif [ \"$1\" = -n ]; then shift; fi\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(sudo), 0700); err != nil { t.Fatal(err) }
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestLinuxRunnerStartupTimeoutKillsCoreProcessTree(t *testing.T) {
	installTunCommandShims(t, "")
	isolateDirectFilesystem(t)
	installRunnerFixtureSudo(t)
	logFile := filepath.Join(os.TempDir(), "lalune_core_logs.txt")
	_ = os.Remove(logFile)
	t.Cleanup(func() { _ = os.Remove(logFile) })

	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "timeout-child.pid")
	corePath := filepath.Join(dir, "fake-core")
	script := "#!/bin/sh\nsleep 60 & echo $! > " + shellQuoteForTest(childPIDFile) + "\nwait\n"
	if err := os.WriteFile(corePath, []byte(script), 0700); err != nil { t.Fatal(err) }

	app := tunTestApp()
	app.core.SetConnected(true)
	bridge := libs.NewBridge(app.core, app.tun, nil)
	runner := &LinuxRunner{app: app, startupTimeout: 150 * time.Millisecond}
	runner.startCoreWithSudo([]string{corePath}, 52230, bridge)

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
	if childPID == 0 { t.Fatal("fake core did not create child PID") }
	waitRunnerStopped(t, app)
	waitProcessGone(t, childPID)
}

func TestLinuxRunnerSetupRoutesFailureKillsCoreProcessTree(t *testing.T) {
	installTunCommandShims(t, "ip tuntap add")
	isolateDirectFilesystem(t)
	installRunnerFixtureSudo(t)
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

func TestAbortCoreStartupCleansBridgeOwnership(t *testing.T) {
	core := libs.NewAppCore()
	core.SetConnected(true)
	tun := &runnerLifecycleProbeTun{}
	bridge := libs.NewBridge(core, tun, nil)
	bridge.TunRunning = true
	abortCoreStartup(bridge, nil, fmt.Sprintf("test abort"))
	if core.IsConnected() { t.Fatal("connected state remained true after abort") }
	if !tun.stopped { t.Fatal("TUN Stop was not called") }
	if !tun.cleaned { t.Fatal("TUN CleanupRoutes was not called") }
}
