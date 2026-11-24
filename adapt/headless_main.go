//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"debug/buildinfo"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"lalune-desktop/Libs"
)

const headlessListen = "127.0.0.1:1062"
const panelListen = "0.0.0.0:1061"

// Set by the pinned ARM64 build workflow through Go -ldflags.
var (
	buildCommit         = "unknown"
	buildRunID          = "unknown"
	buildRunNumber      = "unknown"
	buildDate           = "unknown"
	buildBranch         = "unknown"
	buildUpstreamCommit = "unknown"
)

//go:embed frontend
var panelAssets embed.FS

type connectBody struct {
	ID int64 `json:"id"`
}

type configBody struct {
	Link     string `json:"link"`
	Protocol string `json:"protocol"`
}

type tunnelAuthBody struct {
	Password string `json:"password"`
}

// The panel and LinuxRunner share a private per-user secret file. It is
// outside the web root, atomically replaced, and never returned by the API.
func tunnelAuthFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "csqtt-lalune")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "tunnel-password"), nil
}

func loadTunnelAuthPassword() (string, error) {
	path, err := tunnelAuthFile()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	password := strings.TrimSpace(string(data))
	if password == "" {
		return "", fmt.Errorf("пароль туннеля не задан")
	}
	return password, nil
}

func saveTunnelAuthPassword(password string) error {
	password = strings.TrimSpace(password)
	if password == "" {
		path, err := tunnelAuthFile()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if len(password) > 256 || !regexp.MustCompile(`^[a-zA-Z0-9_.!?:#/-]+$`).MatchString(password) {
		return fmt.Errorf("недопустимый формат пароля туннеля")
	}
	path, err := tunnelAuthFile()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tunnel-password-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteString(password); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Deploy secrets are kept per destination in a root-private controller file.
// GET exposes only configured state and login, never either password.
type deploySavedCredentials struct {
	MainPassword string `json:"mainPassword"`
	WebLogin     string `json:"webLogin"`
	WebPassword  string `json:"webPassword"`
}

var deployCredentialsMu sync.Mutex
var deployCredentialPattern = regexp.MustCompile("^[A-Za-z0-9]+$")
var deployHostPattern = regexp.MustCompile("^[A-Za-z0-9_.:-]+$")

func deployHostKey(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(host) > 253 || !deployHostPattern.MatchString(host) {
		return "", fmt.Errorf("недопустимый адрес сервера")
	}
	return host, nil
}
func validateDeployCredentials(v deploySavedCredentials) error {
	if len(v.MainPassword) < 1 || len(v.MainPassword) > 256 || len(v.WebLogin) < 1 || len(v.WebLogin) > 64 || len(v.WebPassword) < 1 || len(v.WebPassword) > 256 || !deployCredentialPattern.MatchString(v.MainPassword) || !deployCredentialPattern.MatchString(v.WebLogin) || !deployCredentialPattern.MatchString(v.WebPassword) {
		return fmt.Errorf("авторизация: только латиница и цифры, логин до 64 символов, пароль до 256")
	}
	return nil
}
func deployCredentialsPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "csqtt-core")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("небезопасные права каталога авторизации")
	}
	return filepath.Join(dir, "deploy-credentials.json"), nil
}

// Call read/write only while holding deployCredentialsMu.
func readDeployCredentials(path string) (map[string]deploySavedCredentials, error) {
	values := make(map[string]deploySavedCredentials)
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > (1<<20) {
		return nil, fmt.Errorf("небезопасный файл сохранённой авторизации")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &values); err != nil || values == nil {
		return nil, fmt.Errorf("повреждён файл сохранённой авторизации")
	}
	return values, nil
}
func writeDeployCredentials(path string, values map[string]deploySavedCredentials) error {
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("слишком много сохранённых данных авторизации")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".deploy-credentials-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		defer d.Close()
		if err = d.Sync(); err != nil {
			return err
		}
	}
	return nil
}
func getDeployCredentials(host string) (deploySavedCredentials, bool, error) {
	key, err := deployHostKey(host)
	if err != nil {
		return deploySavedCredentials{}, false, err
	}
	deployCredentialsMu.Lock()
	defer deployCredentialsMu.Unlock()
	path, err := deployCredentialsPath()
	if err != nil {
		return deploySavedCredentials{}, false, err
	}
	values, err := readDeployCredentials(path)
	if err != nil {
		return deploySavedCredentials{}, false, err
	}
	value, ok := values[key]
	return value, ok, nil
}
func saveDeployCredentials(host string, value deploySavedCredentials) error {
	key, err := deployHostKey(host)
	if err != nil {
		return err
	}
	if err = validateDeployCredentials(value); err != nil {
		return err
	}
	deployCredentialsMu.Lock()
	defer deployCredentialsMu.Unlock()
	path, err := deployCredentialsPath()
	if err != nil {
		return err
	}
	values, err := readDeployCredentials(path)
	if err != nil {
		return err
	}
	if _, exists := values[key]; !exists && len(values) >= 32 {
		return fmt.Errorf("предел 32 сохранённых сервера")
	}
	values[key] = value
	return writeDeployCredentials(path, values)
}
func deleteDeployCredentials(host string) error {
	key, err := deployHostKey(host)
	if err != nil {
		return err
	}
	deployCredentialsMu.Lock()
	defer deployCredentialsMu.Unlock()
	path, err := deployCredentialsPath()
	if err != nil {
		return err
	}
	values, err := readDeployCredentials(path)
	if err != nil {
		return err
	}
	delete(values, key)
	return writeDeployCredentials(path, values)
}

// Inject only for the exact target host, never for other servers. Uninstall unchanged.
// SSH passwords are deliberately separate from the Deploy server tunnel/WEB
// credentials. They are never returned by API GET or stored in the browser.
var deploySSHMu sync.Mutex

func deploySSHPath() (string, error) {
	authPath, err := deployCredentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(authPath), "deploy-ssh-passwords.json"), nil
}
func readDeploySSH(path string) (map[string]string, error) {
	values := make(map[string]string)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, fmt.Errorf("небезопасный файл SSH-паролей")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return nil, fmt.Errorf("повреждён файл SSH-паролей")
	}
	return values, nil
}
func writeDeploySSH(path string, values map[string]string) error {
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("превышен лимит хранения SSH-паролей")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".deploy-ssh-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return err
		}
	}
	return nil
}
func getDeploySSHPassword(host string) (string, bool, error) {
	key, err := deployHostKey(host)
	if err != nil {
		return "", false, err
	}
	deploySSHMu.Lock()
	defer deploySSHMu.Unlock()
	path, err := deploySSHPath()
	if err != nil {
		return "", false, err
	}
	saved, err := readDeploySSH(path)
	if err != nil {
		return "", false, err
	}
	value, ok := saved[key]
	return value, ok && value != "", nil
}
func saveDeploySSHPassword(host, password string) error {
	key, err := deployHostKey(host)
	if err != nil {
		return err
	}
	if len(password) < 1 || len(password) > 1024 || strings.ContainsAny(password, "\x00\r\n") {
		return fmt.Errorf("SSH-пароль должен содержать 1–1024 символа без переводов строк")
	}
	deploySSHMu.Lock()
	defer deploySSHMu.Unlock()
	path, err := deploySSHPath()
	if err != nil {
		return err
	}
	saved, err := readDeploySSH(path)
	if err != nil {
		return err
	}
	if _, exists := saved[key]; !exists && len(saved) >= 32 {
		return fmt.Errorf("предел 32 сохранённых SSH-паролей")
	}
	saved[key] = password
	return writeDeploySSH(path, saved)
}
func deleteDeploySSHPassword(host string) error {
	key, err := deployHostKey(host)
	if err != nil {
		return err
	}
	deploySSHMu.Lock()
	defer deploySSHMu.Unlock()
	path, err := deploySSHPath()
	if err != nil {
		return err
	}
	saved, err := readDeploySSH(path)
	if err != nil {
		return err
	}
	delete(saved, key)
	return writeDeploySSH(path, saved)
}

