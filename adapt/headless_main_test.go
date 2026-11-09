//go:build linux

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	libs "lalune-desktop/Libs"
)

func TestAutoApiCallCountAndHashBatchContract(t *testing.T) {
	cases := []struct {
		name string
		workers int
		perCall int
		want int
	}{
		{name: "one worker group", workers: 1, perCall: 9, want: 1},
		{name: "exactly one call", workers: 9, perCall: 9, want: 1},
		{name: "rounds up partial call", workers: 10, perCall: 9, want: 2},
		{name: "three calls for 27 workers", workers: 27, perCall: 9, want: 3},
		{name: "caps at six calls", workers: 126, perCall: 9, want: 6},
		{name: "caps pathological worker count", workers: 10000, perCall: 1, want: 6},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := libs.CallCountForWorkers(tt.workers, tt.perCall); got != tt.want {
				t.Fatalf("CallCountForWorkers(%d, %d) = %d, want %d", tt.workers, tt.perCall, got, tt.want)
			}
		})
	}

	batch := &libs.VkCallsBatch{Calls: []libs.VkAutoCall{
		{CallID: "fixture-call-1", Hash: "fixture-hash-1"},
		{CallID: "fixture-call-2", Hash: "fixture-hash-2"},
	}}
	if got, want := batch.Hashes(), []string{"fixture-hash-1", "fixture-hash-2"}; !slices.Equal(got, want) {
		t.Fatalf("Auto API hashes = %#v, want %#v", got, want)
	}
	if got, want := batch.CallIds(), []string{"fixture-call-1", "fixture-call-2"}; !slices.Equal(got, want) {
		t.Fatalf("Auto API call IDs = %#v, want %#v", got, want)
	}
}

