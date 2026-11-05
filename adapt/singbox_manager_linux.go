//go:build linux

package main

import (
	"context"
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
	route, _ := raw["route"].(map[string]any)
	for _, rawSet := range anySlice(route["rule_set"]) {
		set, ok := rawSet.(map[string]any); if !ok { continue }
		item := singBoxRuleSet{}
		item.Type, _ = set["type"].(string); item.Tag, _ = set["tag"].(string)
		item.Format, _ = set["format"].(string); item.Path, _ = set["path"].(string)
		item.URL, _ = set["url"].(string); item.UpdateInterval, _ = set["update_interval"].(string)
		input.RuleSets = append(input.RuleSets, item)
	}
	rules, _ := route["rules"].([]any)
	for _, item := range rules {
		rule, ok := item.(map[string]any); if !ok || rule["action"] != "route" { continue }
		outbound, _ := rule["outbound"].(string)
		domains, suffixes := stringSlice(rule["domain"]), stringSlice(rule["domain_suffix"])
		keywords, regexes, cidrs := stringSlice(rule["domain_keyword"]), stringSlice(rule["domain_regex"]), stringSlice(rule["ip_cidr"])
		ruleSets := stringSlice(rule["rule_set"])
		if outbound == singBoxDirectTag {
			input.DirectDomains=append(input.DirectDomains,domains...)
			input.DirectDomainSuffixes=append(input.DirectDomainSuffixes,suffixes...)
			input.DirectIPCIDRs=append(input.DirectIPCIDRs,cidrs...)
			continue
		}
		if outbound == "" || known[outbound] == nil { return input, fmt.Errorf("route references missing outbound %q", outbound) }
		input.Rules=append(input.Rules,singBoxRouteRule{ID:fmt.Sprintf("loaded-%d",len(input.Rules)+1),Domains:domains,DomainSuffixes:suffixes,DomainKeywords:keywords,DomainRegexes:regexes,IPCIDRs:cidrs,RuleSets:ruleSets,Outbound:outbound,Enabled:true})
	}
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

