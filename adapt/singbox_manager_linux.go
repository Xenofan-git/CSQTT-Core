//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
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
	configuring bool
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

func (m *SingBoxManager) checkConfig(ctx context.Context) error { return m.checkConfigPath(ctx, m.configPath) }

func (m *SingBoxManager) checkConfigPath(ctx context.Context, configPath string) error {
	if m.binary == "" || configPath == "" {
		return fmt.Errorf("sing-box binary and config path are required")
	}
	info, err := os.Stat(configPath)
	if err != nil { return fmt.Errorf("stat sing-box config: %w", err) }
	if !info.Mode().IsRegular() { return fmt.Errorf("sing-box config must be a regular file") }
	timeout := m.checkTimeout
	if timeout <= 0 { timeout = 10 * time.Second }
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, m.binary, "check", "-c", configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if checkCtx.Err() != nil { return fmt.Errorf("sing-box config check timed out: %w", checkCtx.Err()) }
		return fmt.Errorf("sing-box config check failed: %w: %s", err, string(output))
	}
	return nil
}


func (m *SingBoxManager) SaveConfig(input singBoxConfigInput) error {
	encoded, err := buildSingBoxConfig(input)
	if err != nil { return fmt.Errorf("build sing-box config: %w", err) }
	m.mu.Lock()
	if m.cmd != nil || m.starting || m.configuring {
		m.mu.Unlock()
		return fmt.Errorf("cannot save sing-box config while running, starting, or configuring")
	}
	m.configuring = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.configuring = false; m.mu.Unlock() }()

	if m.configPath == "" || m.binary == "" { return fmt.Errorf("sing-box binary and config path are required") }
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0700); err != nil { return fmt.Errorf("create sing-box config directory: %w", err) }
	f, err := os.CreateTemp(dir, ".sing-box-config-*.json")
	if err != nil { return fmt.Errorf("create temporary sing-box config: %w", err) }
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil { _ = f.Close(); return err }
	if _, err := f.Write(encoded); err != nil { _ = f.Close(); return fmt.Errorf("write temporary sing-box config: %w", err) }
	if err := f.Sync(); err != nil { _ = f.Close(); return fmt.Errorf("sync temporary sing-box config: %w", err) }
	if err := f.Close(); err != nil { return fmt.Errorf("close temporary sing-box config: %w", err) }
	ctx, cancel := context.WithTimeout(context.Background(), m.checkTimeout)
	defer cancel()
	if err := m.checkConfigPath(ctx, tmp); err != nil { return fmt.Errorf("refusing invalid sing-box config: %w", err) }
	m.mu.Lock()
	if m.cmd != nil || m.starting { m.mu.Unlock(); return fmt.Errorf("sing-box started while config was being validated") }
	if err := os.Rename(tmp, m.configPath); err != nil { m.mu.Unlock(); return fmt.Errorf("activate sing-box config: %w", err) }
	m.lastError = ""
	m.mu.Unlock()
	if d, err := os.Open(dir); err == nil { _ = d.Sync(); _ = d.Close() }
	return nil
}

func (m *SingBoxManager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd != nil || m.starting || m.configuring {
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


func (m *SingBoxManager) LoadConfig() (singBoxConfigInput, error) {
	var input singBoxConfigInput
	data, err := os.ReadFile(m.configPath)
	if err != nil { return input, fmt.Errorf("read sing-box config: %w", err) }
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil { return input, fmt.Errorf("decode sing-box config: %w", err) }
	inbounds, _ := raw["inbounds"].([]any)
	if len(inbounds) != 1 { return input, fmt.Errorf("unsupported sing-box config: expected one inbound") }
	inbound, _ := inbounds[0].(map[string]any)
	if inbound["type"] != "socks" || inbound["tag"] != singBoxInboundTag { return input, fmt.Errorf("unsupported sing-box inbound") }
	host, _ := inbound["listen"].(string)
	port, ok := inbound["listen_port"].(float64)
	if host == "" || !ok || port < 1 || port > 65535 { return input, fmt.Errorf("invalid sing-box inbound endpoint") }
	input.ListenAddress = net.JoinHostPort(host, strconv.Itoa(int(port)))
	outbounds, _ := raw["outbounds"].([]any)
	known := make(map[string]map[string]any)
	for _, item := range outbounds {
		entry, ok := item.(map[string]any); if !ok { continue }
		tag, _ := entry["tag"].(string); known[tag] = entry
	}
	csqtt := known[singBoxCSQTTTag]
	if csqtt == nil || csqtt["type"] != "socks" { return input, fmt.Errorf("missing CSQTT SOCKS outbound") }
	csqttHost, _ := csqtt["server"].(string)
	csqttPort, ok := csqtt["server_port"].(float64)
	if csqttHost == "" || !ok || csqttPort < 1 || csqttPort > 65535 { return input, fmt.Errorf("invalid CSQTT SOCKS endpoint") }
	input.CSQTTAddress = net.JoinHostPort(csqttHost, strconv.Itoa(int(csqttPort)))
	for tag, entry := range known {
		if !strings.HasPrefix(tag, "socks-") { continue }
		host, _ := entry["server"].(string); port, ok := entry["server_port"].(float64)
		if !ok || port < 1 || port > 65535 { return input, fmt.Errorf("invalid SOCKS5 outbound %q", tag) }
		username, _ := entry["username"].(string); password, _ := entry["password"].(string)
		input.SOCKS5 = append(input.SOCKS5, singBoxSOCKS5Outbound{ID:strings.TrimPrefix(tag,"socks-"), Server:host, ServerPort:int(port), Username:username, Password:password, Enabled:true})
	}
	route, _ := raw["route"].(map[string]any); rules, _ := route["rules"].([]any)
	for _, item := range rules {
		rule, ok := item.(map[string]any); if !ok || rule["action"] != "route" { continue }
		outbound, _ := rule["outbound"].(string)
		domains, suffixes, cidrs := stringSlice(rule["domain"]), stringSlice(rule["domain_suffix"]), stringSlice(rule["ip_cidr"])
		if outbound == singBoxDirectTag { input.DirectDomains=append(input.DirectDomains,domains...); input.DirectIPCIDRs=append(input.DirectIPCIDRs,cidrs...); continue }
		if outbound == "" || known[outbound] == nil { return input, fmt.Errorf("route references missing outbound %q", outbound) }
		input.Rules=append(input.Rules,singBoxRouteRule{ID:fmt.Sprintf("loaded-%d",len(input.Rules)+1),Domains:domains,DomainSuffixes:suffixes,Outbound:outbound,Enabled:true})
	}
	return input,nil
}
func stringSlice(value any) []string {
	items, _ := value.([]any); out := make([]string,0,len(items))
	for _, item := range items { if s,ok:=item.(string); ok { out=append(out,s) } }
	return out
}

func (m *SingBoxManager) CopyLogs(w io.Writer) error {
	f, err := os.Open(m.logPath)
	if err != nil { return err }
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