func enrichDeployRequest(raw json.RawMessage) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return nil, fmt.Errorf("неверный JSON Deploy")
	}
	var protocol, host string
	_ = json.Unmarshal(body["protocol"], &protocol)
	_ = json.Unmarshal(body["host"], &host)
	if !strings.EqualFold(protocol, "CSQTT") {
		return raw, nil
	}
	read := func(key string) string { var v string; _ = json.Unmarshal(body[key], &v); return v }
	var usePassword, uninstall bool
	_ = json.Unmarshal(body["usePassword"], &usePassword)
	_ = json.Unmarshal(body["uninstall"], &uninstall)
	// Both install and uninstall use the saved SSH password only for this host.
	if usePassword && read("password") == "" {
		saved, ok, err := getDeploySSHPassword(host)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("для этого сервера не сохранён SSH-пароль")
		}
		body["password"], err = json.Marshal(saved)
		if err != nil {
			return nil, err
		}
	}
	if uninstall {
		return json.Marshal(body)
	}
	count := 0
	for _, key := range []string{"mainPassword", "webUser", "webPassword"} {
		if read(key) != "" {
			count++
		}
	}
	if count == 3 {
		return json.Marshal(body)
	} // explicit API credential compatibility
	if count != 0 {
		return nil, fmt.Errorf("частично заполненные данные авторизации")
	}
	saved, ok, err := getDeployCredentials(host)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("для сервера не сохранена авторизация")
	}
	for key, value := range map[string]string{
		"mainPassword": saved.MainPassword, "webUser": saved.WebLogin, "webPassword": saved.WebPassword,
	} {
		body[key], err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(body)
}

func csqttDeviceID(core *libs.AppCore) string {
	if core == nil {
		return ""
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(core.GetSettingsJson()), &settings); err != nil {
		return ""
	}
	id, _ := settings["deviceId"].(string)
	return strings.TrimSpace(id)
}

// validateTunnelAuth is shared by connect and reconnect so neither API path
// can bypass the same fail-closed credential and stable-device-ID checks.
func validateTunnelAuth(app *App) error {
	if _, err := loadTunnelAuthPassword(); err != nil {
		return fmt.Errorf("пароль туннеля не задан")
	}
	candidate, modeErr := csqtt250CandidateMode()
	if modeErr != nil {
		return fmt.Errorf("недопустимый режим клиента CSQTT Core")
	}
	if candidate {
		// The original CSQTT 2.5.0 client authenticates with the tunnel
		// password only. The old device-id flag is removed in 2.5.0.
		if app == nil || app.core == nil {
			return fmt.Errorf("ядро не готово")
		}
		return nil
	}
	if app == nil || csqttDeviceID(app.core) == "" {
		return fmt.Errorf("ID устройства CSQTT Core не задан")
	}
	return nil
}

func writeRaw(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(value))
}

