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
	singBoxDirectTag  = "direct"
	singBoxCSQTTTag   = "csqtt"
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
	CSQTTAddress  string
	DirectDomains        []string
	DirectDomainSuffixes []string
	DirectIPCIDRs        []string
	SOCKS5        []singBoxSOCKS5Outbound
	RuleSets      []singBoxRuleSet
	Rules         []singBoxRouteRule
}

type singBoxConfig struct {
	Log      map[string]any      `json:"log"`
	Inbounds []map[string]any    `json:"inbounds"`
	Outbounds []map[string]any   `json:"outbounds"`
	Route    map[string]any      `json:"route"`
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
// Direct exclusions and user rules are evaluated in order; unmatched traffic uses CSQTT.
func buildSingBoxConfig(input singBoxConfigInput) ([]byte, error) {
	listenHost, listenPort, err := singBoxHostPort(input.ListenAddress, "listen address")
	if err != nil {
		return nil, err
	}
	listenIP := net.ParseIP(listenHost)
	if listenIP == nil || listenIP.IsUnspecified() || listenIP.IsMulticast() || listenIP.IsLinkLocalUnicast() || (!listenIP.IsLoopback() && !listenIP.IsPrivate()) {
		return nil, fmt.Errorf("listen address must be loopback or a specific private LAN address; wildcard/public binds are forbidden")
	}
	csqttHost, csqttPort, err := singBoxHostPort(input.CSQTTAddress, "CSQTT SOCKS address")
	if err != nil {
		return nil, err
	}
	csqttIP := net.ParseIP(csqttHost)
	if csqttIP == nil || !csqttIP.IsLoopback() {
		return nil, fmt.Errorf("CSQTT SOCKS outbound must use the local loopback listener")
	}
	if listenHost == csqttHost && listenPort == csqttPort {
		return nil, fmt.Errorf("sing-box inbound and CSQTT SOCKS outbound must use different endpoints")
	}

	directDomains, err := normalizeSingBoxDomains(input.DirectDomains, false)
	if err != nil { return nil, fmt.Errorf("Direct domains: %w", err) }
	directSuffixes, err := normalizeSingBoxDomains(input.DirectDomainSuffixes, true)
	if err != nil { return nil, fmt.Errorf("Direct domain suffixes: %w", err) }
	directCIDRs, err := normalizeSingBoxCIDRs(input.DirectIPCIDRs)
	if err != nil { return nil, fmt.Errorf("Direct IP/CIDR: %w", err) }

	outbounds := []map[string]any{
		{"type": "direct", "tag": singBoxDirectTag},
		{"type": "socks", "tag": singBoxCSQTTTag, "server": csqttHost, "server_port": csqttPort, "version": "5"},
	}
	knownOutbounds := map[string]struct{}{
		singBoxDirectTag: {},
		singBoxCSQTTTag: {},
	}
	for _, proxy := range input.SOCKS5 {
		if !proxy.Enabled {
			continue
		}
		if !singBoxIDPattern.MatchString(proxy.ID) {
			return nil, fmt.Errorf("invalid SOCKS5 outbound ID %q", proxy.ID)
		}
		tag := "socks-" + proxy.ID
		if _, exists := knownOutbounds[tag]; exists {
			return nil, fmt.Errorf("duplicate outbound tag %q", tag)
		}
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
			if set.UpdateInterval != "" { entry["update_interval"] = set.UpdateInterval }
		default:
			return nil, fmt.Errorf("Rule Set %q type must be local or remote", set.Tag)
		}
		ruleSetConfigs = append(ruleSetConfigs, entry)
	}

	rules := make([]map[string]any, 0, 2+len(input.Rules))
	rules = append(rules, map[string]any{
		"inbound": []string{singBoxInboundTag},
		"action": "sniff",
	})
	if len(directDomains) > 0 || len(directSuffixes) > 0 || len(directCIDRs) > 0 {
		rules = append(rules, makeSingBoxRouteRule(directDomains, directSuffixes, nil, nil, directCIDRs, singBoxDirectTag))
	}

	seenRuleIDs := make(map[string]struct{}, len(input.Rules))
	for _, item := range input.Rules {
		if !item.Enabled {
			continue
		}
		if !singBoxIDPattern.MatchString(item.ID) {
			return nil, fmt.Errorf("invalid route rule ID %q", item.ID)
		}
		if _, exists := seenRuleIDs[item.ID]; exists {
			return nil, fmt.Errorf("duplicate route rule ID %q", item.ID)
		}
		seenRuleIDs[item.ID] = struct{}{}
		domains, err := normalizeSingBoxDomains(item.Domains, false)
		if err != nil {
			return nil, fmt.Errorf("route rule %q domains: %w", item.ID, err)
		}
		suffixes, err := normalizeSingBoxDomains(item.DomainSuffixes, true)
		if err != nil { return nil, fmt.Errorf("route rule %q suffixes: %w", item.ID, err) }
		keywords, err := normalizeSingBoxKeywords(item.DomainKeywords)
		if err != nil { return nil, fmt.Errorf("route rule %q keywords: %w", item.ID, err) }
		regexes, err := normalizeSingBoxRegexes(item.DomainRegexes)
		if err != nil { return nil, fmt.Errorf("route rule %q regexes: %w", item.ID, err) }
		cidrs, err := normalizeSingBoxCIDRs(item.IPCIDRs)
		if err != nil { return nil, fmt.Errorf("route rule %q IP/CIDR: %w", item.ID, err) }
		if len(domains)+len(suffixes)+len(keywords)+len(regexes)+len(cidrs)+len(item.RuleSets) == 0 {
			return nil, fmt.Errorf("route rule %q has no matchers", item.ID)
		}
		seenTags := make(map[string]struct{}, len(item.RuleSets))
		for _, tag := range item.RuleSets {
			if _, ok := knownRuleSets[tag]; !ok { return nil, fmt.Errorf("route rule %q references unknown Rule Set %q", item.ID, tag) }
			if _, ok := seenTags[tag]; ok { return nil, fmt.Errorf("route rule %q contains duplicate Rule Set %q", item.ID, tag) }
			seenTags[tag] = struct{}{}
		}
		if _, exists := knownOutbounds[item.Outbound]; !exists {
			return nil, fmt.Errorf("route rule %q references unknown outbound %q", item.ID, item.Outbound)
		}
		rule := makeSingBoxRouteRule(domains, suffixes, keywords, regexes, cidrs, item.Outbound)
		if len(item.RuleSets) > 0 { rule["rule_set"] = item.RuleSets }
		rules = append(rules, rule)
	}
	// CSQTT is the explicit system fallback. Direct exclusions and enabled
	// user rules above it retain priority; no implicit first-outbound fallback
	// or system-wide traffic capture is introduced.
	config := singBoxConfig{
		Log: map[string]any{"level": "info", "timestamp": true},
		Inbounds: []map[string]any{{
			"type": "socks", "tag": singBoxInboundTag,
			"listen": listenHost, "listen_port": listenPort,
		}},
		Outbounds: outbounds,
		Route: map[string]any{"rules": rules, "rule_set": ruleSetConfigs, "final": singBoxCSQTTTag},
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sing-box config: %w", err)
	}
	return append(encoded, '\n'), nil
}
