//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	singBoxInboundTag = "csqtt-route-in"
	singBoxDirectTag        = "direct"
	singBoxDirectBypassTag  = "direct-bypass"
	singBoxCSQTTTag         = "csqtt"
	singBoxCSQTTDisabledTag = "csqtt-disabled"
	singBoxDirectRuleID     = "system-direct"
	singBoxCSQTTRuleID      = "system-csqtt"
	singBoxDNSLocalTag      = "csqtt-dns-local"
	singBoxRuleSetHTTPClientTag = "csqtt-ruleset-http"
)

type singBoxSOCKS5Outbound struct {
	ID         string
	Server     string
	ServerPort int
	Username   string
	Password   string
	Enabled    bool
}

type singBoxRuleSet struct {
	Type           string
	Tag            string
	Format         string
	Path           string
	URL            string
	UpdateInterval string
}

type singBoxRouteRule struct {
	ID             string
	Advanced       map[string]json.RawMessage
	RuleSets       []string
	Domains        []string
	DomainSuffixes []string
	DomainKeywords []string
	DomainRegexes  []string
	IPCIDRs        []string
	Outbound       string
	Enabled        bool
}

type singBoxConfigInput struct {
	ListenAddress string
	CSQTTAddress              string
	CSQTTEnabled              bool
	CSQTTExplicitlyDisabled   bool
	FinalOutbound             string
	DNSMode       string
	DNSServer     string
	DNSPort       int
	DNSStrategy   string
	DirectDomains        []string
	DirectDomainSuffixes []string
	DirectIPCIDRs        []string
	SOCKS5        []singBoxSOCKS5Outbound
	RuleSets      []singBoxRuleSet
	RuleSetCachePath string
	Rules         []singBoxRouteRule
}

type singBoxConfig struct {
	Log      map[string]any      `json:"log"`
	DNS      map[string]any      `json:"dns"`
	Inbounds []map[string]any    `json:"inbounds"`
	Outbounds []map[string]any   `json:"outbounds"`
	HTTPClients []map[string]any `json:"http_clients,omitempty"`
	Experimental map[string]any `json:"experimental,omitempty"`
	Route    map[string]any      `json:"route"`
}

// Only matcher fields are accepted here; routing control remains generated.
var singBoxAdvancedRouteFields = map[string]struct{}{
 "type": {}, "mode": {}, "rules": {}, "invert": {}, "network": {}, "auth_user": {}, "protocol": {}, "client": {},
 "source_ip_cidr": {}, "source_ip_is_private": {}, "ip_is_private": {}, "ip_version": {},
 "port": {}, "port_range": {}, "source_port": {}, "source_port_range": {},
 "process_name": {}, "process_path": {}, "process_path_regex": {}, "package_name": {},
 "user": {}, "user_id": {}, "wifi_ssid": {}, "wifi_bssid": {}, "network_type": {},
 "network_is_expensive": {}, "network_is_constrained": {}, "interface_address": {}, "preferred_by": {},
}
var singBoxManagedRouteFields = map[string]struct{}{ "inbound": {}, "action": {}, "outbound": {}, "domain": {}, "domain_suffix": {}, "domain_keyword": {}, "domain_regex": {}, "ip_cidr": {}, "rule_set": {}, }
func validateSingBoxAdvancedRouteFields(fields map[string]json.RawMessage) error {
 for key, raw := range fields {
  if _,ok:=singBoxAdvancedRouteFields[key]; !ok { return fmt.Errorf("advanced route field %q is not an allowed matcher",key) }
  if len(raw)==0 || !json.Valid(raw) { return fmt.Errorf("advanced route field %q contains invalid JSON",key) }
 }
 _, hasType := fields["type"]
 _, hasMode := fields["mode"]
 _, hasRules := fields["rules"]
 if hasType || hasMode || hasRules {
  var ruleType, mode string
  if !hasType || !hasMode || !hasRules { return fmt.Errorf("logical route matcher requires type, mode, and rules") }
  if err:=json.Unmarshal(fields["type"], &ruleType); err!=nil || ruleType!="logical" { return fmt.Errorf("advanced route type must be logical") }
  if err:=json.Unmarshal(fields["mode"], &mode); err!=nil || (mode!="and" && mode!="or") { return fmt.Errorf("logical route mode must be and or or") }
  var rules []json.RawMessage
  if err:=json.Unmarshal(fields["rules"], &rules); err!=nil || len(rules)==0 { return fmt.Errorf("logical route rules must be a non-empty array") }
 }
 return nil
}