func mustJSON(value string) string { b, _ := json.Marshal(value); return string(b) }

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		w.Header().Set("Allow", want)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// privateClient permits sensitive operations only from a private LAN or the
// Tailscale address range. Do not trust X-Real-IP here: the panel listener is
// directly reachable on the LAN, so a caller could spoof that header. When
// accessed through a local reverse proxy, RemoteAddr is loopback and is allowed.
func privateClient(r *http.Request) bool {
	raw := r.RemoteAddr
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	ip := net.ParseIP(strings.Trim(raw, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	_, tailscaleIPv4, _ := net.ParseCIDR("100.64.0.0/10")
	if tailscaleIPv4.Contains(ip) {
		return true
	}
	_, tailscaleIPv6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return tailscaleIPv6.Contains(ip)
}

var captchaMu sync.Mutex
var captchaPending bool
var captchaMode string
var captchaRedirectURI string
var captchaSessionToken string
var captchaHandledSession string
var captchaUpdated time.Time

func captchaRequestIsActive(mode, redirectURI string, now time.Time) bool {
	// "selected" is emitted by the pinned core's explicit WBV mode. On Linux
	// it must use the same browser bridge as the final manual fallback.
	// "auto" events remain internal retries and must never trigger UI.
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "manual" && mode != "selected" {
		return false
	}
	// VK challenge links include an explicit expiry. Do not resurrect an old
	// challenge from the persistent application log after it has expired.
	u, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}
	expires := strings.TrimSpace(u.Query().Get("expired_at"))
	if expires == "" {
		return false
	}
	unix, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || unix <= now.Unix() {
		return false
	}
	return true
}

func refreshCaptchaStateFromLogs(raw string, now time.Time) {
	var logs []string
	if json.Unmarshal([]byte(raw), &logs) != nil {
		return
	}
	captchaMu.Lock()
	defer captchaMu.Unlock()
	// The newest CAPTCHA_SOLVE event is authoritative for this moment. This
	// allows a later manual fallback event to supersede an earlier auto event.
	for i := len(logs) - 1; i >= 0; i-- {
		parts := strings.SplitN(strings.TrimSpace(logs[i]), "|", 4)
		if len(parts) != 4 || parts[0] != "CAPTCHA_SOLVE" {
			continue
		}
		session := strings.TrimSpace(parts[3])
		if session == "" || session == captchaHandledSession {
			captchaPending = false
			captchaMode = ""
			captchaRedirectURI = ""
			captchaSessionToken = ""
			captchaUpdated = time.Time{}
			return
		}
		if !captchaRequestIsActive(parts[1], parts[2], now) {
			captchaPending = false
			captchaMode = ""
			captchaRedirectURI = ""
			captchaSessionToken = ""
			captchaUpdated = time.Time{}
			return
		}
		if captchaSessionToken != session || !captchaPending {
			captchaUpdated = now
		}
		captchaPending = true
		captchaMode = strings.TrimSpace(parts[1])
		captchaRedirectURI = parts[2]
		captchaSessionToken = session
		return
	}
	// No CAPTCHA event in the current log: clear any in-memory stale state.
	captchaPending = false
	captchaMode = ""
	captchaRedirectURI = ""
	captchaSessionToken = ""
	captchaUpdated = time.Time{}
}

func refreshCaptchaState(app *App) {
	refreshCaptchaStateFromLogs(app.GetLogsJson(), time.Now())
}

func captchaState(app *App) map[string]any {
	refreshCaptchaState(app)
	captchaMu.Lock()
	defer captchaMu.Unlock()
	return map[string]any{"pending": captchaPending, "mode": captchaMode, "redirectUri": captchaRedirectURI, "sessionToken": captchaSessionToken, "updated": captchaUpdated.UnixMilli()}
}

func finishCaptcha(app *App, sessionToken, result string) bool {
	captchaMu.Lock()
	if !captchaPending || sessionToken == "" || sessionToken != captchaSessionToken {
		captchaMu.Unlock()
		return false
	}
	captchaMu.Unlock()
	result = strings.TrimSpace(result)
	if result == "" || len(result) > 16384 || strings.ContainsAny(result, "\r\n") {
		return false
	}
	if app.runner == nil || !app.runner.SubmitCaptchaResult(result) {
		return false
	}
	captchaMu.Lock()
	captchaHandledSession = sessionToken
	captchaPending = false
	captchaMode = ""
	captchaRedirectURI = ""
	captchaSessionToken = ""
	captchaUpdated = time.Time{}
	captchaMu.Unlock()
	return true
}

var vkAutoMu sync.Mutex
var vkAutoResult = "{\"pending\":false}"
var vkAutoRunning bool

var singBoxManagerOnce sync.Once
var sharedSingBoxManager *SingBoxManager

func defaultSingBoxManager() *SingBoxManager {
	singBoxManagerOnce.Do(func() {
		dir := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_DATA_DIR"))
		if dir == "" || !filepath.IsAbs(dir) {
			base, err := os.UserConfigDir()
			if err != nil {
				base = os.TempDir()
			}
			dir = filepath.Join(base, "csqtt-lalune", "sing-box")
		}
		binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
		if binary == "" {
			bundled := "/usr/local/lib/csqtt-lalune/sing-box/1.14.4/sing-box"
			if info, err := os.Stat(bundled); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				binary = bundled
			} else {
				binary = "sing-box"
			}
		}
		sharedSingBoxManager = NewSingBoxManager(binary, filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	})
	return sharedSingBoxManager
}

func apiHandler(app *App) http.Handler { return apiHandlerWithSingBox(app, defaultSingBoxManager()) }

func apiHandlerWithSingBox(app *App, singBox *SingBoxManager) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, map[string]any{
			"api":            1,
			"backend":        "original-go",
			"core":           csqttCoreSelectedVersionLabel(),
			"ui":             "0.6.0",
			"commit":         buildCommit,
			"buildRun":       buildRunID,
			"buildNumber":    buildRunNumber,
			"buildDate":      buildDate,
			"buildBranch":    buildBranch,
			"upstreamCommit": buildUpstreamCommit,
		})
	})

	mux.HandleFunc("/configs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeRaw(w, app.GetConfigsJson())
		case http.MethodPost:
			var body configBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			if body.Link == "" {
				http.Error(w, "link is required", http.StatusBadRequest)
				return
			}
			ok := app.SaveConfigWithProtocol(body.Link, body.Protocol)
			writeJSON(w, map[string]bool{"ok": ok})
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/configs/selected", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeRaw(w, app.GetSelectedConfigJson())
		case http.MethodPut, http.MethodPost:
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": app.SetSelectedConfigJson(string(raw))})
		default:
			w.Header().Set("Allow", "GET, PUT, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/configs/", func(w http.ResponseWriter, r *http.Request) {
		idText := strings.TrimPrefix(r.URL.Path, "/configs/")
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil {
			http.Error(w, "invalid config id", http.StatusBadRequest)
			return
		}
		if !method(w, r, http.MethodDelete) {
			return
		}
		writeJSON(w, map[string]bool{"ok": app.DeleteConfig(id)})
	})

	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeRaw(w, app.GetSettingsJson())
		case http.MethodPut, http.MethodPost:
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": app.SaveSettings(string(raw))})
		default:
			w.Header().Set("Allow", "GET, PUT, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/settings/reset", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		http.Error(w, "settings reset is not implemented by CSQTT Core backend", http.StatusNotImplemented)
	})

	mux.HandleFunc("/tunnel/auth", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, err := loadTunnelAuthPassword()
			writeJSON(w, map[string]bool{"configured": err == nil})
		case http.MethodPut, http.MethodPost:
			var body tunnelAuthBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			if err := saveTunnelAuthPassword(body.Password); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": true, "configured": strings.TrimSpace(body.Password) != ""})
		default:
			w.Header().Set("Allow", "GET, PUT, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/vpn/status", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeRaw(w, app.GetStatusJson())
	})

	mux.HandleFunc("/vpn/connect", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		var body connectBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := validateTunnelAuth(app); err != nil {
			http.Error(w, err.Error(), http.StatusPreconditionRequired)
			return
		}
		writeJSON(w, map[string]bool{"ok": app.Connect(body.ID)})
	})

	mux.HandleFunc("/vpn/disconnect", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, map[string]bool{"ok": app.Disconnect()})
	})

	mux.HandleFunc("/vpn/reconnect", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		var body connectBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := validateTunnelAuth(app); err != nil {
			http.Error(w, err.Error(), http.StatusPreconditionRequired)
			return
		}
		_ = app.Disconnect()
		writeJSON(w, map[string]bool{"ok": app.Connect(body.ID)})
	})

	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeRaw(w, app.GetLogsJson())
	})

	mux.HandleFunc("/logs/tail", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeRaw(w, app.GetLogsJson())
	})

	mux.HandleFunc("/logs/clear", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, map[string]bool{"ok": app.ClearLogs()})
	})

	mux.HandleFunc("/socks5/status", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "SOCKS5 status is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		if app == nil || app.socks == nil {
			writeJSON(w, Socks5Status{Interface: "eth1", Protocol: "SOCKS5", Auth: "no-auth"})
			return
		}
		writeJSON(w, app.socks.Status())
	})

	mux.HandleFunc("/singbox/status", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		status := singBox.Status()
		_, err := os.Stat(singBox.configPath)
		writeJSON(w, map[string]any{"running": status.Running, "pid": status.PID, "configured": err == nil, "log_path": status.LogPath, "error": status.Error})
	})
	mux.HandleFunc("/singbox/config", func(w http.ResponseWriter, r *http.Request) {
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			input, err := singBox.LoadConfig()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					writeJSON(w, map[string]any{"configured": false})
					return
				}
				http.Error(w, "unable to load sing-box config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"configured": true, "config": input})
		case http.MethodPut:
			var input singBoxConfigInput
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				http.Error(w, "invalid sing-box config request", http.StatusBadRequest)
				return
			}
			applied, err := singBox.ApplyConfig(input)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			writeJSON(w, map[string]bool{"ok": true, "applied": applied})
		default:
			w.Header().Set("Allow", "GET, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/singbox/rulesets", func(w http.ResponseWriter, r *http.Request) {
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		if !method(w, r, http.MethodGet) {
			return
		}
		sets, err := singBox.ListRuleSets()
		if err != nil {
			http.Error(w, "unable to list Rule Sets: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"rule_sets": sets})
	})
	mux.HandleFunc("/singbox/rulesets/", func(w http.ResponseWriter, r *http.Request) {
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		if !method(w, r, http.MethodDelete) {
			return
		}
		tag := strings.TrimPrefix(r.URL.Path, "/singbox/rulesets/")
		if tag == "" || strings.Contains(tag, "/") {
			http.Error(w, "invalid Rule Set tag", http.StatusBadRequest)
			return
		}
		if err := singBox.DeleteRuleSet(tag); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/singbox/rulesets/upload", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, (16<<20)+(1<<20))
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			http.Error(w, "invalid Rule Set upload or file exceeds 16 MiB", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing Rule Set file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		if err != nil || len(data) > 16<<20 {
			http.Error(w, "unable to read Rule Set file or file exceeds 16 MiB", http.StatusBadRequest)
			return
		}
		result, err := singBox.UploadRuleSet(strings.TrimSpace(r.FormValue("tag")), header.Filename, data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("/singbox/start", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		if err := singBox.Start(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/singbox/stop", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		if err := singBox.Stop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/singbox/logs", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "sing-box logs are available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := singBox.CopyLogs(w); err != nil {
			if os.IsNotExist(err) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, "unable to read sing-box log", http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/deploy/credentials", func(w http.ResponseWriter, r *http.Request) {
		if !privateClient(r) {
			http.Error(w, "LAN or Tailscale only", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			v, ok, err := getDeployCredentials(r.URL.Query().Get("host"))
			if err != nil {
				http.Error(w, "не удалось прочитать авторизацию", http.StatusBadRequest)
				return
			}
			user := ""
			if ok {
				user = v.WebLogin
			}
			writeJSON(w, map[string]any{"configured": ok, "webLogin": user})
		case http.MethodPut:
			var body struct {
				Host         string `json:"host"`
				MainPassword string `json:"mainPassword"`
				WebLogin     string `json:"webLogin"`
				WebPassword  string `json:"webPassword"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				http.Error(w, "некорректные данные", http.StatusBadRequest)
				return
			}
			if err := saveDeployCredentials(body.Host, deploySavedCredentials{MainPassword: body.MainPassword, WebLogin: body.WebLogin, WebPassword: body.WebPassword}); err != nil {
				http.Error(w, "не удалось сохранить авторизацию", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": true, "configured": true})
		case http.MethodDelete:
			if err := deleteDeployCredentials(r.URL.Query().Get("host")); err != nil {
				http.Error(w, "не удалось удалить авторизацию", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/deploy/ssh-credentials", func(w http.ResponseWriter, r *http.Request) {
		if !privateClient(r) {
			http.Error(w, "LAN or Tailscale only", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_, ok, err := getDeploySSHPassword(r.URL.Query().Get("host"))
			if err != nil {
				http.Error(w, "не удалось прочитать SSH-пароль", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"configured": ok})
		case http.MethodPut:
			var body struct {
				Host     string `json:"host"`
				Password string `json:"password"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				http.Error(w, "некорректный запрос сохранения SSH-пароля", http.StatusBadRequest)
				return
			}
			if err := saveDeploySSHPassword(body.Host, body.Password); err != nil {
				http.Error(w, "не удалось сохранить SSH-пароль", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": true, "configured": true})
		case http.MethodDelete:
			if err := deleteDeploySSHPassword(r.URL.Query().Get("host")); err != nil {
				http.Error(w, "не удалось удалить SSH-пароль", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/deploy", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "deployment is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		prepared, err := enrichDeployRequest(raw)
		if err != nil {
			http.Error(w, "для этого сервера не заданы корректные данные авторизации", http.StatusPreconditionFailed)
			return
		}
		writeJSON(w, map[string]bool{"ok": libs.DeployProtocol(string(prepared))})
	})
	mux.HandleFunc("/deploy/log", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "deployment status is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		writeRaw(w, mustJSON(libs.DeployLog()))
	})
	mux.HandleFunc("/deploy/status", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "deployment status is available only from LAN or Tailscale", http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]bool{"deploying": libs.DeployBusy()})
	})

	// The inherited upstream updater is NOT safe for this hybrid: it could
	// replace our controller with Endlad2/csqtt-core. Never call that code.
	mux.HandleFunc("/updates/core", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		http.Error(w, "legacy upstream updater removed; use verified CSQTT Core release", http.StatusGone)
	})
	mux.HandleFunc("/updates/core/wait", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "LAN or Tailscale only", http.StatusForbidden)
			return
		}
		if app == nil || app.core == nil || app.core.IsConnected() {
			http.Error(w, "disconnect CSQTT Core before updating", http.StatusConflict)
			return
		}
		result, err := prepareVerifiedCSQTTCoreUpdate(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("/updates/core/check", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, checkOurCSQTTCoreUpdate(r.Context()))
	})
	mux.HandleFunc("/updates/core/status", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, readCSQTTCoreUpdateStatus())
	})
	mux.HandleFunc("/updates/lalune", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, app.CheckLaLuneUpdate())
	})
	mux.HandleFunc("/vk/state", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, app.GetVKTokenState())
	})
	mux.HandleFunc("/vk/login", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, map[string]bool{"ok": app.LoginVK()})
	})
	mux.HandleFunc("/vk/import", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !privateClient(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"ok": false, "error": "Некорректный запрос"})
			return
		}
		if err := app.core.SaveVKTokenInput(req.Token); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/vk/delete", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, map[string]bool{"ok": app.DeleteVKToken()})
	})
	mux.HandleFunc("/vk/validate", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, app.ValidateVKToken())
	})
	mux.HandleFunc("/vk/auto", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		vkAutoMu.Lock()
		if vkAutoRunning {
			vkAutoMu.Unlock()
			writeRaw(w, "{\"pending\":true}")
			return
		}
		vkAutoRunning = true
		vkAutoResult = "{\"pending\":true}"
		vkAutoMu.Unlock()
		go func() {
			result := app.RunVkAutoApiCalls()
			vkAutoMu.Lock()
			vkAutoResult = result
			vkAutoRunning = false
			vkAutoMu.Unlock()
		}()
		writeRaw(w, "{\"pending\":true}")
	})
	mux.HandleFunc("/vk/auto/poll", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		vkAutoMu.Lock()
		result := vkAutoResult
		vkAutoMu.Unlock()
		writeRaw(w, result)
	})
	mux.HandleFunc("/vk/finish", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		var ids []string
		if err := json.NewDecoder(r.Body).Decode(&ids); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]bool{"ok": app.FinishVkCalls(ids)})
	})

	mux.HandleFunc("/captcha/state", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, captchaState(app))
	})
	mux.HandleFunc("/captcha/result", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		var body struct {
			SessionToken string `json:"sessionToken"`
			Result       string `json:"result"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]bool{"ok": finishCaptcha(app, body.SessionToken, body.Result)})
	})
	mux.HandleFunc("/captcha/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		captchaMu.Lock()
		token := captchaSessionToken
		captchaMu.Unlock()
		ok := token != "" && finishCaptcha(app, token, "error:cancelled")
		writeJSON(w, map[string]bool{"ok": ok})
	})
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}()
	})

	return mux
}

// panelSessionAuth stores opaque, random, short-lived browser sessions in RAM.
// The existing Basic header remains accepted only for explicit API clients.
// No credentials or sessions are persisted to disk, logs, URLs or localStorage.
type panelSessionAuth struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	failures map[string]panelLoginFailures
}
type panelLoginFailures struct {
	count int
	reset time.Time
}

func newPanelSessionAuth() *panelSessionAuth {
	return &panelSessionAuth{sessions: make(map[string]time.Time), failures: make(map[string]panelLoginFailures)}
}

var sharedPanelAuth = newPanelSessionAuth()

func panelCredentials() (string, string) {
	password := os.Getenv("LALUNE_PANEL_PASSWORD")
	if password == "" {
		password = os.Getenv("CSQTT_WEB_PASS")
	}
	user := os.Getenv("LALUNE_PANEL_USER")
	if user == "" {
		user = os.Getenv("CSQTT_WEB_USER")
	}
	if user == "" {
		user = "admin"
	}
	return user, password
}

func panelCredentialsMatch(user, password, actualUser, actualPassword string) bool {
	if actualPassword == "" || len(user) > 100 || len(password) > 512 {
		return false
	}
	hUser := sha256.Sum256([]byte(user))
	hPass := sha256.Sum256([]byte(password))
	expectedUser := sha256.Sum256([]byte(actualUser))
	expectedPass := sha256.Sum256([]byte(actualPassword))
	return subtle.ConstantTimeCompare(hUser[:], expectedUser[:]) == 1 &&
		subtle.ConstantTimeCompare(hPass[:], expectedPass[:]) == 1
}

func (a *panelSessionAuth) sessionOK(r *http.Request) bool {
	cookie, err := r.Cookie("csqtt_core_session")
	if err != nil || len(cookie.Value) != 43 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

func (a *panelSessionAuth) basicOK(r *http.Request) bool {
	user, password, ok := r.BasicAuth()
	if !ok {
		return false
	}
	expectedUser, expectedPassword := panelCredentials()
	return panelCredentialsMatch(user, password, expectedUser, expectedPassword)
}

func panelSameOrigin(r *http.Request) bool {
	parsed, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return parsed.Scheme == scheme && strings.EqualFold(parsed.Host, r.Host)
}

func panelSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Vary", "Cookie, Authorization")
}

func (a *panelSessionAuth) protect(next http.Handler, ui bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panelSecurityHeaders(w)
		_, expectedPassword := panelCredentials()
		if expectedPassword == "" {
			http.Error(w, "panel authentication is not configured", http.StatusServiceUnavailable)
			return
		}
		// A cached HTTP Basic header must not bypass the new browser login.
		browserUI := ui && r.URL.Path != "/api" && !strings.HasPrefix(r.URL.Path, "/api/")
		authenticated := a.sessionOK(r)
		if !authenticated && !browserUI {
			authenticated = a.basicOK(r) // backward-compatible API automation only
		}
		if !authenticated {
			if browserUI && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			} else {
				// Deliberately no WWW-Authenticate: never trigger native browser dialog.
				http.Error(w, "authentication required", http.StatusUnauthorized)
			}
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !panelSameOrigin(r) {
			http.Error(w, "same-origin request required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Keep the original test/API entrypoint while introducing shared browser sessions.
func panelSecurity(next http.Handler) http.Handler { return sharedPanelAuth.protect(next, false) }

func panelLoginClientID(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		// Only the local reverse proxy can set a meaningful X-Real-IP.
		forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP")))
		if forwarded != nil {
			return forwarded.String()
		}
	}
	if ip != nil {
		return ip.String()
	}
	return "unknown"
}

func (a *panelSessionAuth) loginBlocked(client string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.failures[client]
	if !entry.reset.IsZero() && time.Now().After(entry.reset) {
		delete(a.failures, client)
		return false
	}
	return entry.count >= 8
}

func (a *panelSessionAuth) loginFailed(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.failures[client]
	if entry.reset.IsZero() || time.Now().After(entry.reset) {
		entry = panelLoginFailures{reset: time.Now().Add(5 * time.Minute)}
	}
	entry.count++
	// Bounded memory even when a large number of spoofed client IDs is sent.
	if len(a.failures) >= 1024 {
		for key := range a.failures {
			delete(a.failures, key)
			break
		}
	}
	a.failures[client] = entry
}

func (a *panelSessionAuth) createSession(w http.ResponseWriter, r *http.Request, client string) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	session := base64.RawURLEncoding.EncodeToString(secret)
	expiry := time.Now().Add(8 * time.Hour)
	a.mu.Lock()
	for key, until := range a.sessions {
		if time.Now().After(until) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 256 {
		for key := range a.sessions {
			delete(a.sessions, key)
			break
		}
	}
	a.sessions[session] = expiry
	delete(a.failures, client)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: "csqtt_core_session", Value: session, Path: "/",
		MaxAge: 8 * 3600, Expires: expiry, HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (a *panelSessionAuth) loginPage(w http.ResponseWriter, r *http.Request, files fs.FS) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, password := panelCredentials()
	if password == "" {
		http.Error(w, "panel authentication is not configured", http.StatusServiceUnavailable)
		return
	}
	if a.sessionOK(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// Login assets are embedded locally: no network dependencies or CDN/fonts.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	data, err := fs.ReadFile(files, "login.html")
	if err != nil {
		http.Error(w, "login page unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (a *panelSessionAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !panelSameOrigin(r) {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	_, configuredPassword := panelCredentials()
	if configuredPassword == "" {
		http.Error(w, "panel authentication is not configured", http.StatusServiceUnavailable)
		return
	}
	client := panelLoginClientID(r)
	if a.loginBlocked(client) {
		http.Redirect(w, r, "/login?error=2", http.StatusSeeOther)
		return
	}
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" &&
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded;") {
		http.Error(w, "expected form data", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid login form", http.StatusBadRequest)
		return
	}
	username, password := r.PostForm.Get("username"), r.PostForm.Get("password")
	configuredUser, configuredPassword := panelCredentials()
	if !panelCredentialsMatch(username, password, configuredUser, configuredPassword) {
		a.loginFailed(client)
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	if err := a.createSession(w, r, client); err != nil {
		http.Error(w, "cannot start session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *panelSessionAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !panelSameOrigin(r) {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie("csqtt_core_session"); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: "csqtt_core_session", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func panelUIAuth(next http.Handler, files fs.FS) http.Handler {
	return sharedPanelAuth.ui(next, files)
}

func (a *panelSessionAuth) ui(next http.Handler, files fs.FS) http.Handler {
	protected := a.protect(next, true)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/favicon.svg", "/favicon.ico", "/apple-touch-icon.png":
			// Public, non-sensitive brand artwork must load before login.
			panelSecurityHeaders(w)
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/favicon.svg":
				w.Header().Set("Content-Type", "image/svg+xml")
			case "/favicon.ico":
				w.Header().Set("Content-Type", "image/x-icon")
			case "/apple-touch-icon.png":
				w.Header().Set("Content-Type", "image/png")
			}
			http.ServeFileFS(w, r, files, strings.TrimPrefix(r.URL.Path, "/"))
		case "/login":
			a.loginPage(w, r, files)
		case "/auth/login":
			a.handleLogin(w, r)
		case "/auth/logout":
			a.handleLogout(w, r)
		default:
			protected.ServeHTTP(w, r)
		}
	})
}

// panelAPIHandler maps the public /api prefix to backend routes. Sharing it
// with tests keeps the browser's public URL and protected handler in sync.
func panelAPIHandler(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		u := *r.URL
		u.Path = strings.TrimPrefix(r.URL.Path, "/api")
		if u.Path == "" {
			u.Path = "/"
		}
		rr := r.Clone(r.Context())
		rr.URL = &u
		api.ServeHTTP(w, rr)
	})
}

func panelListenAddress() (string, error) {
	port := strings.TrimSpace(os.Getenv("CSQTT_LALUNE_WEB_PORT"))
	if port == "" {
		return panelListen, nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid CSQTT_LALUNE_WEB_PORT %q", port)
	}
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(n)), nil
}

// CSQTT Core release channel. Never invoke inherited Endlad2/csqtt-core updaters.
const csqttUpdateRepo = "Xenofan-git/CSQTT-Core"
const csqttUpdateBranch = "test/csqtt-core-linux-unified-253-20261122"
const csqttUpdateBinary = "/usr/local/libexec/csqtt-core/bin/csqtt-core-headless"
const csqttUpdateRoot = "/var/lib/csqtt-core/updates"
const csqttUpdateAssetName = "csqtt-core-headless-linux-arm64"

var csqttGitHubBase = "https://api.github.com"
var csqttHTTP = &http.Client{Timeout: 12 * time.Second}
var csqttUpdateLock sync.Mutex

type csqttAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	URL    string `json:"browser_download_url"`
}
type csqttRelease struct {
	Tag        string       `json:"tag_name"`
	Draft      bool         `json:"draft"`
	Prerelease bool         `json:"prerelease"`
	Assets     []csqttAsset `json:"assets"`
}
type csqttUpdateInfo struct {
	Repository      string `json:"repository"`
	SourceBranch    string `json:"sourceBranch"`
	InstalledCommit string `json:"installedCommit"`
	SourceCommit    string `json:"sourceCommit,omitempty"`
	SourceAhead     bool   `json:"sourceAhead"`
	ReleaseTag      string `json:"releaseTag,omitempty"`
	Version         string `json:"version,omitempty"`
	Update          bool   `json:"update"`
	CanInstall      bool   `json:"canInstall"`
	Message         string `json:"message"`
	Error           string `json:"error,omitempty"`
	asset           csqttAsset
	releaseCommit   string
}

func csqttGithubGet(ctx context.Context, link string, out any) error {
	req, e := http.NewRequestWithContext(ctx, "GET", link, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "CSQTT-Core-LinuxARM64/1")
	res, e := csqttHTTP.Do(req)
	if e != nil && strings.HasPrefix(link, "https://api.github.com/") {
		// Preserve GitHub TLS verification when using the app-owned
		// Sing-box SOCKS5 as a fallback. No global routing changes.
		proxyURL, _ := url.Parse("socks5://127.0.0.1:19180")
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		fallback := &http.Client{Timeout: 12 * time.Second, Transport: transport}
		res, e = fallback.Do(req.Clone(ctx))
		transport.CloseIdleConnections()
	}
	if e != nil {
		return fmt.Errorf("GitHub API недоступен по HTTPS и SOCKS5: %w", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("GitHub HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
func csqttSHA(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, 128<<20+1)); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func csqttAssetURL(link, tag string) bool {
	u, e := url.Parse(link)
	if e != nil {
		return false
	}
	return u.Scheme == "https" && u.Host == "github.com" && u.RawQuery == "" && u.Fragment == "" &&
		strings.HasPrefix(u.Path, "/Xenofan-git/CSQTT-Core/releases/download/"+tag+"/") &&
		strings.HasSuffix(u.Path, "/"+csqttUpdateAssetName)
}
func csqttApprovedAsset(r csqttRelease) (csqttAsset, bool) {
	if r.Draft || r.Prerelease || !regexp.MustCompile(`^csqtt-core-v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(r.Tag) {
		return csqttAsset{}, false
	}
	for _, a := range r.Assets {
		if a.Name != csqttUpdateAssetName || a.Size < 5<<20 || a.Size > 128<<20 || !strings.HasPrefix(a.Digest, "sha256:") {
			continue
		}
		sha := strings.TrimPrefix(a.Digest, "sha256:")
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(sha) || !csqttAssetURL(a.URL, r.Tag) {
			continue
		}
		return a, true
	}
	return csqttAsset{}, false
}
func checkOurCSQTTCoreUpdate(ctx context.Context) csqttUpdateInfo {
	o := csqttUpdateInfo{Repository: csqttUpdateRepo, SourceBranch: csqttUpdateBranch, InstalledCommit: buildCommit}
	var head struct {
		SHA string `json:"sha"`
	}
	if e := csqttGithubGet(ctx, csqttGitHubBase+"/repos/"+csqttUpdateRepo+"/commits/"+url.PathEscape(csqttUpdateBranch), &head); e != nil {
		o.Error = "Не удалось проверить исходники CSQTT Core: " + e.Error()
		o.Message = o.Error
		return o
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(head.SHA) {
		o.Error = "Некорректный commit SHA GitHub"
		o.Message = o.Error
		return o
	}
	o.SourceCommit = head.SHA
	o.SourceAhead = head.SHA != buildCommit
	var releases []csqttRelease
	if e := csqttGithubGet(ctx, csqttGitHubBase+"/repos/"+csqttUpdateRepo+"/releases?per_page=30", &releases); e != nil {
		o.Error = "Не удалось проверить релизы CSQTT Core: " + e.Error()
		o.Message = o.Error
		return o
	}
	for _, r := range releases {
		asset, ok := csqttApprovedAsset(r)
		if !ok {
			continue
		}
		// The installed code must be an ancestor of the release tag. This
		// blocks stale releases, unrelated forks and accidental downgrades.
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(buildCommit) {
			o.Error = "Неизвестен commit работающего CSQTT Core — обновление запрещено"
			o.Message = o.Error
			return o
		}
		var cmp struct {
			Status string `json:"status"`
		}
		cmpURL := csqttGitHubBase + "/repos/" + csqttUpdateRepo + "/compare/" + buildCommit + "..." + url.PathEscape(r.Tag)
		if e := csqttGithubGet(ctx, cmpURL, &cmp); e != nil {
			o.Error = "Невозможно проверить историю релиза: " + e.Error()
			o.Message = o.Error
			return o
		}
		if cmp.Status != "ahead" && cmp.Status != "identical" {
			continue
		}
		var releaseHead struct {
			SHA string `json:"sha"`
		}
		if e := csqttGithubGet(ctx, csqttGitHubBase+"/repos/"+csqttUpdateRepo+"/commits/"+url.PathEscape(r.Tag), &releaseHead); e != nil {
			o.Error = "Невозможно подтвердить коммит релиза: " + e.Error()
			o.Message = o.Error
			return o
		}
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(releaseHead.SHA) {
			o.Error = "Некорректный коммит релиза"
			o.Message = o.Error
			return o
		}
		current, e := csqttSHA(csqttUpdateBinary)
		if e != nil {
			o.Message = "Нет доступа к установленному контроллеру — обновление запрещено"
			return o
		}
		o.ReleaseTag = r.Tag
		o.Version = r.Tag
		o.asset = asset
		o.releaseCommit = releaseHead.SHA
		if current == strings.TrimPrefix(asset.Digest, "sha256:") {
			o.Message = "Проверенный ARM64-релиз " + r.Tag + " уже установлен"
			return o
		}
		o.Update = true
		o.CanInstall = true
		o.Message = "Доступен проверенный контроллер " + r.Tag + " (Sing-box отдельно)"
		return o
	}
	if o.SourceAhead {
		o.Message = "Исходники обновились, но совместимый опубликованный ARM64-релиз отсутствует. Установка заблокирована."
	} else {
		o.Message = "Исходники CSQTT Core актуальны. Подходящего опубликованного ARM64-релиза пока нет."
	}
	return o
}
func readCSQTTCoreUpdateStatus() map[string]any {
	b, e := os.ReadFile(filepath.Join(csqttUpdateRoot, "last-status"))
	if e != nil {
		return map[string]any{"downloading": false, "state": "idle"}
	}
	state := strings.TrimSpace(string(b))
	busy := strings.HasPrefix(state, "scheduled:") || strings.HasPrefix(state, "applying:") || strings.HasPrefix(state, "rollback:")
	return map[string]any{"downloading": busy, "state": state}
}

func csqttDownloadAsset(ctx context.Context, a csqttAsset, tag, target string) error {
	if !csqttAssetURL(a.URL, tag) {
		return fmt.Errorf("недоверенный URL релиза")
	}
	client := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(req *http.Request, history []*http.Request) error {
		host := req.URL.Hostname()
		if len(history) > 5 || req.URL.Scheme != "https" || (host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
			return fmt.Errorf("недоверенный редирект загрузки")
		}
		return nil
	}}
	req, e := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "CSQTT-Core-ARM64-Updater/1")
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("скачивание HTTP %d", res.StatusCode)
	}
	f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, 128<<20+1))
	if e != nil {
		return e
	}
	if n != a.Size || n > 128<<20 {
		return fmt.Errorf("неверный размер файла релиза")
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.TrimPrefix(a.Digest, "sha256:") {
		return fmt.Errorf("SHA-256 не совпадает")
	}
	if e = f.Sync(); e != nil {
		return e
	}
	return f.Chmod(0700)
}
func csqttValidateCandidate(path, expectedCommit string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	header := make([]byte, 20)
	if _, e = io.ReadFull(f, header); e != nil {
		return e
	}
	if string(header[:4]) != "\x7fELF" || header[4] != 2 || header[5] != 1 || binary.LittleEndian.Uint16(header[18:20]) != 183 {
		return fmt.Errorf("артефакт не является Linux ARM64 ELF")
	}
	info, e := buildinfo.ReadFile(path)
	if e != nil {
		return fmt.Errorf("отсутствуют Go build сведения: %w", e)
	}
	if info.Path != "lalune-desktop" {
		return fmt.Errorf("неподходящий модуль контроллера")
	}
	linux, arm := false, false
	for _, v := range info.Settings {
		if v.Key == "GOOS" && v.Value == "linux" {
			linux = true
		}
		if v.Key == "GOARCH" && v.Value == "arm64" {
			arm = true
		}
	}
	if !linux || !arm {
		return fmt.Errorf("нет проверенных GOOS/GOARCH метаданных")
	}
	// Go 1.26 does not expose -X main.buildCommit in debug/buildinfo,
	// but the linker puts the exact 40-byte SHA into the ELF data section.
	// Require the release-tag commit within that ELF after SHA-256 verification.
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(expectedCommit) {
		return fmt.Errorf("недопустимый GitHub commit SHA")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(expectedCommit)) {
		return fmt.Errorf("коммит ELF не совпадает с коммитом тега GitHub")
	}
	return nil
}
func prepareVerifiedCSQTTCoreUpdate(ctx context.Context) (map[string]any, error) {
	if !csqttUpdateLock.TryLock() {
		return nil, fmt.Errorf("обновление уже подготавливается")
	}
	defer csqttUpdateLock.Unlock()
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Geteuid() != 0 {
		return nil, fmt.Errorf("обновление разрешено только root на Linux ARM64")
	}
	exe, e := os.Executable()
	if e != nil || exe != csqttUpdateBinary {
		return nil, fmt.Errorf("неподтвержденный путь CSQTT Core")
	}
	result := checkOurCSQTTCoreUpdate(ctx)
	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}
	if !result.CanInstall {
		return nil, fmt.Errorf("нет совместимого опубликованного ARM64-релиза")
	}
	tag, a := result.ReleaseTag, result.asset
	parent := filepath.Join(csqttUpdateRoot, tag)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return nil, e
	}
	if e = os.Chmod(parent, 0700); e != nil {
		return nil, e
	}
	// Per-attempt private directory prevents another check from replacing
	// an asset while the detached systemd updater validates it.
	dir, e := os.MkdirTemp(parent, "candidate-")
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(dir, 0700); e != nil {
		return nil, e
	}
	candidate := filepath.Join(dir, "candidate")
	if e = csqttDownloadAsset(ctx, a, tag, candidate); e != nil {
		_ = os.Remove(candidate)
		return nil, e
	}
	if e = csqttValidateCandidate(candidate, result.releaseCommit); e != nil {
		_ = os.Remove(candidate)
		return nil, e
	}
	script := filepath.Join(dir, "apply.sh")
	if e = os.WriteFile(script, []byte(csqttApplyScript), 0700); e != nil {
		return nil, e
	}
	sha := strings.TrimPrefix(a.Digest, "sha256:")
	unit := "csqtt-core-upgrade-" + sha[:12] + "-" + strconv.FormatInt(time.Now().Unix(), 10)
	_ = os.WriteFile(filepath.Join(csqttUpdateRoot, "last-status"), []byte("scheduled:"+tag+"\n"), 0600)
	cmd := exec.Command("systemd-run", "--collect", "--unit="+unit, "--property=Type=oneshot", "/bin/bash", script, candidate, sha)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.WriteFile(filepath.Join(csqttUpdateRoot, "last-status"), []byte("failed-to-start:"+tag+"\n"), 0600)
		return nil, fmt.Errorf("не удалось запустить отдельную systemd-службу: %v %s", err, strings.TrimSpace(string(output)))
	}
	return map[string]any{"ok": true, "scheduled": true, "releaseTag": tag, "message": "Обновление запущено отдельной systemd-службой; при ошибке будет выполнен откат"}, nil
}