func TestPanelSecurityUsesInstallerCredentials(t *testing.T) {
	oldPassword, hadPassword := os.LookupEnv("LALUNE_PANEL_PASSWORD")
	oldUser, hadUser := os.LookupEnv("LALUNE_PANEL_USER")
	oldCompatPassword, hadCompatPassword := os.LookupEnv("CSQTT_WEB_PASS")
	oldCompatUser, hadCompatUser := os.LookupEnv("CSQTT_WEB_USER")
	t.Cleanup(func() {
		for key, value := range map[string]struct{ value string; had bool }{
			"LALUNE_PANEL_PASSWORD": {oldPassword, hadPassword},
			"LALUNE_PANEL_USER": {oldUser, hadUser},
			"CSQTT_WEB_PASS": {oldCompatPassword, hadCompatPassword},
			"CSQTT_WEB_USER": {oldCompatUser, hadCompatUser},
		} {
			if value.had { _ = os.Setenv(key, value.value) } else { _ = os.Unsetenv(key) }
		}
	})
	_ = os.Unsetenv("LALUNE_PANEL_PASSWORD")
	_ = os.Unsetenv("LALUNE_PANEL_USER")
	_ = os.Setenv("CSQTT_WEB_PASS", "installer-secret")
	_ = os.Setenv("CSQTT_WEB_USER", "installer-user")
	handler := panelSecurity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	req.SetBasicAuth("installer-user", "installer-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent { t.Fatalf("installer credentials status = %d, want 204", w.Code) }
}

func TestPrivateClient(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xRealIP    string
		want       bool
	}{
		{name: "LAN IPv4", remoteAddr: "192.168.4.12:43120", want: true},
		{name: "loopback proxy", remoteAddr: "127.0.0.1:43120", want: true},
		{name: "Tailscale IPv4 peer", remoteAddr: "100.70.73.57:43120", want: true},
		{name: "Tailscale IPv6 peer", remoteAddr: "[fd7a:115c:a1e0::1234]:43120", want: true},
		{name: "public peer", remoteAddr: "8.8.8.8:43120", want: false},
		{name: "spoofed private X-Real-IP from public peer", remoteAddr: "8.8.8.8:43120", xRealIP: "192.168.4.2", want: false},
		{name: "spoofed Tailscale X-Real-IP from public peer", remoteAddr: "8.8.8.8:43120", xRealIP: "100.70.73.57", want: false},
		{name: "X-Real-IP ignored for loopback proxy", remoteAddr: "127.0.0.1:43120", xRealIP: "8.8.8.8", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://localhost/deploy", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xRealIP != "" {
				r.Header.Set("X-Real-IP", tt.xRealIP)
			}
			if got := privateClient(r); got != tt.want {
				t.Fatalf("privateClient() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectedConfigJsonClearsEmptySelection(t *testing.T) {
	core := &libs.AppCore{}
	if !core.SetSelectedConfigJson(`{"id":7,"name":"test"}`) {
		t.Fatal("could not set selected config")
	}
	if core.GetSelectedConfig() == nil {
		t.Fatal("selected config unexpectedly nil")
	}
	for _, raw := range []string{"null", "{}", ""} {
		if !core.SetSelectedConfigJson(raw) {
			t.Fatalf("could not clear selection with %q", raw)
		}
		if core.GetSelectedConfig() != nil {
			t.Fatalf("selection was not cleared for %q", raw)
		}
	}
}


func TestCaptchaRequestIsActive(t *testing.T) {
	now := time.Unix(1791549509, 0)
	tests := []struct {
		name string
		mode string
		url  string
		want bool
	}{
		{name: "active manual fallback", mode: "manual", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549600", want: true},
		{name: "explicit WBV selected mode uses Linux browser bridge", mode: "selected", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549600", want: true},
		{name: "selected mode is case and whitespace tolerant", mode: " Selected ", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549600", want: true},
		{name: "automatic retry never opens dialog", mode: "auto", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549600", want: false},
		{name: "unknown mode never opens dialog", mode: "wv", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549600", want: false},
		{name: "expired manual challenge", mode: "manual", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549400", want: false},
		{name: "expired selected challenge", mode: "selected", url: "https://id.vk.ru/not_robot_captcha?expired_at=1791549400", want: false},
		{name: "missing expiry", mode: "manual", url: "https://id.vk.ru/not_robot_captcha", want: false},
		{name: "invalid expiry", mode: "manual", url: "https://id.vk.ru/not_robot_captcha?expired_at=nope", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := captchaRequestIsActive(tt.mode, tt.url, now); got != tt.want {
				t.Fatalf("captchaRequestIsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRJSManualFallbackSupersedesAutoCaptchaEvent(t *testing.T) {
    // Simulate the core's event stream after RJS fails: its earlier Auto retry
    // must not open the Linux overlay, but the later manual fallback must.
    old := struct {
        pending bool
        mode, uri, session, handled string
        updated time.Time
    }{}
    captchaMu.Lock()
    old.pending, old.mode, old.uri, old.session, old.handled, old.updated =
        captchaPending, captchaMode, captchaRedirectURI, captchaSessionToken, captchaHandledSession, captchaUpdated
    captchaPending, captchaMode, captchaRedirectURI, captchaSessionToken, captchaHandledSession = false, "", "", "", ""
    captchaUpdated = time.Time{}
    captchaMu.Unlock()
    t.Cleanup(func() {
        captchaMu.Lock()
        captchaPending, captchaMode, captchaRedirectURI, captchaSessionToken, captchaHandledSession, captchaUpdated =
            old.pending, old.mode, old.uri, old.session, old.handled, old.updated
        captchaMu.Unlock()
    })

    now := time.Unix(1791549509, 0)
    uri := "https://id.vk.ru/not_robot_captcha?expired_at=1791549600"
    autoLogs, _ := json.Marshal([]string{"CAPTCHA_SOLVE|auto|" + uri + "|rjs-auto-session"})
    refreshCaptchaStateFromLogs(string(autoLogs), now)
    captchaMu.Lock()
    if captchaPending || captchaMode != "" {
        t.Fatalf("auto retry unexpectedly opened manual overlay: pending=%v mode=%q", captchaPending, captchaMode)
    }
    captchaMu.Unlock()

    fallbackLogs, _ := json.Marshal([]string{
        "CAPTCHA_SOLVE|auto|" + uri + "|rjs-auto-session",
        "CAPTCHA_SOLVE|manual|" + uri + "|rjs-manual-session",
    })
    refreshCaptchaStateFromLogs(string(fallbackLogs), now.Add(time.Second))
    captchaMu.Lock()
    defer captchaMu.Unlock()
    if !captchaPending {
        t.Fatal("manual RJS fallback did not create a pending CAPTCHA")
    }
    if captchaMode != "manual" {
        t.Fatalf("fallback mode = %q, want manual", captchaMode)
    }
    if captchaSessionToken != "rjs-manual-session" {
        t.Fatalf("fallback session = %q, want rjs-manual-session", captchaSessionToken)
    }
    if captchaRedirectURI != uri {
        t.Fatalf("fallback redirect URI = %q, want %q", captchaRedirectURI, uri)
    }
    if !captchaUpdated.Equal(now.Add(time.Second)) {
        t.Fatalf("updated = %v, want %v", captchaUpdated, now.Add(time.Second))
    }
}

func TestDirectDomainValidation(t *testing.T) {
	tests := []struct {
		name string
		domain string
		want bool
	}{
		{name: "base domain", domain: "example.com", want: true},
		{name: "subdomain", domain: "api.example.com", want: true},
		{name: "reject wildcard syntax", domain: "*.example.com", want: false},
		{name: "reject URL", domain: "https://example.com", want: false},
		{name: "reject path", domain: "example.com/path", want: false},
		{name: "reject empty label", domain: "example..com", want: false},
		{name: "reject leading hyphen", domain: "-bad.example", want: false},
		{name: "accept IPv4 literal", domain: "192.0.2.1", want: true},
		{name: "accept IPv6 literal", domain: "2001:db8::1", want: true},
		{name: "reject blank", domain: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validDirectDomain(tt.domain); got != tt.want {
				t.Fatalf("validDirectDomain(%q) = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}

func TestVersionEndpointReportsBuildProvenance(t *testing.T) {
	old := []string{buildCommit, buildRunID, buildRunNumber, buildDate, buildBranch, buildUpstreamCommit}
	buildCommit, buildRunID, buildRunNumber, buildDate = "commit-test", "run-test", "42", "2026-10-09T00:00:00Z"
	buildBranch, buildUpstreamCommit = "feature/csqtt-direct-route-tab", "e4a6d08b63aef6025bf8ad8f77da660ea552e04b"
	defer func() {
		buildCommit, buildRunID, buildRunNumber, buildDate = old[0], old[1], old[2], old[3]
		buildBranch, buildUpstreamCommit = old[4], old[5]
	}()
	r := httptest.NewRequest("GET", "/version", nil)
	w := httptest.NewRecorder()
	apiHandler(nil).ServeHTTP(w, r)
	if w.Code != 200 { t.Fatalf("/version status = %d, want 200", w.Code) }
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil { t.Fatalf("invalid /version JSON: %v", err) }
	want := map[string]string{
		"backend": "original-go", "core": "csqtt-server/2.1.9", "ui": "0.6.0",
		"commit": "commit-test", "buildRun": "run-test", "buildNumber": "42",
		"buildDate": "2026-10-09T00:00:00Z", "buildBranch": "feature/csqtt-direct-route-tab",
		"upstreamCommit": "e4a6d08b63aef6025bf8ad8f77da660ea552e04b",
	}
	for key, wantValue := range want {
		if got[key] != wantValue { t.Errorf("/version[%q] = %#v, want %q", key, got[key], wantValue) }
	}
}

func TestDirectPlanTransitions(t *testing.T) {
	tests := []struct {
		name, domains string
		wantDomains []string
		wantDNS, wantEnabled bool
		wantErr bool
	}{
		{name: "multiple domains", domains: "example.com, api.example.com", wantDomains: []string{"example.com", "api.example.com"}, wantDNS: true, wantEnabled: true},
		{name: "mixed domains and IPv4 IPv6", domains: "example.com, 192.0.2.1, 2001:db8::1", wantDomains: []string{"example.com", "192.0.2.1", "2001:db8::1"}, wantDNS: true, wantEnabled: true},
		{name: "IPv4 only", domains: "192.0.2.1", wantDomains: []string{"192.0.2.1"}, wantDNS: false, wantEnabled: true},
		{name: "IPv6 only", domains: "2001:db8::1", wantDomains: []string{"2001:db8::1"}, wantDNS: false, wantEnabled: true},
		{name: "empty list", wantEnabled: false},
		{name: "reject URL", domains: "https://example.com", wantErr: true},
		{name: "reject CIDR", domains: "192.0.2.0/24", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domains, needsDNS, err := prepareDirectPlan(tt.domains)
			if (err != nil) != tt.wantErr { t.Fatalf("prepareDirectPlan error = %v, wantErr %v", err, tt.wantErr) }
			if tt.wantErr { return }
			if !slices.Equal(domains, tt.wantDomains) { t.Errorf("domains = %#v, want %#v", domains, tt.wantDomains) }
			if needsDNS != tt.wantDNS { t.Errorf("needsDNS = %v, want %v", needsDNS, tt.wantDNS) }
			if (len(domains) > 0) != tt.wantEnabled { t.Errorf("enabled = %v, want %v", len(domains)>0, tt.wantEnabled) }
		})
	}
}

// setCaptchaTestState installs an isolated pending challenge for endpoint tests.
func setCaptchaTestState(t *testing.T, session string) {
	t.Helper()
	captchaMu.Lock()
	oldPending, oldMode := captchaPending, captchaMode
	oldURI, oldSession, oldHandled, oldUpdated := captchaRedirectURI, captchaSessionToken, captchaHandledSession, captchaUpdated
	captchaPending = true
	captchaMode = "manual"
	captchaRedirectURI = "https://id.vk.ru/not_robot_captcha?expired_at=1791549600"
	captchaSessionToken = session
	captchaHandledSession = ""
	captchaUpdated = time.Now()
	captchaMu.Unlock()
	t.Cleanup(func() {
		captchaMu.Lock()
		captchaPending, captchaMode = oldPending, oldMode
		captchaRedirectURI, captchaSessionToken = oldURI, oldSession
		captchaHandledSession, captchaUpdated = oldHandled, oldUpdated
		captchaMu.Unlock()
	})
}

func TestCaptchaResultEndpointForwardsToCoreStdin(t *testing.T) {
	const session = "ci-captcha-session"
	const result = "fixture-captcha-result"
	setCaptchaTestState(t, session)

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	app := &App{runner: &LinuxRunner{}}
	linuxRunnerInputs.Store(app.runner, writer)
	defer linuxRunnerInputs.Delete(app.runner)

	expected := "CAPTCHA_RESULT|" + result + "\n"
	readResult := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, len(expected))
		_, err := io.ReadFull(reader, buf)
		if err != nil {
			readErr <- err
			return
		}
		readResult <- string(buf)
	}()

	body := strings.NewReader(`{"sessionToken":"ci-captcha-session","result":"fixture-captcha-result"}`)
	req := httptest.NewRequest("POST", "/captcha/result", body)
	rec := httptest.NewRecorder()
	apiHandler(app).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("/captcha/result status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid /captcha/result JSON: %v", err)
	}
	if !response["ok"] {
		t.Fatalf("/captcha/result rejected valid fixture: %s", rec.Body.String())
	}
	select {
	case got := <-readResult:
		if got != expected {
			t.Fatalf("core stdin = %q, want %q", got, expected)
		}
	case err := <-readErr:
		t.Fatalf("reading core stdin fixture: %v", err)
	case <-time.After(time.Second):
		t.Fatal("CAPTCHA_RESULT was not forwarded to core stdin")
	}
}

func TestCaptchaBridgeRoundTripThroughProtectedPanelAPI(t *testing.T) {
	const session = "ci-captcha-bridge-session"
	const result = "fixture-bridge-success-token"
	setCaptchaTestState(t, session)
	t.Setenv("LALUNE_PANEL_USER", "fixture-admin")
	t.Setenv("LALUNE_PANEL_PASSWORD", "fixture-panel-password")

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	app := &App{runner: &LinuxRunner{}}
	linuxRunnerInputs.Store(app.runner, writer)
	defer linuxRunnerInputs.Delete(app.runner)

	expected := "CAPTCHA_RESULT|" + result + "\n"
	readResult := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, len(expected))
		_, err := io.ReadFull(reader, buf)
		if err != nil { readErr <- err; return }
		readResult <- string(buf)
	}()

	body := strings.NewReader(`{"sessionToken":"ci-captcha-bridge-session","result":"fixture-bridge-success-token"}`)
	req := httptest.NewRequest(http.MethodPost, "http://panel.test/api/captcha/result", body)
	req.SetBasicAuth("fixture-admin", "fixture-panel-password")
	req.Header.Set("Origin", "http://panel.test")
	rec := httptest.NewRecorder()
	panelSecurity(panelAPIHandler(apiHandler(app))).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("protected same-origin CAPTCHA bridge POST status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil { t.Fatalf("invalid CAPTCHA response JSON: %v", err) }
	if !response["ok"] { t.Fatalf("protected panel rejected valid bridge result: %s", rec.Body.String()) }
	select {
	case got := <-readResult:
		if got != expected { t.Fatalf("core stdin = %q, want %q", got, expected) }
	case err := <-readErr:
		t.Fatalf("reading core stdin fixture: %v", err)
	case <-time.After(time.Second):
		t.Fatal("same-origin protected panel did not forward CAPTCHA result to core stdin")
	}
}

func TestCaptchaResultEndpointRejectsStaleSessionAndUnsafeResult(t *testing.T) {
	const session = "ci-captcha-session"
	setCaptchaTestState(t, session)
	app := &App{runner: &LinuxRunner{}}

	tests := []struct {
		name string
		body string
	}{
		{name: "stale session", body: `{"sessionToken":"old-session","result":"fixture-result"}`},
		{name: "embedded newline", body: `{"sessionToken":"ci-captcha-session","result":"fixture\nresult"}`},
		{name: "empty result", body: `{"sessionToken":"ci-captcha-session","result":"  "}`},
		{name: "oversized result", body: `{"sessionToken":"ci-captcha-session","result":"` + strings.Repeat("x", 16385) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/captcha/result", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			apiHandler(app).ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("/captcha/result status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var response map[string]bool
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("invalid /captcha/result JSON: %v", err)
			}
			if response["ok"] {
				t.Fatalf("/captcha/result accepted unsafe fixture: %s", tt.name)
			}
		})
	}
}

func TestCaptchaResultEndpointRequiresPost(t *testing.T) {
	req := httptest.NewRequest("GET", "/captcha/result", nil)
	rec := httptest.NewRecorder()
	apiHandler(&App{}).ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /captcha/result status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Fatalf("GET /captcha/result Allow = %q, want POST", got)
	}
}


func TestCaptchaResultEndpointRejectsMalformedJSON(t *testing.T) {
	setCaptchaTestState(t, "ci-captcha-session")
	req := httptest.NewRequest("POST", "/captcha/result", strings.NewReader(`{"sessionToken":`))
	rec := httptest.NewRecorder()
	apiHandler(&App{runner: &LinuxRunner{}}).ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("malformed POST /captcha/result status = %d, want 400", rec.Code)
	}
}

func TestCaptchaCancelEndpointForwardsCancellationAndConsumesChallenge(t *testing.T) {
	const session = "ci-captcha-cancel-session"
	setCaptchaTestState(t, session)

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	app := &App{runner: &LinuxRunner{}}
	linuxRunnerInputs.Store(app.runner, writer)
	defer linuxRunnerInputs.Delete(app.runner)

	expected := "CAPTCHA_RESULT|error:cancelled\n"
	gotCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, len(expected))
		_, err := io.ReadFull(reader, buf)
		if err != nil {
			errCh <- err
			return
		}
		gotCh <- string(buf)
	}()

	req := httptest.NewRequest("POST", "/captcha/cancel", nil)
	rec := httptest.NewRecorder()
	apiHandler(app).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /captcha/cancel status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid /captcha/cancel JSON: %v", err)
	}
	if !response["ok"] {
		t.Fatalf("/captcha/cancel rejected active fixture: %s", rec.Body.String())
	}
	select {
	case got := <-gotCh:
		if got != expected {
			t.Fatalf("core stdin = %q, want %q", got, expected)
		}
	case err := <-errCh:
		t.Fatalf("reading cancellation from core stdin: %v", err)
	case <-time.After(time.Second):
		t.Fatal("cancellation was not forwarded to core stdin")
	}

	captchaMu.Lock()
	pending, activeSession := captchaPending, captchaSessionToken
	captchaMu.Unlock()
	if pending || activeSession != "" {
		t.Fatalf("challenge was not consumed after cancellation: pending=%v session=%q", pending, activeSession)
	}
}

func TestCaptchaStateEndpointRequiresGet(t *testing.T) {
	req := httptest.NewRequest("POST", "/captcha/state", nil)
	rec := httptest.NewRecorder()
	apiHandler(&App{}).ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("POST /captcha/state status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET" {
		t.Fatalf("POST /captcha/state Allow = %q, want GET", got)
	}
}


func TestPanelListenAddressUsesInstallerPort(t *testing.T) {
    old, had := os.LookupEnv("CSQTT_LALUNE_WEB_PORT")
    t.Cleanup(func(){ if had { _ = os.Setenv("CSQTT_LALUNE_WEB_PORT", old) } else { _ = os.Unsetenv("CSQTT_LALUNE_WEB_PORT") } })
    _ = os.Setenv("CSQTT_LALUNE_WEB_PORT", "47002")
    got, err := panelListenAddress()
    if err != nil { t.Fatal(err) }
    if got != "0.0.0.0:47002" { t.Fatalf("panelListenAddress() = %q, want 0.0.0.0:47002", got) }
    _ = os.Setenv("CSQTT_LALUNE_WEB_PORT", "70000")
    if _, err := panelListenAddress(); err == nil { t.Fatal("invalid panel port was accepted") }
}


func TestTunnelAuthPasswordPersistenceAndPermissions(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    const password = "fixture-tunnel-secret-42"
    if err := saveTunnelAuthPassword(password); err != nil { t.Fatalf("save password: %v", err) }
    got, err := loadTunnelAuthPassword()
    if err != nil { t.Fatalf("load password: %v", err) }
    if got != password { t.Fatalf("loaded password = %q, want exact saved value", got) }
    path, err := tunnelAuthFile()
    if err != nil { t.Fatal(err) }
    info, err := os.Stat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0600 { t.Fatalf("password file mode = %04o, want 0600", info.Mode().Perm()) }
    if err := saveTunnelAuthPassword(""); err != nil { t.Fatalf("clear password: %v", err) }
    if _, err := loadTunnelAuthPassword(); err == nil { t.Fatal("empty password remained configured") }
}

func TestTunnelAuthRejectsInvalidPassword(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    for _, password := range []string{"contains space", "line\\nbreak", strings.Repeat("x", 257)} {
        if err := saveTunnelAuthPassword(password); err == nil {
            t.Errorf("invalid password %q was accepted", password)
        }
    }
}

func TestTunnelAuthAPIReportsOnlyConfiguredState(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    const password = "api-secret-must-never-be-returned"
    handler := apiHandler(&App{core: libs.NewAppCore()})
    put := httptest.NewRequest(http.MethodPut, "/tunnel/auth", strings.NewReader(`{"password":"`+password+`"}`))
    putRec := httptest.NewRecorder()
    handler.ServeHTTP(putRec, put)
    if putRec.Code != http.StatusOK { t.Fatalf("PUT /tunnel/auth status = %d: %s", putRec.Code, putRec.Body.String()) }
    if strings.Contains(putRec.Body.String(), password) { t.Fatal("PUT response leaked tunnel password") }
    get := httptest.NewRequest(http.MethodGet, "/tunnel/auth", nil)
    getRec := httptest.NewRecorder()
    handler.ServeHTTP(getRec, get)
    if getRec.Code != http.StatusOK { t.Fatalf("GET /tunnel/auth status = %d", getRec.Code) }
    var state map[string]bool
    if err := json.Unmarshal(getRec.Body.Bytes(), &state); err != nil { t.Fatal(err) }
    if !state["configured"] { t.Fatal("GET /tunnel/auth did not report configured=true") }
    if strings.Contains(getRec.Body.String(), password) { t.Fatal("GET response leaked tunnel password") }
}

func TestVPNConnectAndReconnectRequireTunnelAuth(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    app := &App{core: libs.NewAppCore()}
    for _, path := range []string{"/vpn/connect", "/vpn/reconnect"} {
        req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"id":1}`))
        rec := httptest.NewRecorder()
        apiHandler(app).ServeHTTP(rec, req)
        if rec.Code != http.StatusPreconditionRequired {
            t.Errorf("POST %s without password status = %d, want 428: %s", path, rec.Code, rec.Body.String())
        }
        if !strings.Contains(rec.Body.String(), "пароль туннеля не задан") {
            t.Errorf("POST %s did not explain missing password: %s", path, rec.Body.String())
        }
    }
}

func TestVPNConnectAndReconnectRequireStableDeviceID(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    if err := saveTunnelAuthPassword("fixture-valid-password"); err != nil { t.Fatal(err) }
    app := &App{core: &libs.AppCore{}}
    for _, path := range []string{"/vpn/connect", "/vpn/reconnect"} {
        req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"id":1}`))
        rec := httptest.NewRecorder()
        apiHandler(app).ServeHTTP(rec, req)
        if rec.Code != http.StatusPreconditionRequired {
            t.Errorf("POST %s without device ID status = %d, want 428: %s", path, rec.Code, rec.Body.String())
        }
        if !strings.Contains(rec.Body.String(), "ID устройства LaLune не задан") {
            t.Errorf("POST %s did not explain missing device ID: %s", path, rec.Body.String())
        }
    }
}

func TestSingBoxAPIControlsIndependentManager(t *testing.T) {
	dir := t.TempDir()
	binary := newFakeSingBox(t, "if [ \"$1\" = check ]; then exit 0; fi\necho singbox-fixture-log\nexec sleep 60")
	manager := NewSingBoxManager(binary, filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	handler := apiHandlerWithSingBox(nil, manager)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://panel.test"+path, strings.NewReader(body))
		req.RemoteAddr = "192.168.1.10:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	config := `{"ListenAddress":"127.0.0.1:19180","CSQTTAddress":"127.0.0.1:19181","FinalOutbound":"direct"}`
	if got := request(http.MethodPut, "/singbox/config", config); got.Code != http.StatusOK { t.Fatalf("save config status=%d body=%s",got.Code,got.Body.String()) }
	loaded := request(http.MethodGet, "/singbox/config", "")
	if loaded.Code != http.StatusOK { t.Fatalf("load config status=%d body=%s", loaded.Code, loaded.Body.String()) }
	var restored struct { Config singBoxConfigInput `json:"config"`; Configured bool `json:"configured"` }
	if err := json.Unmarshal(loaded.Body.Bytes(), &restored); err != nil { t.Fatalf("decode loaded config: %v", err) }
	if !restored.Configured || restored.Config.ListenAddress != "127.0.0.1:19180" || restored.Config.CSQTTAddress != "127.0.0.1:19181" || restored.Config.FinalOutbound != singBoxDirectTag { t.Fatalf("loaded config mismatch: %#v", restored) }
	if got := request(http.MethodPost, "/singbox/start", ""); got.Code != http.StatusOK { t.Fatalf("start status=%d body=%s",got.Code,got.Body.String()) }
	if got := request(http.MethodGet, "/singbox/status", ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"running":true`) { t.Fatalf("status=%d body=%s",got.Code,got.Body.String()) }
	if got := request(http.MethodGet, "/singbox/logs", ""); got.Code != http.StatusOK && got.Code != http.StatusNoContent { t.Fatalf("logs status=%d body=%s",got.Code,got.Body.String()) }
	if got := request(http.MethodPost, "/singbox/stop", ""); got.Code != http.StatusOK { t.Fatalf("stop status=%d body=%s",got.Code,got.Body.String()) }
	if manager.Status().Running { t.Fatal("manager still running after API stop") }
}

func TestSingBoxAPIRejectsPublicControlAndUnknownConfigFields(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), filepath.Join(dir,"config.json"), filepath.Join(dir,"log"))
	handler := apiHandlerWithSingBox(nil, manager)
	public := httptest.NewRequest(http.MethodPost, "/singbox/start", nil)
	public.RemoteAddr = "8.8.8.8:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, public)
	if rec.Code != http.StatusForbidden { t.Fatalf("public control status=%d, want 403",rec.Code) }
	unknown := httptest.NewRequest(http.MethodPut, "/singbox/config", strings.NewReader(`{"ListenAddress":"127.0.0.1:1","CSQTTAddress":"127.0.0.1:2","unexpected":true}`))
	unknown.RemoteAddr = "192.168.1.2:1234"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unknown)
	if rec.Code != http.StatusBadRequest { t.Fatalf("unknown config field status=%d, want 400",rec.Code) }
}


func TestSingBoxAPIRejectsSystemCardTampering(t *testing.T) {
	dir := t.TempDir()
	manager := NewSingBoxManager(newFakeSingBox(t, "exit 0"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	handler := apiHandlerWithSingBox(nil, manager)
	cases := []struct {
		name string
		input singBoxConfigInput
	}{
		{
			name: "Direct cannot be moved",
			input: singBoxConfigInput{
				ListenAddress: "127.0.0.1:19180", CSQTTAddress: "127.0.0.1:19181",
				CSQTTEnabled: true, FinalOutbound: singBoxDirectTag,
				CSQTTRule: singBoxRouteRule{Enabled: true, Domains: []string{"csqtt.example"}},
				RouteOrder: []string{"direct", "csqtt"},
			},
		},
		{
			name: "route order cannot omit disabled user cards",
			input: singBoxConfigInput{
				ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag,
				RouteOrder: []string{"csqtt"},
				Rules: []singBoxRouteRule{{ID: "hidden-disabled", Enabled: false, Domains: []string{"disabled.example"}, Outbound: singBoxDirectTag}},
			},
		},
		{
			name: "disabled CSQTT cannot be referenced",
			input: singBoxConfigInput{
				ListenAddress: "127.0.0.1:19180", FinalOutbound: singBoxDirectTag,
				RouteOrder: []string{"csqtt", "user-route"},
				Rules: []singBoxRouteRule{{ID: "user-route", Enabled: true, Domains: []string{"proxy.example"}, Outbound: singBoxCSQTTTag}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.input)
			if err != nil { t.Fatal(err) }
			req := httptest.NewRequest(http.MethodPut, "/singbox/config", strings.NewReader(string(body)))
			req.RemoteAddr = "192.168.1.10:12345"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("tampered system-card request status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
