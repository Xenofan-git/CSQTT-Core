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

type singBoxPanelState struct {
	Version int `json:"version"`
	ConfigSHA256 string `json:"config_sha256"`
	Config singBoxConfigInput `json:"config"`
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
	if m.configPath == "" || m.binary == "" { return fmt.Errorf("sing-box binary and config path are required") }
	if err := m.normalizeRuleSetCachePath(&input); err != nil { return err }
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

	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0700); err != nil { return fmt.Errorf("create sing-box config directory: %w", err) }
	if err := m.validateRuleSetCachePath(input); err != nil { return fmt.Errorf("unsafe Rule Set cache path: %w", err) }
	f, err := os.CreateTemp(dir, ".sing-box-config-*.json")
	if err != nil { return fmt.Errorf("create temporary sing-box config: %w", err) }
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil { _ = f.Close(); return err }
	if _, err := f.Write(encoded); err != nil { _ = f.Close(); return fmt.Errorf("write temporary sing-box config: %w", err) }
	if err := f.Sync(); err != nil { _ = f.Close(); return fmt.Errorf("sync temporary sing-box config: %w", err) }
	if err := f.Close(); err != nil { return fmt.Errorf("close temporary sing-box config: %w", err) }
	stateHash := sha256.Sum256(encoded)
	stateData, err := json.MarshalIndent(singBoxPanelState{Version: 1, ConfigSHA256: hex.EncodeToString(stateHash[:]), Config: input}, "", "  ")
	if err != nil { return fmt.Errorf("encode sing-box panel state: %w", err) }
	stateFile, err := os.CreateTemp(dir, ".sing-box-panel-state-*.json")
	if err != nil { return fmt.Errorf("create temporary sing-box panel state: %w", err) }
	stateTmp := stateFile.Name()
	defer os.Remove(stateTmp)
	if err := stateFile.Chmod(0600); err != nil { _ = stateFile.Close(); return err }
	if _, err := stateFile.Write(append(stateData, '\n')); err != nil { _ = stateFile.Close(); return fmt.Errorf("write temporary sing-box panel state: %w", err) }
	if err := stateFile.Sync(); err != nil { _ = stateFile.Close(); return fmt.Errorf("sync temporary sing-box panel state: %w", err) }
	if err := stateFile.Close(); err != nil { return fmt.Errorf("close temporary sing-box panel state: %w", err) }
	ctx, cancel := context.WithTimeout(context.Background(), m.checkTimeout)
	defer cancel()
	if err := m.checkConfigPath(ctx, tmp); err != nil { return fmt.Errorf("refusing invalid sing-box config: %w", err) }
	m.mu.Lock()
	if m.cmd != nil || m.starting { m.mu.Unlock(); return fmt.Errorf("sing-box started while config was being validated") }
	if err := os.Rename(tmp, m.configPath); err != nil { m.mu.Unlock(); return fmt.Errorf("activate sing-box config: %w", err) }
	if err := os.Rename(stateTmp, m.panelStatePath()); err != nil {
		m.lastError = "config saved but panel state activation failed: " + err.Error()
		m.mu.Unlock()
		return fmt.Errorf("activate sing-box panel state: %w", err)
	}
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

	input, loadErr := m.LoadConfig()
	if loadErr != nil { return fail(loadErr) }
	// Revalidate persisted local Rule Set paths at startup as configs may be
	// edited outside SaveConfig after they were originally written.
	if err := m.validateRuleSetPaths(input.RuleSets); err != nil {
		return fail(fmt.Errorf("unsafe Rule Set path: %w", err))
	}
	if err := m.validateRuleSetCachePath(input); err != nil {
		return fail(fmt.Errorf("unsafe Rule Set cache path: %w", err))
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


func (m *SingBoxManager) panelStatePath() string { return m.configPath + ".panel.json" }

func (m *SingBoxManager) LoadConfig() (singBoxConfigInput, error) {
	var input singBoxConfigInput
	data, err := os.ReadFile(m.configPath)
	if err != nil { return input, fmt.Errorf("read sing-box config: %w", err) }
	if stateData, stateErr := os.ReadFile(m.panelStatePath()); stateErr == nil {
		var state singBoxPanelState
		if err := json.Unmarshal(stateData, &state); err != nil { return input, fmt.Errorf("decode sing-box panel state: %w", err) }
		actualHash := sha256.Sum256(data)
		if state.Version != 1 || state.ConfigSHA256 != hex.EncodeToString(actualHash[:]) {
			return input, fmt.Errorf("sing-box panel state does not match the active config; refusing to discard disabled route and outbound settings")
		}
		expected, err := buildSingBoxConfig(state.Config)
		if err != nil { return input, fmt.Errorf("validate sing-box panel state: %w", err) }
		if string(expected) != string(data) { return input, fmt.Errorf("sing-box panel state and active config diverge; refusing to discard panel settings") }
		return state.Config, nil
	} else if errors.Is(stateErr, os.ErrNotExist) {
		var marker struct { Outbounds []struct { Tag string `json:"tag"` } `json:"outbounds"` }
		if err := json.Unmarshal(data, &marker); err != nil { return input, fmt.Errorf("decode sing-box config: %w", err) }
		for _, outbound := range marker.Outbounds {
			if outbound.Tag == singBoxDirectBypassTag || outbound.Tag == singBoxCSQTTDisabledTag {
				return input, fmt.Errorf("sing-box panel state is missing for a managed config; refusing to discard disabled settings")
			}
		}

	} else {
		return input, fmt.Errorf("read sing-box panel state: %w", stateErr)
	}
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
	if dns, ok := raw["dns"].(map[string]any); ok {
		if dns["reverse_mapping"] != true {
			return input, fmt.Errorf("unsupported sing-box DNS config: reverse_mapping must be enabled for domain routing")
		}
		input.DNSStrategy, _ = dns["strategy"].(string)
		if dns["final"] != singBoxDNSLocalTag {
			return input, fmt.Errorf("unsupported sing-box DNS config: expected final DNS server %q", singBoxDNSLocalTag)
		}
		servers := anySlice(dns["servers"])
		if len(servers) != 1 { return input, fmt.Errorf("unsupported sing-box DNS config: expected exactly one managed DNS server") }
		server, ok := servers[0].(map[string]any)
		if !ok || server["tag"] != singBoxDNSLocalTag { return input, fmt.Errorf("unsupported sing-box DNS server entry") }
		switch server["type"] {
		case "local":
			input.DNSMode = "local"
		case "udp":
			input.DNSMode = "udp"
			input.DNSServer, _ = server["server"].(string)
			serverPort, ok := server["server_port"].(float64)
			if !ok || serverPort < 1 || serverPort > 65535 { return input, fmt.Errorf("invalid persisted UDP DNS server port") }
			input.DNSPort = int(serverPort)
			if server["detour"] != singBoxDirectTag { return input, fmt.Errorf("unsupported UDP DNS detour; expected Direct") }
		default:
			return input, fmt.Errorf("unsupported sing-box DNS server type %v", server["type"])
		}
	} else {
		input.DNSMode = "local"
		input.DNSStrategy = "prefer_ipv4"
	}
	outbounds, _ := raw["outbounds"].([]any)
	known := make(map[string]map[string]any)
	for _, item := range outbounds {
		entry, ok := item.(map[string]any); if !ok { continue }
		tag, _ := entry["tag"].(string); known[tag] = entry
	}
	csqtt := known[singBoxCSQTTTag]
	if csqtt == nil {
		if disabled := known[singBoxCSQTTDisabledTag]; disabled != nil {
			csqtt = disabled
			input.CSQTTExplicitlyDisabled = true
		}
	}
	if csqtt != nil {
		if csqtt["type"] != "socks" { return input, fmt.Errorf("invalid CSQTT outbound type") }
		csqttHost, _ := csqtt["server"].(string)
		csqttPort, ok := csqtt["server_port"].(float64)
		if csqttHost == "" || !ok || csqttPort < 1 || csqttPort > 65535 { return input, fmt.Errorf("invalid CSQTT SOCKS endpoint") }
		input.CSQTTAddress = net.JoinHostPort(csqttHost, strconv.Itoa(int(csqttPort)))
		// Endpoint configuration is independent of whether the Core process is currently connected.
		input.CSQTTEnabled = input.CSQTTAddress != ""
	}
	for tag, entry := range known {
		if !strings.HasPrefix(tag, "socks-") { continue }
		host, _ := entry["server"].(string); port, ok := entry["server_port"].(float64)
		if !ok || port < 1 || port > 65535 { return input, fmt.Errorf("invalid SOCKS5 outbound %q", tag) }
		username, _ := entry["username"].(string); password, _ := entry["password"].(string)
		input.SOCKS5 = append(input.SOCKS5, singBoxSOCKS5Outbound{ID:strings.TrimPrefix(tag,"socks-"), Server:host, ServerPort:int(port), Username:username, Password:password, Enabled:true})
	}
	route, _ := raw["route"].(map[string]any)
	input.FinalOutbound, _ = route["final"].(string)
	// Older generated configs used the ordinary "direct" outbound for the
	// separate Direct-exclusions list. New configs carry a dedicated internal
	// direct-bypass outbound, allowing explicit Route -> DIRECT rules to remain
	// in the ordered queue without being folded into the Direct tab.
	legacyDirectExclusions := known[singBoxDirectBypassTag] == nil
	for _, rawSet := range anySlice(route["rule_set"]) {
		set, ok := rawSet.(map[string]any); if !ok { continue }
		item := singBoxRuleSet{}
		item.Type, _ = set["type"].(string); item.Tag, _ = set["tag"].(string)
		item.Format, _ = set["format"].(string); item.Path, _ = set["path"].(string)
		item.URL, _ = set["url"].(string); item.UpdateInterval, _ = set["update_interval"].(string)
		input.RuleSets = append(input.RuleSets, item)
	}
	rules, _ := route["rules"].([]any)
	directSystemSeen, csqttSystemSeen := false, false
	for _, item := range rules {
		rule, ok := item.(map[string]any); if !ok || rule["action"] != "route" { continue }
		outbound, _ := rule["outbound"].(string)
		domains, suffixes := stringSlice(rule["domain"]), stringSlice(rule["domain_suffix"])
		keywords, regexes, cidrs := stringSlice(rule["domain_keyword"]), stringSlice(rule["domain_regex"]), stringSlice(rule["ip_cidr"])
		ruleSets := stringSlice(rule["rule_set"])
		advanced := singBoxAdvancedFieldsFromRule(rule)
		if outbound == singBoxDirectBypassTag || (legacyDirectExclusions && outbound == singBoxDirectTag) {
			if len(keywords)+len(regexes)+len(ruleSets)+len(advanced) > 0 { return input, fmt.Errorf("Direct exclusions contain unsupported advanced matchers; refusing to discard them") }
			input.DirectDomains=append(input.DirectDomains,domains...)
			input.DirectDomainSuffixes=append(input.DirectDomainSuffixes,suffixes...)
			input.DirectIPCIDRs=append(input.DirectIPCIDRs,cidrs...)
			continue
		}
		if outbound == "" || known[outbound] == nil { return input, fmt.Errorf("route references missing outbound %q", outbound) }
		matcherCount := len(domains)+len(suffixes)+len(keywords)+len(regexes)+len(cidrs)+len(ruleSets)+len(advanced)
		id := fmt.Sprintf("loaded-%d", len(input.Rules)+1)
		enabled := true
		if outbound == singBoxDirectTag && !directSystemSeen {
			id = singBoxDirectRuleID
			directSystemSeen = true
		}
		if outbound == singBoxCSQTTTag && matcherCount == 0 && !csqttSystemSeen {
			// The legacy system CSQTT rule was a catch-all. Migrate it disabled;
			// matcher-based CSQTT rules with a sidecar remain fully preserved.
			id = singBoxCSQTTRuleID
			csqttSystemSeen = true
			enabled = false
		}
		input.Rules=append(input.Rules,singBoxRouteRule{ID:id,Domains:domains,DomainSuffixes:suffixes,DomainKeywords:keywords,DomainRegexes:regexes,IPCIDRs:cidrs,RuleSets:ruleSets,Advanced:advanced,Outbound:outbound,Enabled:enabled})
	}
	// Migrate the former implicit CSQTT route.final fallback into an explicit
	// catch-all card at the end of the ordered queue. Unmatched traffic then
	// falls back to DIRECT instead of silently selecting CSQTT.
	if input.FinalOutbound == singBoxCSQTTTag && !csqttSystemSeen {
		// Legacy route.final=csqtt was an implicit catch-all. Do not recreate it
		// as an unconditional rule: migrate it to a disabled, editable CSQTT card.
		input.Rules = append(input.Rules, singBoxRouteRule{ID:singBoxCSQTTRuleID, Outbound:singBoxCSQTTTag, Enabled:false})
		csqttSystemSeen = true
	}
	if input.FinalOutbound == singBoxCSQTTTag { input.FinalOutbound = singBoxDirectTag }
	return input,nil
}
func anySlice(value any) []any { items, _ := value.([]any); return items }

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

// validateRuleSetPaths confines local Rule Set files to the manager-owned directory.
// It rejects symlinks and non-regular files so a config cannot make sing-box read
// arbitrary host files through a crafted path or symlink.
func (m *SingBoxManager) normalizeRuleSetCachePath(input *singBoxConfigInput) error {
	if input == nil { return fmt.Errorf("sing-box config is required") }
	needsCache := false
	for _, set := range input.RuleSets { if set.Type == "remote" { needsCache = true; break } }
	if !needsCache { input.RuleSetCachePath = ""; return nil }
	if m.configPath == "" { return fmt.Errorf("sing-box config path is required for remote Rule Set cache") }
	expected := filepath.Join(filepath.Dir(m.configPath), "sing-box-cache.db")
	if input.RuleSetCachePath != "" && filepath.Clean(input.RuleSetCachePath) != expected {
		return fmt.Errorf("remote Rule Set cache path must remain inside the sing-box manager directory")
	}
	input.RuleSetCachePath = expected
	return nil
}

func (m *SingBoxManager) validateRuleSetCachePath(input singBoxConfigInput) error {
	needsCache := false
	for _, set := range input.RuleSets { if set.Type == "remote" { needsCache = true; break } }
	if !needsCache {
		if input.RuleSetCachePath != "" { return fmt.Errorf("unexpected Rule Set cache path without remote Rule Sets") }
		return nil
	}
	// Legacy configs created before persistent Rule Set caching may still
	// start; the next SaveConfig migrates them to the manager-owned cache.
	if input.RuleSetCachePath == "" { return nil }
	expected := filepath.Join(filepath.Dir(m.configPath), "sing-box-cache.db")
	if input.RuleSetCachePath != expected || !filepath.IsAbs(input.RuleSetCachePath) || filepath.Clean(input.RuleSetCachePath) != input.RuleSetCachePath {
		return fmt.Errorf("remote Rule Set cache path must be the manager-owned cache file")
	}
	if dirInfo, err := os.Lstat(filepath.Dir(m.configPath)); err == nil {
		if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("sing-box manager directory is not a safe directory") }
	} else if !errors.Is(err, os.ErrNotExist) { return fmt.Errorf("inspect sing-box manager directory: %w", err) }
	if cacheInfo, err := os.Lstat(input.RuleSetCachePath); err == nil {
		if cacheInfo.Mode()&os.ModeSymlink != 0 || !cacheInfo.Mode().IsRegular() { return fmt.Errorf("remote Rule Set cache must be a regular non-symlink file") }
	} else if !errors.Is(err, os.ErrNotExist) { return fmt.Errorf("inspect remote Rule Set cache: %w", err) }
	return nil
}

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