const csqttApplyScript = `#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
exec 9>/run/csqtt-core-release-update.lock
flock -n 9
TARGET=/usr/local/libexec/csqtt-core/bin/csqtt-core-headless
STATE=/var/lib/csqtt-core/updates/last-status
CANDIDATE="$1"
SHA="$2"
[[ "$CANDIDATE" == /var/lib/csqtt-core/updates/csqtt-core-v*/candidate-*/candidate ]]
[[ "$SHA" =~ ^[0-9a-f]{64}$ ]]
[[ "$(sha256sum "$CANDIDATE" | cut -d' ' -f1)" == "$SHA" ]]
[[ "$(id -u)" == 0 ]]
[[ -f "$TARGET" ]]
[[ -f /etc/systemd/system/csqtt-core.service ]]
for service in csqtt-core.service hydraroute-core.service m-ui.service tailscale-xray-proxy.service; do
 systemctl is-active --quiet "$service"
done
BEFORE_ROUTE="$(ip -4 route show default)"
BACKUP="$(dirname "$CANDIDATE")/backup-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 0700 "$BACKUP"
cp -p "$TARGET" "$BACKUP/csqtt-core-headless.previous"
cp -p /etc/systemd/system/csqtt-core.service "$BACKUP/csqtt-core.service"
OLD="$(sha256sum "$BACKUP/csqtt-core-headless.previous" | cut -d' ' -f1)"
echo "applying:$SHA" > "$STATE"
rollback() {
 rc="$?"
 trap - ERR
 echo "rollback:$SHA" > "$STATE"
 install -m 0755 "$BACKUP/csqtt-core-headless.previous" "$TARGET.rollback"
 mv -f "$TARGET.rollback" "$TARGET"
 if systemctl restart csqtt-core.service &&
    [[ "$(sha256sum "$TARGET" | cut -d' ' -f1)" == "$OLD" ]] &&
    systemctl is-active --quiet csqtt-core.service &&
    [[ "$(ip -4 route show default)" == "$BEFORE_ROUTE" ]]; then
   echo "rolled-back:$SHA" > "$STATE"
 else
   echo "rollback-failed:$SHA" > "$STATE"
 fi
 echo "CSQTT_CORE_RELEASE_ROLLBACK old=$OLD new=$SHA rc=$rc"
 exit "$rc"
}
trap rollback ERR
install -m 0755 "$CANDIDATE" "$TARGET.new"
[[ "$(sha256sum "$TARGET.new" | cut -d' ' -f1)" == "$SHA" ]]
mv -f "$TARGET.new" "$TARGET"
systemctl restart csqtt-core.service
healthy=false
for attempt in $(seq 1 20); do
 if systemctl is-active --quiet csqtt-core.service &&
  [[ "$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:1061/login)" == 200 ]] &&
  ss -lnt | grep -q '127.0.0.1:19180'; then
  healthy=true
  break
 fi
 sleep 1
done
[[ "$healthy" == true ]]
[[ "$(sha256sum "$TARGET" | cut -d' ' -f1)" == "$SHA" ]]
[[ "$(ip -4 route show default)" == "$BEFORE_ROUTE" ]]
for service in hydraroute-core.service m-ui.service tailscale-xray-proxy.service; do
 systemctl is-active --quiet "$service"
done
echo "success:$SHA" > "$STATE"
echo "CSQTT_CORE_RELEASE_UPDATE_OK old=$OLD new=$SHA"
`