// invert modifies a matcher; by itself it does not define one.
func singBoxAdvancedHasMatcher(fields map[string]json.RawMessage) bool {
	for key := range fields {
		if key != "invert" && key != "type" && key != "mode" {
			return true
		}
	}
	return false
}
func singBoxAdvancedFieldsFromRule(rule map[string]any) map[string]json.RawMessage {
 fields:=make(map[string]json.RawMessage); for key,value:=range rule { if _,managed:=singBoxManagedRouteFields[key]; managed {continue}; if _,allowed:=singBoxAdvancedRouteFields[key]; !allowed {continue}; encoded,err:=json.Marshal(value); if err==nil {fields[key]=encoded} }; if len(fields)==0{return nil}; return fields
}

var singBoxIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

func singBoxHostPort(value, field string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return "", 0, fmt.Errorf("%s must be an explicit host:port: %w", field, err)
	}
	if net.ParseIP(host) == nil {
		return "", 0, fmt.Errorf("%s host must be an IP address", field)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%s port must be between 1 and 65535", field)
	}
	return host, port, nil
}

func singBoxOutboundHostPort(host, portText, field string) (string, int, error) {
	host = strings.TrimSpace(host)
	if net.ParseIP(host) == nil {
		normalized, err := normalizeSingBoxDomain(host)
		if err != nil {
			return "", 0, fmt.Errorf("%s host: %w", field, err)
		}
		host = normalized
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%s port must be between 1 and 65535", field)
	}
	return host, port, nil
}

func normalizeSingBoxDomain(value string) (string, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, " /:*?#[]") {
		return "", fmt.Errorf("invalid domain %q", value)
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain %q", value)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", fmt.Errorf("invalid domain %q", value)
			}
		}
	}
	return domain, nil
}

func normalizeSingBoxSuffix(value string) (string, error) {
	suffix := strings.TrimPrefix(strings.TrimSpace(value), ".")
	domain, err := normalizeSingBoxDomain(suffix)
	if err != nil {
		return "", err
	}
	return "." + domain, nil
}

