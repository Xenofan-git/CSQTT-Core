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
	CSQTTAddress  string
	CSQTTEnabled  bool
	FinalOutbound string
	// DirectEnabled is a pointer so omitted legacy API fields retain the
	// historical enabled-by-default Direct behavior.
	DirectEnabled *bool
	DirectDomains        []string
	DirectDomainSuffixes []string
	DirectIPCIDRs        []string
	// CSQTTRule is the immutable system rule card. Its outbound is always
	// the managed CSQTT outbound; callers cannot redirect or delete it.
	CSQTTRule singBoxRouteRule
	// RouteOrder contains "csqtt" and user rule IDs. Direct is always pinned
	// first and is deliberately not represented in this movable sequence.
	RouteOrder []string
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

// normalizeSingBoxSystemCards migrates legacy requests that predate RouteOrder.
// In that legacy shape, the presence of a CSQTT outbound means its system card
// is enabled. New panel requests always include RouteOrder and must keep the
// card and outbound toggles exactly synchronized.
func normalizeSingBoxSystemCards(input singBoxConfigInput) (singBoxConfigInput, error) {
	if input.RouteOrder == nil {
		enabled := input.CSQTTEnabled || input.CSQTTRule.Enabled || strings.TrimSpace(input.CSQTTAddress) != ""
		input.CSQTTEnabled = enabled
		input.CSQTTRule.Enabled = enabled
	}
	if input.CSQTTEnabled != input.CSQTTRule.Enabled {
		return input, fmt.Errorf("CSQTT system card and CSQTT outbound must share the same enabled state")
	}
	if !input.CSQTTEnabled && strings.TrimSpace(input.CSQTTAddress) != "" {
		return input, fmt.Errorf("CSQTT SOCKS address must be empty while the CSQTT system card is disabled")
	}
	return input, nil
}