func main() {
	configuredPanelListen, listenErr := panelListenAddress()
	if listenErr != nil {
		panic(listenErr)
	}
	app := NewApp()
	// The CSQTT Linux runner owns both eth1 and loopback SOCKS5 listeners.
	// The loopback listener is started only after the TUN packet bridge is ready;
	// sing-box connects to it directly, with no extra TCP forwarding process.
	app.startup(context.Background())
	// Explicit opt-in only: a manually stopped Sing-box must not start
	// after reboot unless the systemd unit deliberately enables it.
	if err := defaultSingBoxManager().AutoStartConfiguredOnBoot(
		context.Background(), strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_AUTOSTART"))); err != nil {
		app.core.AddLog(fmt.Sprintf("[sing-box] Автозапуск отклонён: %v", err))
	}

	api := apiHandler(app)
	apiRoutes := panelAPIHandler(api)
	secureAPI := panelSecurity(api)
	srv := &http.Server{Addr: headlessListen, Handler: secureAPI, ReadHeaderTimeout: 5 * time.Second}
	panelFS, _ := fs.Sub(panelAssets, "frontend")
	panelHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			apiRoutes.ServeHTTP(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(panelFS, p); err != nil {
			p = "index.html"
		}
		http.ServeFileFS(w, r, panelFS, p)
	})
	panelSrv := &http.Server{Addr: configuredPanelListen, Handler: panelUIAuth(panelHandler, panelFS), ReadHeaderTimeout: 5 * time.Second}

	errCh := make(chan error, 2)
	go func() {
		app.core.AddLog(fmt.Sprintf("[INFO] Headless API: http://%s", headlessListen))
		errCh <- srv.ListenAndServe()
	}()
	go func() {
		app.core.AddLog(fmt.Sprintf("[INFO] CSQTT Core panel: http://%s", configuredPanelListen))
		errCh <- panelSrv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		app.core.AddLog(fmt.Sprintf("[INFO] Shutdown signal: %s", sig))
	case err := <-errCh:
		app.core.AddLog(fmt.Sprintf("[ERROR] Headless API stopped: %v", err))
	}

	if err := defaultSingBoxManager().Stop(); err != nil {
		app.core.AddLog(fmt.Sprintf("[sing-box] Shutdown error: %v", err))
	}
	_ = app.Disconnect()
	_ = srv.Shutdown(context.Background())
	_ = panelSrv.Shutdown(context.Background())
}