func normalizeSingBoxDomains(values []string, suffix bool) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		var normalized string
		var err error
		if suffix {
			normalized, err = normalizeSingBoxSuffix(value)
		} else {
			normalized, err = normalizeSingBoxDomain(value)
		}
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeSingBoxKeywords(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" || len(value) > 253 || strings.ContainsAny(value, " /:*?#[]") {
			return nil, fmt.Errorf("invalid domain keyword %q", raw)
		}
		if _, ok := seen[value]; ok { continue }
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func normalizeSingBoxRegexes(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || len(value) > 512 { return nil, fmt.Errorf("invalid domain regex %q", raw) }
		if _, err := regexp.Compile(value); err != nil { return nil, fmt.Errorf("invalid domain regex %q: %w", raw, err) }
		if _, ok := seen[value]; ok { continue }
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func normalizeSingBoxCIDRs(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil { return nil, fmt.Errorf("invalid IP/CIDR %q: %w", raw, err) }
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		normalized := prefix.Masked().String()
		if _, ok := seen[normalized]; ok { continue }
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func makeSingBoxRouteRule(domains, suffixes, keywords, regexes, cidrs []string, outbound string) map[string]any {
	rule := map[string]any{
		"inbound": []string{singBoxInboundTag},
		"action": "route",
		"outbound": outbound,
	}
	if len(domains) > 0 {
		rule["domain"] = domains
	}
	if len(suffixes) > 0 {
		rule["domain_suffix"] = suffixes
	}
	if len(keywords) > 0 {
		rule["domain_keyword"] = keywords
	}
	if len(regexes) > 0 {
		rule["domain_regex"] = regexes
	}
	if len(cidrs) > 0 {
		rule["ip_cidr"] = cidrs
	}
	return rule
}

// buildSingBoxConfig creates an explicit SOCKS entrypoint and ordered route rules.
// It deliberately creates no TUN inbound, auto_route, auto_redirect, or system capture.
// Direct exclusions and ordered route rules are evaluated first; unmatched traffic uses the explicit route.final (DIRECT by default).
func buildSingBoxConfig(input singBoxConfigInput) ([]byte, error) {
	listenHost, listenPort, err := singBoxHostPort(input.ListenAddress, "listen address")
	if err != nil {
		return nil, err
	}
	listenIP := net.ParseIP(listenHost)
	if listenIP == nil || listenIP.IsUnspecified() || listenIP.IsMulticast() || listenIP.IsLinkLocalUnicast() || (!listenIP.IsLoopback() && !listenIP.IsPrivate()) {
		return nil, fmt.Errorf("listen address must be loopback or a specific private LAN address; wildcard/public binds are forbidden")
	}
	outbounds := []map[string]any{
		{"type": "direct", "tag": singBoxDirectTag},
		{"type": "direct", "tag": singBoxDirectBypassTag},
	}
	knownOutbounds := map[string]struct{}{singBoxDirectTag: {}, singBoxDirectBypassTag: {}}
	allOutbounds := map[string]struct{}{singBoxDirectTag: {}, singBoxDirectBypassTag: {}, singBoxCSQTTTag: {}}
	csqttAddress := strings.TrimSpace(input.CSQTTAddress)
	csqttEnabled := input.CSQTTEnabled || (csqttAddress != "" && !input.CSQTTExplicitlyDisabled)
	if input.CSQTTExplicitlyDisabled { csqttEnabled = false }
	if csqttEnabled && csqttAddress == "" { return nil, fmt.Errorf("CSQTT is enabled but its SOCKS endpoint is empty") }
	if csqttAddress != "" {
		csqttHost, csqttPort, err := singBoxHostPort(csqttAddress, "CSQTT SOCKS address")
		if err != nil { return nil, err }
		csqttIP := net.ParseIP(csqttHost)
		if csqttIP == nil || !csqttIP.IsLoopback() { return nil, fmt.Errorf("CSQTT SOCKS outbound must use the local loopback listener") }
		if listenHost == csqttHost && listenPort == csqttPort { return nil, fmt.Errorf("sing-box inbound and CSQTT SOCKS outbound must use different endpoints") }
		tag := singBoxCSQTTDisabledTag
		if csqttEnabled { tag = singBoxCSQTTTag; knownOutbounds[singBoxCSQTTTag] = struct{}{} }
		outbounds = append(outbounds, map[string]any{"type":"socks","tag":tag,"server":csqttHost,"server_port":csqttPort,"version":"5"})
	}

	directDomains, err := normalizeSingBoxDomains(input.DirectDomains, false)
	if err != nil { return nil, fmt.Errorf("Direct domains: %w", err) }
	directSuffixes, err := normalizeSingBoxDomains(input.DirectDomainSuffixes, true)
	if err != nil { return nil, fmt.Errorf("Direct domain suffixes: %w", err) }
	directCIDRs, err := normalizeSingBoxCIDRs(input.DirectIPCIDRs)
	if err != nil { return nil, fmt.Errorf("Direct IP/CIDR: %w", err) }

	dnsMode := strings.ToLower(strings.TrimSpace(input.DNSMode))
	if dnsMode == "" { dnsMode = "local" }
	dnsStrategy := strings.TrimSpace(input.DNSStrategy)
	if dnsStrategy == "" { dnsStrategy = "prefer_ipv4" }
	switch dnsStrategy {
	case "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only":
	default:
		return nil, fmt.Errorf("DNS strategy must be prefer_ipv4, prefer_ipv6, ipv4_only, or ipv6_only")
	}
	dnsServer := map[string]any{"type": "local", "tag": singBoxDNSLocalTag, "prefer_go": true}
	switch dnsMode {
	case "local":
		if strings.TrimSpace(input.DNSServer) != "" || input.DNSPort != 0 {
			return nil, fmt.Errorf("local DNS mode does not accept a custom server or port")
		}
	case "udp":
		host := strings.TrimSpace(input.DNSServer)
		if net.ParseIP(host) == nil {
			return nil, fmt.Errorf("UDP DNS server must be an IP address to avoid resolver bootstrap loops")
		}
		port := input.DNSPort
		if port == 0 { port = 53 }
		if port < 1 || port > 65535 { return nil, fmt.Errorf("UDP DNS server port must be between 1 and 65535") }
		// Leave detour unset: sing-box uses its default outbound for DNS transport;
		// explicitly detouring to the empty DIRECT outbound is rejected by 1.14.4.
		dnsServer = map[string]any{"type": "udp", "tag": singBoxDNSLocalTag, "server": host, "server_port": port}
	default:
		return nil, fmt.Errorf("DNS mode must be local or udp")
	}
	dnsConfig := map[string]any{
		"servers": []map[string]any{dnsServer},
		"final": singBoxDNSLocalTag,
		"strategy": dnsStrategy,
		"reverse_mapping": true,
	}

	if len(input.SOCKS5) > 15 { return nil, fmt.Errorf("at most 15 user SOCKS5 outbounds are allowed") }
	for _, proxy := range input.SOCKS5 {
		if !singBoxIDPattern.MatchString(proxy.ID) {
			return nil, fmt.Errorf("invalid SOCKS5 outbound ID %q", proxy.ID)
		}
		tag := "socks-" + proxy.ID
		if _, exists := allOutbounds[tag]; exists {
			return nil, fmt.Errorf("duplicate outbound tag %q", tag)
		}
		allOutbounds[tag] = struct{}{}
		if !proxy.Enabled { continue }
		host, port, err := singBoxOutboundHostPort(proxy.Server, strconv.Itoa(proxy.ServerPort), "SOCKS5 outbound "+tag)
		if err != nil {
			return nil, err
		}
		if (proxy.Username == "") != (proxy.Password == "") {
			return nil, fmt.Errorf("SOCKS5 outbound %q must provide both username and password", tag)
		}
		outbound := map[string]any{
			"type": "socks", "tag": tag, "server": host, "server_port": port, "version": "5",
		}
		if proxy.Username != "" {
			outbound["username"] = proxy.Username
			outbound["password"] = proxy.Password
		}
		outbounds = append(outbounds, outbound)
		knownOutbounds[tag] = struct{}{}
	}

	// Rule Set definitions use the modern sing-box 1.14 schema. Local
	// paths must be absolute and remain in the managed rule-set directory;
	// remote sources are HTTPS-only to avoid cleartext downloads.
	ruleSetConfigs := make([]map[string]any, 0, len(input.RuleSets))
	knownRuleSets := make(map[string]struct{}, len(input.RuleSets))
	for _, set := range input.RuleSets {
		if !singBoxIDPattern.MatchString(set.Tag) { return nil, fmt.Errorf("invalid Rule Set tag %q", set.Tag) }
		if _, exists := knownRuleSets[set.Tag]; exists { return nil, fmt.Errorf("duplicate Rule Set tag %q", set.Tag) }
		knownRuleSets[set.Tag] = struct{}{}
		format := set.Format
		if format != "binary" && format != "source" { return nil, fmt.Errorf("Rule Set %q format must be binary or source", set.Tag) }
		entry := map[string]any{"tag": set.Tag, "format": format}
		switch set.Type {
		case "local":
			if !filepath.IsAbs(set.Path) || filepath.Clean(set.Path) != set.Path || filepath.Base(set.Path) == "." {
				return nil, fmt.Errorf("Rule Set %q local path must be an absolute managed file path", set.Tag)
			}
			if set.URL != "" { return nil, fmt.Errorf("local Rule Set %q cannot have a URL", set.Tag) }
			entry["type"], entry["path"] = "local", set.Path
		case "remote":
			u, err := url.ParseRequestURI(set.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return nil, fmt.Errorf("Rule Set %q requires an HTTPS URL without embedded credentials", set.Tag)
			}
			if set.Path != "" { return nil, fmt.Errorf("remote Rule Set %q cannot have a local path", set.Tag) }
			entry["type"], entry["url"] = "remote", set.URL
			if input.RuleSetCachePath != "" { entry["http_client"] = singBoxRuleSetHTTPClientTag }
			if set.UpdateInterval != "" { entry["update_interval"] = set.UpdateInterval }
		default:
			return nil, fmt.Errorf("Rule Set %q type must be local or remote", set.Tag)
		}
		ruleSetConfigs = append(ruleSetConfigs, entry)
	}

	rules := make([]map[string]any, 0, 3+len(input.Rules))
	// DNS sent through the SOCKS inbound is handed to sing-box's own DNS
	// module before sniffing or ordinary outbound selection. No system DNS
	// settings, listeners, routes, or firewall rules are modified.
	rules = append(rules, map[string]any{
		"inbound": []string{singBoxInboundTag},
		"network": []string{"tcp", "udp"},
		"port": []int{53},
		"action": "hijack-dns",
	})
	rules = append(rules, map[string]any{
		"inbound": []string{singBoxInboundTag},
		"action": "sniff",
	})
	if len(directDomains) > 0 || len(directSuffixes) > 0 || len(directCIDRs) > 0 {
		rules = append(rules, makeSingBoxRouteRule(directDomains, directSuffixes, nil, nil, directCIDRs, singBoxDirectBypassTag))
	}

	seenRuleIDs := make(map[string]struct{}, len(input.Rules))
	userRuleCount := 0
	systemDirectSeen, systemCSQTTSeen := false, false
	for index, item := range input.Rules {
		isDirectSystem := item.ID == singBoxDirectRuleID
		isCSQTTSystem := item.ID == singBoxCSQTTRuleID
		if !isDirectSystem && !isCSQTTSystem { userRuleCount++; if userRuleCount > 15 { return nil, fmt.Errorf("at most 15 user route rules are allowed") } }
		if !singBoxIDPattern.MatchString(item.ID) { return nil, fmt.Errorf("invalid route rule ID %q", item.ID) }
		if _, exists := seenRuleIDs[item.ID]; exists { return nil, fmt.Errorf("duplicate route rule ID %q", item.ID) }
		seenRuleIDs[item.ID] = struct{}{}
		if isDirectSystem {
			if systemDirectSeen || index != 0 { return nil, fmt.Errorf("the system DIRECT rule must appear exactly once at the top of the Route queue") }
			systemDirectSeen = true
			if item.Outbound != singBoxDirectTag { return nil, fmt.Errorf("the system DIRECT rule must use the DIRECT outbound") }
		}
		if isCSQTTSystem {
			if systemCSQTTSeen { return nil, fmt.Errorf("the system CSQTT rule may appear only once") }
			systemCSQTTSeen = true
			if item.Outbound != singBoxCSQTTTag { return nil, fmt.Errorf("the system CSQTT rule must use the CSQTT outbound") }
			if item.Enabled && !csqttEnabled { return nil, fmt.Errorf("the system CSQTT rule is enabled while the CSQTT connection is disabled") }
		}
		domains, err := normalizeSingBoxDomains(item.Domains, false)
		if err != nil { return nil, fmt.Errorf("route rule %q domains: %w", item.ID, err) }
		suffixes, err := normalizeSingBoxDomains(item.DomainSuffixes, true)
		if err != nil { return nil, fmt.Errorf("route rule %q suffixes: %w", item.ID, err) }
		keywords, err := normalizeSingBoxKeywords(item.DomainKeywords)
		if err != nil { return nil, fmt.Errorf("route rule %q keywords: %w", item.ID, err) }
		regexes, err := normalizeSingBoxRegexes(item.DomainRegexes)
		if err != nil { return nil, fmt.Errorf("route rule %q regexes: %w", item.ID, err) }
		cidrs, err := normalizeSingBoxCIDRs(item.IPCIDRs)
		if err != nil { return nil, fmt.Errorf("route rule %q IP/CIDR: %w", item.ID, err) }
		if err := validateSingBoxAdvancedRouteFields(item.Advanced); err != nil { return nil, fmt.Errorf("route rule %q advanced fields: %w", item.ID, err) }
		matcherCount := len(domains)+len(suffixes)+len(keywords)+len(regexes)+len(cidrs)+len(item.RuleSets)
		seenTags := make(map[string]struct{}, len(item.RuleSets))
		for _, tag := range item.RuleSets {
			if _, ok := knownRuleSets[tag]; !ok { return nil, fmt.Errorf("route rule %q references unknown Rule Set %q", item.ID, tag) }
			if _, ok := seenTags[tag]; ok { return nil, fmt.Errorf("route rule %q contains duplicate Rule Set %q", item.ID, tag) }
			seenTags[tag] = struct{}{}
		}
		if isCSQTTSystem {
			if matcherCount != 0 || singBoxAdvancedHasMatcher(item.Advanced) { return nil, fmt.Errorf("system CSQTT rule is a catch-all connection and cannot contain matchers") }
			if item.Enabled {
				if !csqttEnabled { return nil, fmt.Errorf("the system CSQTT rule is enabled while the CSQTT connection is disabled") }
				rules = append(rules, map[string]any{"inbound": []string{singBoxInboundTag}, "action": "route", "outbound": singBoxCSQTTTag})
			}
			continue
		}
		if item.Outbound == singBoxDirectBypassTag { return nil, fmt.Errorf("route rule %q cannot target the internal Direct-exclusions outbound", item.ID) }
		if _, exists := allOutbounds[item.Outbound]; !exists { return nil, fmt.Errorf("route rule %q references unknown outbound %q", item.ID, item.Outbound) }
		if isDirectSystem && item.Outbound != singBoxDirectTag { return nil, fmt.Errorf("system DIRECT rule must target DIRECT") }
		if !item.Enabled { continue }
		if matcherCount == 0 && !singBoxAdvancedHasMatcher(item.Advanced) { return nil, fmt.Errorf("route rule %q has no matchers", item.ID) }
		if _, exists := knownOutbounds[item.Outbound]; !exists { return nil, fmt.Errorf("route rule %q references unavailable outbound %q", item.ID, item.Outbound) }
		rule := makeSingBoxRouteRule(domains, suffixes, keywords, regexes, cidrs, item.Outbound)
		if item.Advanced["type"] != nil {
			var ruleType string
			if err := json.Unmarshal(item.Advanced["type"], &ruleType); err == nil && ruleType == "logical" { delete(rule, "inbound") }
		}
		if len(item.RuleSets) > 0 { rule["rule_set"] = item.RuleSets }
		for key, raw := range item.Advanced {
			if _, managed := singBoxManagedRouteFields[key]; managed { return nil, fmt.Errorf("route rule %q advanced field %q conflicts with managed routing", item.ID, key) }
			var value any
			if err := json.Unmarshal(raw, &value); err != nil { return nil, fmt.Errorf("route rule %q advanced field %q: %w", item.ID, key, err) }
			rule[key] = value
		}
		rules = append(rules, rule)
	}

	// Explicit fallback selection. Legacy configs keep CSQTT when configured.
	finalOutbound := strings.TrimSpace(input.FinalOutbound)
	if finalOutbound == "" { finalOutbound = singBoxDirectTag }
	if finalOutbound == "" { return nil, fmt.Errorf("select a default outbound for unmatched traffic") }
	if finalOutbound == singBoxDirectBypassTag {
		return nil, fmt.Errorf("default outbound cannot use the internal Direct-exclusions outbound")
	}
	if _, ok := knownOutbounds[finalOutbound]; !ok { return nil, fmt.Errorf("default outbound %q is not configured", finalOutbound) }

	routeConfig := map[string]any{"rules": rules, "rule_set": ruleSetConfigs, "final": finalOutbound}
	config := singBoxConfig{
		Log: map[string]any{"level": "info", "timestamp": true},
		DNS: dnsConfig,
		Inbounds: []map[string]any{{
			"type": "socks", "tag": singBoxInboundTag,
			"listen": listenHost, "listen_port": listenPort,
		}},
		Outbounds: outbounds,
		Route: routeConfig,
	}
	if input.RuleSetCachePath != "" {
		// sing-box 1.14 deprecated the implicit remote Rule Set HTTP client;
		// use an explicit shared client and persist remote Rule Set cache.
		config.HTTPClients = []map[string]any{{"tag": singBoxRuleSetHTTPClientTag}}
		config.Experimental = map[string]any{"cache_file": map[string]any{"enabled": true, "path": input.RuleSetCachePath}}
		routeConfig["default_http_client"] = singBoxRuleSetHTTPClientTag
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sing-box config: %w", err)
	}
	return append(encoded, '\n'), nil
}