// buildSingBoxConfig creates an explicit SOCKS entrypoint and ordered route rules.
// It deliberately creates no TUN inbound, auto_route, auto_redirect, or system capture.
// Direct exclusions and user rules are evaluated in order; unmatched traffic uses only the explicitly selected default outbound.
func buildSingBoxConfig(input singBoxConfigInput) ([]byte, error) {
	var err error
	input, err = normalizeSingBoxSystemCards(input)
	if err != nil { return nil, err }
	listenHost, listenPort, err := singBoxHostPort(input.ListenAddress, "listen address")
	if err != nil {
		return nil, err
	}
	listenIP := net.ParseIP(listenHost)
	if listenIP == nil || listenIP.IsUnspecified() || listenIP.IsMulticast() || listenIP.IsLinkLocalUnicast() || (!listenIP.IsLoopback() && !listenIP.IsPrivate()) {
		return nil, fmt.Errorf("listen address must be loopback or a specific private LAN address; wildcard/public binds are forbidden")
	}
	outbounds := []map[string]any{{"type": "direct", "tag": singBoxDirectTag}}
	knownOutbounds := map[string]struct{}{singBoxDirectTag: {}}
	csqttEnabled := input.CSQTTEnabled || input.CSQTTRule.Enabled || strings.TrimSpace(input.CSQTTAddress) != ""
	if csqttEnabled {
		csqttHost, csqttPort, err := singBoxHostPort(input.CSQTTAddress, "CSQTT SOCKS address")
		if err != nil { return nil, err }
		csqttIP := net.ParseIP(csqttHost)
		if csqttIP == nil || !csqttIP.IsLoopback() { return nil, fmt.Errorf("CSQTT SOCKS outbound must use the local loopback listener") }
		if listenHost == csqttHost && listenPort == csqttPort { return nil, fmt.Errorf("sing-box inbound and CSQTT SOCKS outbound must use different endpoints") }
		outbounds = append(outbounds, map[string]any{"type":"socks","tag":singBoxCSQTTTag,"server":csqttHost,"server_port":csqttPort,"version":"5"})
		knownOutbounds[singBoxCSQTTTag] = struct{}{}
	}

	directDomains, err := normalizeSingBoxDomains(input.DirectDomains, false)
	if err != nil { return nil, fmt.Errorf("Direct domains: %w", err) }
	directSuffixes, err := normalizeSingBoxDomains(input.DirectDomainSuffixes, true)
	if err != nil { return nil, fmt.Errorf("Direct domain suffixes: %w", err) }
	directCIDRs, err := normalizeSingBoxCIDRs(input.DirectIPCIDRs)
	if err != nil { return nil, fmt.Errorf("Direct IP/CIDR: %w", err) }

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

	rules := make([]map[string]any, 0, 3+len(input.Rules))
	rules = append(rules, map[string]any{
		"inbound": []string{singBoxInboundTag},
		"action": "sniff",
	})

	// Compile every enabled card into a stable ID -> sing-box rule map, then
	// apply the panel's explicit priority order. Direct is immutable and pinned.
	routeByID := make(map[string]map[string]any, len(input.Rules)+2)
	directEnabled := input.DirectEnabled == nil || *input.DirectEnabled
	if directEnabled && (len(directDomains)+len(directSuffixes)+len(directCIDRs) > 0) {
		routeByID["direct"] = makeSingBoxRouteRule(directDomains, directSuffixes, nil, nil, directCIDRs, singBoxDirectTag)
	}

	seenRuleIDs := make(map[string]struct{}, len(input.Rules))
	compileRule := func(item singBoxRouteRule, id, outbound string) (map[string]any, error) {
		if !singBoxIDPattern.MatchString(id) { return nil, fmt.Errorf("invalid route rule ID %q", id) }
		domains, err := normalizeSingBoxDomains(item.Domains, false)
		if err != nil { return nil, fmt.Errorf("route rule %q domains: %w", id, err) }
		suffixes, err := normalizeSingBoxDomains(item.DomainSuffixes, true)
		if err != nil { return nil, fmt.Errorf("route rule %q suffixes: %w", id, err) }
		keywords, err := normalizeSingBoxKeywords(item.DomainKeywords)
		if err != nil { return nil, fmt.Errorf("route rule %q keywords: %w", id, err) }
		regexes, err := normalizeSingBoxRegexes(item.DomainRegexes)
		if err != nil { return nil, fmt.Errorf("route rule %q regexes: %w", id, err) }
		cidrs, err := normalizeSingBoxCIDRs(item.IPCIDRs)
		if err != nil { return nil, fmt.Errorf("route rule %q IP/CIDR: %w", id, err) }
		if err := validateSingBoxAdvancedRouteFields(item.Advanced); err != nil { return nil, fmt.Errorf("route rule %q advanced fields: %w", id, err) }
		if len(domains)+len(suffixes)+len(keywords)+len(regexes)+len(cidrs)+len(item.RuleSets) == 0 && !singBoxAdvancedHasMatcher(item.Advanced) {
			return nil, fmt.Errorf("route rule %q has no matchers", id)
		}
		seenTags := make(map[string]struct{}, len(item.RuleSets))
		for _, tag := range item.RuleSets {
			if _, ok := knownRuleSets[tag]; !ok { return nil, fmt.Errorf("route rule %q references unknown Rule Set %q", id, tag) }
			if _, ok := seenTags[tag]; ok { return nil, fmt.Errorf("route rule %q contains duplicate Rule Set %q", id, tag) }
			seenTags[tag] = struct{}{}
		}
		if _, exists := knownOutbounds[outbound]; !exists { return nil, fmt.Errorf("route rule %q references unknown outbound %q", id, outbound) }
		rule := makeSingBoxRouteRule(domains, suffixes, keywords, regexes, cidrs, outbound)
		if item.Advanced["type"] != nil {
			var ruleType string
			if err := json.Unmarshal(item.Advanced["type"], &ruleType); err == nil && ruleType == "logical" { delete(rule, "inbound") }
		}
		if len(item.RuleSets) > 0 { rule["rule_set"] = item.RuleSets }
		for key, raw := range item.Advanced {
			if _, managed := singBoxManagedRouteFields[key]; managed { return nil, fmt.Errorf("route rule %q advanced field %q conflicts with managed routing", id, key) }
			var value any
			if err := json.Unmarshal(raw, &value); err != nil { return nil, fmt.Errorf("route rule %q advanced field %q: %w", id, key, err) }
			rule[key] = value
		}
		return rule, nil
	}

	if csqttEnabled && input.CSQTTRule.Enabled {
		hasMatcher := len(input.CSQTTRule.Domains)+len(input.CSQTTRule.DomainSuffixes)+len(input.CSQTTRule.DomainKeywords)+len(input.CSQTTRule.DomainRegexes)+len(input.CSQTTRule.IPCIDRs)+len(input.CSQTTRule.RuleSets) > 0 || singBoxAdvancedHasMatcher(input.CSQTTRule.Advanced)
		if hasMatcher {
			csqttRule, err := compileRule(input.CSQTTRule, "csqtt", singBoxCSQTTTag)
			if err != nil { return nil, err }
			routeByID["csqtt"] = csqttRule
		}
	}
	for _, item := range input.Rules {
		if !singBoxIDPattern.MatchString(item.ID) { return nil, fmt.Errorf("invalid route rule ID %q", item.ID) }
		if item.ID == "direct" || item.ID == "csqtt" { return nil, fmt.Errorf("route rule ID %q is reserved for a system card", item.ID) }
		if _, exists := seenRuleIDs[item.ID]; exists { return nil, fmt.Errorf("duplicate route rule ID %q", item.ID) }
		seenRuleIDs[item.ID] = struct{}{}
		if !item.Enabled { continue }
		rule, err := compileRule(item, item.ID, item.Outbound)
		if err != nil { return nil, err }
		routeByID[item.ID] = rule
	}

	ordered := make([]string, 0, len(routeByID))
	seenOrder := make(map[string]struct{}, len(input.RouteOrder))
	for _, id := range input.RouteOrder {
		if id == "direct" { return nil, fmt.Errorf("Direct is pinned and cannot be reordered") }
		if _, exists := seenOrder[id]; exists { return nil, fmt.Errorf("duplicate route order entry %q", id) }
		seenOrder[id] = struct{}{}
		if _, exists := routeByID[id]; !exists {
			// A disabled card remains in the panel model and therefore may be
			// present in RouteOrder without producing a runtime route rule.
			if id != "csqtt" {
				if _, known := seenRuleIDs[id]; !known { return nil, fmt.Errorf("route order references unknown rule %q", id) }
			}
			continue
		}
		ordered = append(ordered, id)
	}
	// Legacy requests omit RouteOrder: preserve their Rules slice order.
	if len(input.RouteOrder) == 0 {
		for _, item := range input.Rules { if item.Enabled { ordered = append(ordered, item.ID) } }
		if input.CSQTTRule.Enabled { ordered = append([]string{"csqtt"}, ordered...) }
	} else {
		for _, item := range input.Rules {
			if item.Enabled {
				if _, exists := seenOrder[item.ID]; !exists { ordered = append(ordered, item.ID) }
			}
		}
		if input.CSQTTRule.Enabled {
			if _, exists := seenOrder["csqtt"]; !exists { ordered = append(ordered, "csqtt") }
		}
	}
	if directRule, ok := routeByID["direct"]; ok { rules = append(rules, directRule) }
	for _, id := range ordered { if rule, ok := routeByID[id]; ok { rules = append(rules, rule) } }
	// Explicit fallback selection. Legacy configs keep CSQTT when configured.
	finalOutbound := strings.TrimSpace(input.FinalOutbound)
	if finalOutbound == "" { return nil, fmt.Errorf("select a default outbound for unmatched traffic; CSQTT is never selected implicitly") }
	if _, ok := knownOutbounds[finalOutbound]; !ok { return nil, fmt.Errorf("default outbound %q is not configured", finalOutbound) }

	config := singBoxConfig{
		Log: map[string]any{"level": "info", "timestamp": true},
		Inbounds: []map[string]any{{
			"type": "socks", "tag": singBoxInboundTag,
			"listen": listenHost, "listen_port": listenPort,
		}},
		Outbounds: outbounds,
		Route: map[string]any{"rules": rules, "rule_set": ruleSetConfigs, "final": finalOutbound},
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sing-box config: %w", err)
	}
	return append(encoded, '\n'), nil
}
