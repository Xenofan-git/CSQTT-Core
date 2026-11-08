//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sort"
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
	configMu sync.RWMutex
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

type singBoxPanelState struct {
	ConfigSHA256 string             `json:"config_sha256"`
	Input        singBoxConfigInput `json:"input"`
}

func (m *SingBoxManager) panelStatePath() string { return m.configPath + ".panel.json" }

// panelStatePathForHash stores immutable panel metadata beside the config
// generation it describes. The sidecar is published before the config rename,
// so readers can always find matching metadata after either side of a crash.
func (m *SingBoxManager) panelStatePathForHash(hash string) string {
	return m.configPath + ".panel." + hash + ".json"
}


func NewSingBoxManager(binary, configPath, logPath string) *SingBoxManager {
	return &SingBoxManager{
		binary: binary, configPath: configPath, logPath: logPath,
		checkTimeout: 10 * time.Second, stopTimeout: 5 * time.Second,
	}
}

func singBoxListenIPAssigned(ip net.IP, addrs []net.Addr) bool {
	for _, addr := range addrs {
		assigned, _, err := net.ParseCIDR(addr.String())
		if err == nil && assigned.Equal(ip) { return true }
	}
	return false
}

func validateSingBoxListenAddress(address string) error {
	host, _, err := singBoxHostPort(address, "listen address")
	if err != nil { return err }
	ip := net.ParseIP(host)
	if ip == nil { return fmt.Errorf("listen address must be an IP literal") }
	if ip.IsLoopback() { return nil }
	if !ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return fmt.Errorf("listen address must be loopback or a private eth1 address")
	}
	iface, err := net.InterfaceByName("eth1")
	if err != nil { return fmt.Errorf("private sing-box inbound must bind to eth1: %w", err) }
	addrs, err := iface.Addrs()
	if err != nil { return fmt.Errorf("read eth1 addresses: %w", err) }
	if !singBoxListenIPAssigned(ip, addrs) {
		return fmt.Errorf("sing-box inbound address %s is not assigned to eth1", ip)
	}
	return nil
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
	var err error
	input, err = normalizeSingBoxSystemCards(input)
	if err != nil { return fmt.Errorf("validate sing-box system cards: %w", err) }
	encoded, err := buildSingBoxConfig(input)
	if err != nil { return fmt.Errorf("build sing-box config: %w", err) }
	if err := m.validateRuleSetPaths(input.RuleSets); err != nil { return fmt.Errorf("unsafe Rule Set path: %w", err) }
	if err := validateSingBoxListenAddress(input.ListenAddress); err != nil {
		return fmt.Errorf("unsafe sing-box inbound: %w", err)
	}
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

	// Persist the complete panel model separately from sing-box's strict schema.
	// The checksum binds disabled cards and ordering to the exact active config;
	// a crash between the two renames makes the stale sidecar safely ignorable.
	sum := sha256.Sum256(encoded)
	stateData, err := json.MarshalIndent(singBoxPanelState{ConfigSHA256: hex.EncodeToString(sum[:]), Input: input}, "", "  ")
	if err != nil { return fmt.Errorf("encode sing-box panel state: %w", err) }
	stateFile, err := os.CreateTemp(dir, ".sing-box-panel-*.json")
	if err != nil { return fmt.Errorf("create temporary sing-box panel state: %w", err) }
	stateTmp := stateFile.Name()
	defer os.Remove(stateTmp)
	if err := stateFile.Chmod(0600); err != nil { _ = stateFile.Close(); return err }
	if _, err := stateFile.Write(append(stateData, '\n')); err != nil { _ = stateFile.Close(); return fmt.Errorf("write sing-box panel state: %w", err) }
	if err := stateFile.Sync(); err != nil { _ = stateFile.Close(); return fmt.Errorf("sync sing-box panel state: %w", err) }
	if err := stateFile.Close(); err != nil { return fmt.Errorf("close sing-box panel state: %w", err) }

	// Serialize the generation switch with readers. The hash-named sidecar is
	// immutable and installed first; the config rename is the single commit
	// point. A crash before it leaves only an orphan sidecar, while a crash
	// after it leaves a complete config/sidecar pair.
	m.configMu.Lock()
	defer m.configMu.Unlock()
	statePath := m.panelStatePathForHash(hex.EncodeToString(sum[:]))
	m.mu.Lock()
	if m.cmd != nil || m.starting { m.mu.Unlock(); return fmt.Errorf("sing-box started while config was being validated") }
	if err := os.Rename(stateTmp, statePath); err != nil { m.mu.Unlock(); return fmt.Errorf("activate sing-box panel state: %w", err) }
	// Make the new sidecar durable before publishing the config that refers to
	// it. If this sync fails, the old config and its old sidecar remain active.
	dirHandle, err := os.Open(dir)
	if err != nil { m.mu.Unlock(); return fmt.Errorf("open sing-box config directory for sync: %w", err) }
	if err := dirHandle.Sync(); err != nil { _ = dirHandle.Close(); m.mu.Unlock(); return fmt.Errorf("sync sing-box panel state directory entry: %w", err) }
	if err := dirHandle.Close(); err != nil { m.mu.Unlock(); return fmt.Errorf("close sing-box config directory: %w", err) }
	if err := os.Rename(tmp, m.configPath); err != nil { m.mu.Unlock(); return fmt.Errorf("activate sing-box config: %w", err) }
	m.lastError = ""
	m.mu.Unlock()
	// The config is now committed. Sync its directory entry before pruning old
	// generations, so either the old pair or the new pair survives a crash.
	dirHandle, err = os.Open(dir)
	if err != nil { return fmt.Errorf("config committed but opening directory for sync failed: %w", err) }
	if err := dirHandle.Sync(); err != nil { _ = dirHandle.Close(); return fmt.Errorf("config committed but directory sync failed: %w", err) }
	if err := dirHandle.Close(); err != nil { return fmt.Errorf("config committed but directory close failed: %w", err) }
	// Old generations and the pre-generation sidecar are no longer needed.
	// Readers are excluded by configMu until cleanup is complete.
	if matches, err := filepath.Glob(m.configPath + ".panel.*.json"); err == nil {
		for _, oldPath := range matches { if oldPath != statePath { _ = os.Remove(oldPath) } }
	}
	_ = os.Remove(m.panelStatePath())
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

	input, loadErr := m.LoadConfig()
	if loadErr != nil { return fail(loadErr) }
	// Revalidate persisted local Rule Set paths at startup as configs may be
	// edited outside SaveConfig after they were originally written.
	if err := m.validateRuleSetPaths(input.RuleSets); err != nil {
		return fail(fmt.Errorf("unsafe Rule Set path: %w", err))
	}
	if err := validateSingBoxListenAddress(input.ListenAddress); err != nil {
		return fail(fmt.Errorf("unsafe sing-box inbound: %w", err))
	}
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
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	var input singBoxConfigInput
	data, err := os.ReadFile(m.configPath)
	if err != nil { return input, fmt.Errorf("read sing-box config: %w", err) }
	// Prefer the full panel model when it is bound to this exact generated
	// sing-box config. This round-trips disabled cards and their priority
	// without inserting non-schema fields into sing-box's own JSON.
	sum := sha256.Sum256(data)
	sumHex := hex.EncodeToString(sum[:])
	stateData, stateErr := os.ReadFile(m.panelStatePathForHash(sumHex))
	generationSidecar := stateErr == nil
	if errors.Is(stateErr, os.ErrNotExist) {
		// Backward compatibility with configs written before hash-named
		// sidecars were introduced.
		stateData, stateErr = os.ReadFile(m.panelStatePath())
	}
	if stateErr == nil {
		var state singBoxPanelState
		if err := json.Unmarshal(stateData, &state); err != nil { return input, fmt.Errorf("decode sing-box panel state: %w", err) }
		if state.ConfigSHA256 == sumHex {
			generated, err := buildSingBoxConfig(state.Input)
			if err != nil { return input, fmt.Errorf("validate sing-box panel state: %w", err) }
			generatedSum := sha256.Sum256(generated)
			if hex.EncodeToString(generatedSum[:]) != state.ConfigSHA256 { return input, fmt.Errorf("sing-box panel state does not match active config") }
			return state.Input, nil
		}
		if generationSidecar { return input, fmt.Errorf("sing-box panel state generation does not match active config") }
		return input, fmt.Errorf("sing-box config changed outside the panel; refusing to discard saved disabled-card and priority metadata")
	} else if !errors.Is(stateErr, os.ErrNotExist) {
		return input, fmt.Errorf("read sing-box panel state: %w", stateErr)
	} else if matches, globErr := filepath.Glob(m.configPath + ".panel.*.json"); globErr != nil {
		return input, fmt.Errorf("inspect sing-box panel state generations: %w", globErr)
	} else if len(matches) > 0 {
		return input, fmt.Errorf("sing-box config changed outside the panel; no matching panel-state generation exists, refusing lossy import")
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil { return input, fmt.Errorf("decode sing-box config: %w", err) }
	for key := range raw {
		if key != "log" && key != "inbounds" && key != "outbounds" && key != "route" {
			return input, fmt.Errorf("unsupported top-level sing-box field %q; refusing lossy panel import", key)
		}
	}
	inbounds, ok := raw["inbounds"].([]any)
	if !ok || len(inbounds) != 1 { return input, fmt.Errorf("unsupported sing-box config: expected one inbound") }
	inbound, ok := inbounds[0].(map[string]any)
	if !ok || inbound["type"] != "socks" || inbound["tag"] != singBoxInboundTag { return input, fmt.Errorf("unsupported sing-box inbound") }
	for key := range inbound {
		if key != "type" && key != "tag" && key != "listen" && key != "listen_port" {
			return input, fmt.Errorf("unsupported sing-box inbound field %q; refusing lossy panel import", key)
		}
	}
	host, _ := inbound["listen"].(string)
	port, ok := inbound["listen_port"].(float64)
	if host == "" || !ok || port < 1 || port > 65535 { return input, fmt.Errorf("invalid sing-box inbound endpoint") }
	input.ListenAddress = net.JoinHostPort(host, strconv.Itoa(int(port)))
	outbounds, ok := raw["outbounds"].([]any)
	if !ok || len(outbounds) == 0 {
		return input, fmt.Errorf("invalid sing-box outbounds configuration")
	}
	known := make(map[string]map[string]any, len(outbounds))
	for index, item := range outbounds {
		entry, ok := item.(map[string]any)
		if !ok {
			return input, fmt.Errorf("invalid sing-box outbound at index %d", index)
		}
		tag, ok := entry["tag"].(string)
		if !ok || strings.TrimSpace(tag) == "" {
			return input, fmt.Errorf("sing-box outbound at index %d has no valid tag", index)
		}
		if _, exists := known[tag]; exists {
			return input, fmt.Errorf("duplicate sing-box outbound tag %q", tag)
		}
		known[tag] = entry
	}
	if direct := known[singBoxDirectTag]; direct != nil {
		if direct["type"] != "direct" || len(direct) != 2 {
			return input, fmt.Errorf("unsupported Direct outbound shape; refusing lossy panel import")
		}
	}
	if csqtt := known[singBoxCSQTTTag]; csqtt != nil {
		if csqtt["type"] != "socks" { return input, fmt.Errorf("invalid CSQTT outbound type") }
		for key := range csqtt {
			if key != "type" && key != "tag" && key != "server" && key != "server_port" && key != "version" {
				return input, fmt.Errorf("unsupported CSQTT outbound field %q; refusing lossy panel import", key)
			}
		}
		if version, exists := csqtt["version"]; exists && version != "5" {
			return input, fmt.Errorf("CSQTT outbound version must be SOCKS5")
		}
		csqttHost, _ := csqtt["server"].(string)
		csqttPort, ok := csqtt["server_port"].(float64)
		if !ok || csqttPort < 1 || csqttPort > 65535 || csqttPort != float64(int(csqttPort)) {
			return input, fmt.Errorf("invalid CSQTT SOCKS endpoint")
		}
		csqttIP := net.ParseIP(strings.TrimSpace(csqttHost))
		if csqttIP == nil || !csqttIP.IsLoopback() {
			return input, fmt.Errorf("CSQTT SOCKS outbound must use the local loopback listener")
		}
		if input.ListenAddress == net.JoinHostPort(csqttIP.String(), strconv.Itoa(int(csqttPort))) {
			return input, fmt.Errorf("sing-box inbound and CSQTT SOCKS outbound must use different endpoints")
		}
		input.CSQTTAddress = net.JoinHostPort(csqttIP.String(), strconv.Itoa(int(csqttPort)))
		input.CSQTTEnabled = true
		input.CSQTTRule.Enabled = true
	}
	for tag, entry := range known {
		if !strings.HasPrefix(tag, "socks-") { continue }
		if entry["type"] != "socks" {
			return input, fmt.Errorf("invalid SOCKS5 outbound type for %q", tag)
		}
		for key := range entry {
			if key != "type" && key != "tag" && key != "server" && key != "server_port" && key != "version" && key != "username" && key != "password" {
				return input, fmt.Errorf("unsupported SOCKS5 outbound field %q in %q; refusing lossy panel import", key, tag)
			}
		}
		if version, exists := entry["version"]; exists && version != "5" {
			return input, fmt.Errorf("SOCKS5 outbound %q version must be 5", tag)
		}
		host, _ := entry["server"].(string)
		port, ok := entry["server_port"].(float64)
		if !ok || port < 1 || port > 65535 || port != float64(int(port)) {
			return input, fmt.Errorf("invalid SOCKS5 outbound %q", tag)
		}
		id := strings.TrimPrefix(tag, "socks-")
		if !singBoxIDPattern.MatchString(id) {
			return input, fmt.Errorf("invalid SOCKS5 outbound ID in tag %q", tag)
		}
		if _, _, err := singBoxOutboundHostPort(host, strconv.Itoa(int(port)), "SOCKS5 outbound "+tag); err != nil {
			return input, err
		}
		username, hasUsername := entry["username"].(string)
		password, hasPassword := entry["password"].(string)
		if _, exists := entry["username"]; exists && !hasUsername {
			return input, fmt.Errorf("SOCKS5 outbound %q username must be a string", tag)
		}
		if _, exists := entry["password"]; exists && !hasPassword {
			return input, fmt.Errorf("SOCKS5 outbound %q password must be a string", tag)
		}
		if (username == "") != (password == "") {
			return input, fmt.Errorf("SOCKS5 outbound %q must provide both username and password", tag)
		}
		input.SOCKS5 = append(input.SOCKS5, singBoxSOCKS5Outbound{ID:id, Server:strings.TrimSpace(host), ServerPort:int(port), Username:username, Password:password, Enabled:true})
	}
	// Go map iteration is intentionally randomized. Keep the panel model stable
	// across loads so outbound ordering does not flicker between API responses.
	sort.Slice(input.SOCKS5, func(i, j int) bool { return input.SOCKS5[i].ID < input.SOCKS5[j].ID })
	route, ok := raw["route"].(map[string]any)
	if !ok {
		return input, fmt.Errorf("invalid sing-box route configuration")
	}
	finalOutbound, ok := route["final"].(string)
	if !ok || strings.TrimSpace(finalOutbound) == "" {
		return input, fmt.Errorf("sing-box route must define an explicit default outbound")
	}
	if known[finalOutbound] == nil {
		return input, fmt.Errorf("sing-box default outbound %q is not configured", finalOutbound)
	}
	input.FinalOutbound = finalOutbound
	for key := range route {
		if key != "final" && key != "rule_set" && key != "rules" {
			return input, fmt.Errorf("unsupported sing-box route field %q; refusing lossy panel import", key)
		}
	}
	rawRuleSets := []any{}
	if rawSets, exists := route["rule_set"]; exists {
		var valid bool
		rawRuleSets, valid = rawSets.([]any)
		if !valid { return input, fmt.Errorf("sing-box route Rule Sets must be an array; refusing lossy panel import") }
	}
	for _, rawSet := range rawRuleSets {
		set, ok := rawSet.(map[string]any)
		if !ok { return input, fmt.Errorf("invalid sing-box Rule Set entry; refusing lossy panel import") }
		item := singBoxRuleSet{}
		var valid bool
		if value, exists := set["type"]; exists { item.Type, valid = value.(string); if !valid { return input, fmt.Errorf("Rule Set type must be a string") } }
		if value, exists := set["tag"]; exists { item.Tag, valid = value.(string); if !valid { return input, fmt.Errorf("Rule Set tag must be a string") } }
		if value, exists := set["format"]; exists { item.Format, valid = value.(string); if !valid { return input, fmt.Errorf("Rule Set format must be a string") } }
		if value, exists := set["path"]; exists { item.Path, valid = value.(string); if !valid { return input, fmt.Errorf("Rule Set path must be a string") } }
		if value, exists := set["url"]; exists { item.URL, valid = value.(string); if !valid { return input, fmt.Errorf("Rule Set URL must be a string") } }
		if value, exists := set["update_interval"]; exists { item.UpdateInterval, valid = value.(string); if !valid { return input, fmt.Errorf("Rule Set update_interval must be a string") } }
		for key := range set {
			allowed := key == "type" || key == "tag" || key == "format"
			if item.Type == "local" { allowed = allowed || key == "path" }
			if item.Type == "remote" { allowed = allowed || key == "url" || key == "update_interval" }
			if !allowed { return input, fmt.Errorf("unsupported Rule Set field %q for type %q; refusing lossy panel import", key, item.Type) }
		}
		input.RuleSets = append(input.RuleSets, item)
	}
	rulesValue, hasRules := route["rules"]
	rules := []any{}
	if hasRules {
		var valid bool
		rules, valid = rulesValue.([]any)
		if !valid { return input, fmt.Errorf("sing-box route rules must be an array; refusing lossy panel import") }
	}
	input.RouteOrder = make([]string, 0, len(rules))
	seenNonDirectRoute := false
	readStrings := func(rule map[string]any, key string) ([]string, error) {
		raw, exists := rule[key]
		if !exists { return nil, nil }
		items, ok := raw.([]any)
		if !ok { return nil, fmt.Errorf("route matcher %q must be an array of strings", key) }
		values := make([]string, 0, len(items))
		for _, item := range items {
			value, ok := item.(string)
			if !ok { return nil, fmt.Errorf("route matcher %q contains a non-string value", key) }
			values = append(values, value)
		}
		return values, nil
	}
	validateInbound := func(rule map[string]any) error {
		inbounds, err := readStrings(rule, "inbound")
		if err != nil { return err }
		if _, exists := rule["inbound"]; exists && (len(inbounds) != 1 || inbounds[0] != singBoxInboundTag) {
			return fmt.Errorf("route references unsupported inbound; refusing lossy panel import")
		}
		return nil
	}
	for index, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok { return input, fmt.Errorf("sing-box route rule %d is not an object; refusing lossy panel import", index) }
		action, _ := rule["action"].(string)
		if action == "sniff" {
			if len(rule) != 2 { return input, fmt.Errorf("custom sniff action at route rule %d is not representable in the panel", index) }
			if err := validateInbound(rule); err != nil { return input, err }
			continue
		}
		if action != "route" { return input, fmt.Errorf("unsupported sing-box route action %q at rule %d; refusing lossy panel import", action, index) }
		if err := validateInbound(rule); err != nil { return input, err }
		outbound, _ := rule["outbound"].(string)
		domains, err := readStrings(rule, "domain"); if err != nil { return input, err }
		suffixes, err := readStrings(rule, "domain_suffix"); if err != nil { return input, err }
		keywords, err := readStrings(rule, "domain_keyword"); if err != nil { return input, err }
		regexes, err := readStrings(rule, "domain_regex"); if err != nil { return input, err }
		cidrs, err := readStrings(rule, "ip_cidr"); if err != nil { return input, err }
		ruleSets, err := readStrings(rule, "rule_set"); if err != nil { return input, err }
		if outbound == singBoxDirectTag {
			if seenNonDirectRoute { return input, fmt.Errorf("Direct route appears after another route; pinned Direct order cannot be imported without changing first-match semantics") }
			for key := range rule {
				switch key {
				case "inbound", "action", "outbound", "domain", "domain_suffix", "ip_cidr":
				default:
					return input, fmt.Errorf("Direct route uses unsupported matcher %q; refusing lossy panel import", key)
				}
			}
			if len(domains)+len(suffixes)+len(cidrs) == 0 { return input, fmt.Errorf("Direct route has no supported matchers; refusing lossy panel import") }
			input.DirectDomains=append(input.DirectDomains,domains...)
			input.DirectDomainSuffixes=append(input.DirectDomainSuffixes,suffixes...)
			input.DirectIPCIDRs=append(input.DirectIPCIDRs,cidrs...)
			continue
		}
		seenNonDirectRoute = true
		if outbound == "" || known[outbound] == nil { return input, fmt.Errorf("route references missing outbound %q", outbound) }
		for key := range rule {
			if _, managed := singBoxManagedRouteFields[key]; managed { continue }
			if _, allowed := singBoxAdvancedRouteFields[key]; !allowed {
				return input, fmt.Errorf("route uses unsupported matcher %q; refusing lossy panel import", key)
			}
		}
		id := fmt.Sprintf("loaded-%d", len(input.Rules)+1)
		input.Rules=append(input.Rules,singBoxRouteRule{ID:id,Domains:domains,DomainSuffixes:suffixes,DomainKeywords:keywords,DomainRegexes:regexes,IPCIDRs:cidrs,RuleSets:ruleSets,Advanced:singBoxAdvancedFieldsFromRule(rule),Outbound:outbound,Enabled:true})
		input.RouteOrder = append(input.RouteOrder, id)
	}
	if _, err := buildSingBoxConfig(input); err != nil {
		return input, fmt.Errorf("sing-box config cannot be represented safely by the panel: %w", err)
	}
	return input,nil
}
func (m *SingBoxManager) CopyLogs(w io.Writer) error {
	f, err := os.Open(m.logPath)
	if err != nil { return err }
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// validateRuleSetPaths confines local Rule Set files to the manager-owned directory.
// It rejects symlinks and non-regular files so a config cannot make sing-box read
// arbitrary host files through a crafted path or symlink.
func (m *SingBoxManager) validateRuleSetPaths(ruleSets []singBoxRuleSet) error {
	needsLocal := false
	for _, set := range ruleSets { if set.Type == "local" { needsLocal = true; break } }
	if !needsLocal { return nil }
	root := filepath.Join(filepath.Dir(m.configPath), "rule-sets")
	rootInfo, err := os.Lstat(root)
	if err != nil { return fmt.Errorf("inspect managed Rule Set directory: %w", err) }
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("managed Rule Set path is not a real directory") }
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil { return fmt.Errorf("resolve managed Rule Set directory: %w", err) }
	for _, set := range ruleSets {
		if set.Type != "local" { continue }
		if !filepath.IsAbs(set.Path) || filepath.Clean(set.Path) != set.Path { return fmt.Errorf("Rule Set %q path must be a clean absolute path", set.Tag) }
		info, err := os.Lstat(set.Path)
		if err != nil { return fmt.Errorf("inspect Rule Set %q: %w", set.Tag, err) }
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() { return fmt.Errorf("Rule Set %q must be a regular non-symlink file", set.Tag) }
		resolved, err := filepath.EvalSymlinks(set.Path)
		if err != nil { return fmt.Errorf("resolve Rule Set %q: %w", set.Tag, err) }
		rel, err := filepath.Rel(rootReal, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("Rule Set %q must remain inside the managed Rule Set directory", set.Tag)
		}
	}
	return nil
}

func (m *SingBoxManager) validateRuleSetUpload(ctx context.Context, stagedPath, format string) error {
	if m.binary == "" {
		return fmt.Errorf("sing-box binary is required")
	}
	timeout := m.checkTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch format {
	case "source":
		outputFile, err := os.CreateTemp(filepath.Dir(stagedPath), ".validate-*.srs")
		if err != nil {
			return fmt.Errorf("create Rule Set validation output: %w", err)
		}
		outputPath := outputFile.Name()
		if err := outputFile.Close(); err != nil {
			_ = os.Remove(outputPath)
			return fmt.Errorf("close Rule Set validation output: %w", err)
		}
		defer os.Remove(outputPath)

		cmd := exec.CommandContext(checkCtx, m.binary, "rule-set", "compile", "--output", outputPath, stagedPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if checkCtx.Err() != nil {
				return fmt.Errorf("Rule Set source validation timed out: %w", checkCtx.Err())
			}
			return fmt.Errorf("sing-box Rule Set source compile failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		info, err := os.Stat(outputPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("sing-box Rule Set source compile produced no binary output")
		}
		return nil

	case "binary":
		configFile, err := os.CreateTemp(filepath.Dir(stagedPath), ".validate-*.json")
		if err != nil {
			return fmt.Errorf("create Rule Set validation config: %w", err)
		}
		configPath := configFile.Name()
		defer os.Remove(configPath)

		config := map[string]any{
			"inbounds":  []any{},
			"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
			"route": map[string]any{
				"rule_set": []any{map[string]any{
					"type": "local", "tag": "upload-validation",
					"format": "binary", "path": stagedPath,
				}},
				"final": "direct",
			},
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			_ = configFile.Close()
			return fmt.Errorf("encode Rule Set validation config: %w", err)
		}
		if err := configFile.Chmod(0600); err != nil {
			_ = configFile.Close()
			return fmt.Errorf("secure Rule Set validation config: %w", err)
		}
		if _, err := configFile.Write(encoded); err != nil {
			_ = configFile.Close()
			return fmt.Errorf("write Rule Set validation config: %w", err)
		}
		if err := configFile.Sync(); err != nil {
			_ = configFile.Close()
			return fmt.Errorf("sync Rule Set validation config: %w", err)
		}
		if err := configFile.Close(); err != nil {
			return fmt.Errorf("close Rule Set validation config: %w", err)
		}

		cmd := exec.CommandContext(checkCtx, m.binary, "check", "-c", configPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if checkCtx.Err() != nil {
				return fmt.Errorf("Rule Set binary validation timed out: %w", checkCtx.Err())
			}
			return fmt.Errorf("sing-box Rule Set binary check failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	default:
		return fmt.Errorf("unsupported Rule Set format %q", format)
	}
}


func (m *SingBoxManager) ListRuleSets() ([]singBoxRuleSet, error) {
 byTag:=make(map[string]singBoxRuleSet)
 input,err:=m.LoadConfig()
 if err==nil { for _,set:=range input.RuleSets { byTag[set.Tag]=set } } else if !errors.Is(err, os.ErrNotExist) { return nil,fmt.Errorf("load Rule Set registry: %w",err) }
 dir:=filepath.Join(filepath.Dir(m.configPath),"rule-sets")
 entries,err:=os.ReadDir(dir)
 if err!=nil&&!errors.Is(err, os.ErrNotExist){return nil,fmt.Errorf("read managed Rule Set directory: %w",err)}
 if err==nil {
  info,e:=os.Lstat(dir);if e!=nil||!info.IsDir()||info.Mode()&os.ModeSymlink!=0{return nil,fmt.Errorf("managed Rule Set path is not a safe directory")}
  for _,entry:=range entries {
   name:=entry.Name();ext:=strings.ToLower(filepath.Ext(name));if ext!=".srs"&&ext!=".json"{continue}
   tag:=strings.TrimSuffix(name,filepath.Ext(name));if !singBoxIDPattern.MatchString(tag){continue}
   info,e:=os.Lstat(filepath.Join(dir,name));if e!=nil||info.Mode()&os.ModeSymlink!=0||!info.Mode().IsRegular(){continue}
   format:="binary";if ext==".json"{format="source"}
   if _,ok:=byTag[tag];!ok{byTag[tag]=singBoxRuleSet{Type:"local",Tag:tag,Format:format,Path:filepath.Join(dir,name)}}
  }
 }
 result:=make([]singBoxRuleSet,0,len(byTag));for _,set:=range byTag{result=append(result,set)}
 sort.Slice(result,func(i,j int)bool{return result[i].Tag<result[j].Tag})
 return result,nil
}

func singBoxValueReferencesRuleSet(value any, tag string) bool {
 switch item := value.(type) {
 case map[string]any:
  for key, child := range item {
   if key == "rule_set" {
    switch refs := child.(type) {
    case string:
     if refs == tag { return true }
    case []any:
     for _, ref := range refs { if text, ok := ref.(string); ok && text == tag { return true } }
    }
   }
   if singBoxValueReferencesRuleSet(child, tag) { return true }
  }
 case []any:
  for _, child := range item { if singBoxValueReferencesRuleSet(child, tag) { return true } }
 }
 return false
}

// Inspect persisted route JSON, including nested advanced/logical rule objects
// that the simplified panel model does not expose.
func singBoxConfigRouteReferencesRuleSet(configPath, tag string) (bool, error) {
 data, err := os.ReadFile(configPath)
 if errors.Is(err, os.ErrNotExist) { return false, nil }
 if err != nil { return false, err }
 var raw map[string]any
 if err := json.Unmarshal(data, &raw); err != nil { return false, err }
 route, _ := raw["route"].(map[string]any)
 return singBoxValueReferencesRuleSet(route["rules"], tag), nil
}

func (m *SingBoxManager) DeleteRuleSet(tag string) error {
 if !singBoxIDPattern.MatchString(tag){return fmt.Errorf("invalid Rule Set tag %q",tag)}
 m.mu.Lock();defer m.mu.Unlock()
 if m.cmd!=nil||m.starting||m.configuring{return fmt.Errorf("cannot delete Rule Set while sing-box is running, starting, or configuring")}
 input,err:=m.LoadConfig();if err!=nil&&!errors.Is(err, os.ErrNotExist){return fmt.Errorf("load sing-box config before deletion: %w",err)}
 if referenced, err := singBoxConfigRouteReferencesRuleSet(m.configPath, tag); err != nil {
  return fmt.Errorf("inspect sing-box Rule Set references: %w", err)
 } else if referenced {
  return fmt.Errorf("Rule Set %q is referenced by a route rule; remove the reference and save first", tag)
 }
 for _,set:=range input.RuleSets{if set.Tag==tag{return fmt.Errorf("Rule Set %q is still registered in sing-box config; remove it from the registry and save first",tag)}}
 dir:=filepath.Join(filepath.Dir(m.configPath),"rule-sets");info,err:=os.Lstat(dir);if errors.Is(err, os.ErrNotExist){return nil}
 if err!=nil||!info.IsDir()||info.Mode()&os.ModeSymlink!=0{return fmt.Errorf("managed Rule Set path is not a safe directory")}
 removed:=false
 for _,ext:=range []string{".srs",".json"}{path:=filepath.Join(dir,tag+ext);info,err:=os.Lstat(path);if errors.Is(err, os.ErrNotExist){continue};if err!=nil{return fmt.Errorf("inspect Rule Set %q: %w",tag,err)};if info.Mode()&os.ModeSymlink!=0||!info.Mode().IsRegular(){return fmt.Errorf("Rule Set %q is not a regular managed file",tag)};if err:=os.Remove(path);err!=nil{return fmt.Errorf("delete Rule Set %q: %w",tag,err)};removed=true}
 if removed{if d,err:=os.Open(dir);err==nil{_ = d.Sync();_ = d.Close()}}
 return nil
}

func (m *SingBoxManager) UploadRuleSet(tag, filename string, data []byte) (singBoxRuleSet, error) {
	var result singBoxRuleSet
	if !singBoxIDPattern.MatchString(tag) {
		return result, fmt.Errorf("invalid Rule Set tag %q", tag)
	}
	if len(data) == 0 || len(data) > 16<<20 {
		return result, fmt.Errorf("Rule Set file must be between 1 byte and 16 MiB")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	format := ""
	switch ext {
	case ".srs":
		format = "binary"
	case ".json":
		format = "source"
	default:
		return result, fmt.Errorf("Rule Set file extension must be .srs or .json")
	}
	if m.binary == "" || m.configPath == "" {
		return result, fmt.Errorf("sing-box binary and config path are required")
	}
	// Upload/activation must not race with Start, SaveConfig, or deletion.
	// Keep the manager in its configuring state for the complete staged
	// validation and atomic activation window.
	m.mu.Lock()
	if m.cmd != nil || m.starting || m.configuring {
		m.mu.Unlock()
		return result, fmt.Errorf("cannot upload Rule Set while sing-box is running, starting, or configuring")
	}
	m.configuring = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.configuring = false; m.mu.Unlock() }()

	// A tag identifies one registry entry. It must resolve to the canonical
	// manager-owned filename; extension-only matches could otherwise overwrite
	// an unrelated path while leaving the configured Rule Set unchanged.
	dir := filepath.Join(filepath.Dir(m.configPath), "rule-sets")
	activePath := filepath.Join(dir, tag+ext)
	if input, err := m.LoadConfig(); err == nil {
		for _, existing := range input.RuleSets {
			if existing.Tag != tag { continue }
			if existing.Type != "local" || existing.Format != format || filepath.Clean(existing.Path) != activePath {
				return result, fmt.Errorf("Rule Set tag %q is already registered with a different source, format, or managed path", tag)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("load sing-box config before upload: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return result, fmt.Errorf("create Rule Set directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("Rule Set directory is not a safe directory")
	}

	// Do not leave a stale sibling format for the same tag. The registry
	// intentionally presents one entry per tag, so two physical formats would
	// make its effective source ambiguous.
	otherExt := ".srs"
	if ext == ".srs" { otherExt = ".json" }
	otherPath := filepath.Join(dir, tag+otherExt)
	if otherInfo, otherErr := os.Lstat(otherPath); otherErr == nil {
		if otherInfo.Mode()&os.ModeSymlink != 0 || !otherInfo.Mode().IsRegular() {
			return result, fmt.Errorf("existing Rule Set sibling for tag %q is not a regular managed file", tag)
		}
		return result, fmt.Errorf("Rule Set tag %q has a stale sibling file in the other format; remove it before uploading", tag)
	} else if !errors.Is(otherErr, os.ErrNotExist) {
		return result, fmt.Errorf("inspect alternate Rule Set format for %q: %w", tag, otherErr)
	}
	if activeInfo, activeErr := os.Lstat(activePath); activeErr == nil {
		if activeInfo.Mode()&os.ModeSymlink != 0 || !activeInfo.Mode().IsRegular() {
			return result, fmt.Errorf("existing Rule Set %q is not a regular managed file", tag)
		}
	} else if !errors.Is(activeErr, os.ErrNotExist) {
		return result, fmt.Errorf("inspect existing Rule Set %q: %w", tag, activeErr)
	}
	staging, err := os.CreateTemp(dir, ".upload-*.tmp")
	if err != nil {
		return result, fmt.Errorf("create Rule Set staging file: %w", err)
	}
	stagingPath := staging.Name()
	defer os.Remove(stagingPath)
	if err := staging.Chmod(0600); err != nil {
		_ = staging.Close()
		return result, fmt.Errorf("secure Rule Set staging file: %w", err)
	}
	if _, err := staging.Write(data); err != nil {
		_ = staging.Close()
		return result, fmt.Errorf("write Rule Set staging file: %w", err)
	}
	if err := staging.Sync(); err != nil {
		_ = staging.Close()
		return result, fmt.Errorf("sync Rule Set staging file: %w", err)
	}
	if err := staging.Close(); err != nil {
		return result, fmt.Errorf("close Rule Set staging file: %w", err)
	}

	if err := m.validateRuleSetUpload(context.Background(), stagingPath, format); err != nil {
		return result, fmt.Errorf("refusing invalid Rule Set upload: %w", err)
	}
	if err := os.Rename(stagingPath, activePath); err != nil {
		return result, fmt.Errorf("activate Rule Set upload: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return singBoxRuleSet{Type: "local", Tag: tag, Format: format, Path: activePath}, nil
}

