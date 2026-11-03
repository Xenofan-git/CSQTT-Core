//go:build linux

package main

import (
    "sync"
    "crypto/subtle"
    "context"
    "embed"
    "io/fs"
    "encoding/json"
    "fmt"
    "net"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "regexp"
    "os/signal"
    "strconv"
    "strings"
    "syscall"
    "time"

    "lalune-desktop/Libs"
)

const headlessListen = "127.0.0.1:1062"
const panelListen = "0.0.0.0:1061"

// Set by the pinned ARM64 build workflow through Go -ldflags.
var (
    buildCommit = "unknown"
    buildRunID = "unknown"
    buildRunNumber = "unknown"
    buildDate = "unknown"
    buildBranch = "unknown"
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
    if err != nil { return "", err }
    dir = filepath.Join(dir, "csqtt-lalune")
    if err := os.MkdirAll(dir, 0700); err != nil { return "", err }
    return filepath.Join(dir, "tunnel-password"), nil
}

func loadTunnelAuthPassword() (string, error) {
    path, err := tunnelAuthFile()
    if err != nil { return "", err }
    data, err := os.ReadFile(path)
    if err != nil { return "", err }
    password := strings.TrimSpace(string(data))
    if password == "" { return "", fmt.Errorf("пароль туннеля не задан") }
    return password, nil
}

func saveTunnelAuthPassword(password string) error {
    password = strings.TrimSpace(password)
    if password == "" {
        path, err := tunnelAuthFile()
        if err != nil { return err }
        if err := os.Remove(path); err != nil && !os.IsNotExist(err) { return err }
        return nil
    }
    if len(password) > 256 || !regexp.MustCompile(`^[a-zA-Z0-9_.!?:#/-]+$`).MatchString(password) {
        return fmt.Errorf("недопустимый формат пароля туннеля")
    }
    path, err := tunnelAuthFile()
    if err != nil { return err }
    f, err := os.CreateTemp(filepath.Dir(path), ".tunnel-password-*")
    if err != nil { return err }
    tmp := f.Name()
    defer os.Remove(tmp)
    if err := f.Chmod(0600); err != nil { f.Close(); return err }
    if _, err := f.WriteString(password); err != nil { f.Close(); return err }
    if err := f.Sync(); err != nil { f.Close(); return err }
    if err := f.Close(); err != nil { return err }
    return os.Rename(tmp, path)
}

func csqttDeviceID(core *libs.AppCore) string {
    if core == nil { return "" }
    var settings map[string]any
    if err := json.Unmarshal([]byte(core.GetSettingsJson()), &settings); err != nil { return "" }
    id, _ := settings["deviceId"].(string)
    return strings.TrimSpace(id)
}

// validateTunnelAuth is shared by connect and reconnect so neither API path
// can bypass the same fail-closed credential and stable-device-ID checks.
func validateTunnelAuth(app *App) error {
    if _, err := loadTunnelAuthPassword(); err != nil {
        return fmt.Errorf("пароль туннеля не задан")
    }
    if app == nil || csqttDeviceID(app.core) == "" {
        return fmt.Errorf("ID устройства LaLune не задан")
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
    if json.Unmarshal([]byte(raw), &logs) != nil { return }
    captchaMu.Lock()
    defer captchaMu.Unlock()
    // The newest CAPTCHA_SOLVE event is authoritative for this moment. This
    // allows a later manual fallback event to supersede an earlier auto event.
    for i := len(logs)-1; i >= 0; i-- {
        parts := strings.SplitN(strings.TrimSpace(logs[i]), "|", 4)
        if len(parts) != 4 || parts[0] != "CAPTCHA_SOLVE" { continue }
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
    captchaMu.Lock(); defer captchaMu.Unlock()
    return map[string]any{"pending": captchaPending, "mode": captchaMode, "redirectUri": captchaRedirectURI, "sessionToken": captchaSessionToken, "updated": captchaUpdated.UnixMilli()}
}

func finishCaptcha(app *App, sessionToken, result string) bool {
    captchaMu.Lock()
    if !captchaPending || sessionToken == "" || sessionToken != captchaSessionToken { captchaMu.Unlock(); return false }
    captchaMu.Unlock()
    result = strings.TrimSpace(result)
    if result == "" || len(result) > 16384 || strings.ContainsAny(result, "\r\n") { return false }
    if app.runner == nil || !app.runner.SubmitCaptchaResult(result) { return false }
    captchaMu.Lock(); captchaHandledSession = sessionToken; captchaPending = false; captchaMode = ""; captchaRedirectURI = ""; captchaSessionToken = ""; captchaUpdated = time.Time{}; captchaMu.Unlock()
    return true
}
var vkAutoMu sync.Mutex
var vkAutoResult = "{\"pending\":false}"
var vkAutoRunning bool

var singBoxManagerOnce sync.Once
var sharedSingBoxManager *SingBoxManager

func defaultSingBoxManager() *SingBoxManager {
	singBoxManagerOnce.Do(func() {
		dir, err := os.UserConfigDir()
		if err != nil { dir = os.TempDir() }
		dir = filepath.Join(dir, "csqtt-lalune", "sing-box")
		binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
		if binary == "" { binary = "sing-box" }
		sharedSingBoxManager = NewSingBoxManager(binary, filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	})
	return sharedSingBoxManager
}

func apiHandler(app *App) http.Handler { return apiHandlerWithSingBox(app, defaultSingBoxManager()) }

func apiHandlerWithSingBox(app *App, singBox *SingBoxManager) http.Handler {
    mux := http.NewServeMux()

    mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, map[string]any{"ok": true})
    })

    mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, map[string]any{
            "api": 1,
            "backend": "original-go",
            "core": "csqtt-server/2.1.9",
            "ui": "0.6.0",
            "commit": buildCommit,
            "buildRun": buildRunID,
            "buildNumber": buildRunNumber,
            "buildDate": buildDate,
            "buildBranch": buildBranch,
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
        if !method(w, r, http.MethodDelete) { return }
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
        if !method(w, r, http.MethodPost) { return }
        http.Error(w, "settings reset is not implemented by pinned LaLune", http.StatusNotImplemented)
    })


    mux.HandleFunc("/tunnel/auth", func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet:
            _, err := loadTunnelAuthPassword()
            writeJSON(w, map[string]bool{"configured": err == nil})
        case http.MethodPut, http.MethodPost:
            var body tunnelAuthBody
            if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
                http.Error(w, "bad json", http.StatusBadRequest); return
            }
            if err := saveTunnelAuthPassword(body.Password); err != nil {
                http.Error(w, err.Error(), http.StatusBadRequest); return
            }
            writeJSON(w, map[string]bool{"ok": true, "configured": strings.TrimSpace(body.Password) != ""})
        default:
            w.Header().Set("Allow", "GET, PUT, POST")
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        }
    })

    mux.HandleFunc("/vpn/status", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeRaw(w, app.GetStatusJson())
    })

    mux.HandleFunc("/vpn/connect", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        var body connectBody
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            http.Error(w, "bad json", http.StatusBadRequest)
            return
        }
        if err := validateTunnelAuth(app); err != nil { http.Error(w, err.Error(), http.StatusPreconditionRequired); return }
        writeJSON(w, map[string]bool{"ok": app.Connect(body.ID)})
    })

    mux.HandleFunc("/vpn/disconnect", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": app.Disconnect()})
    })

    mux.HandleFunc("/vpn/reconnect", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        var body connectBody
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            http.Error(w, "bad json", http.StatusBadRequest)
            return
        }
        if err := validateTunnelAuth(app); err != nil { http.Error(w, err.Error(), http.StatusPreconditionRequired); return }
        _ = app.Disconnect()
        writeJSON(w, map[string]bool{"ok": app.Connect(body.ID)})
    })

    mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeRaw(w, app.GetLogsJson())
    })

    mux.HandleFunc("/logs/tail", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeRaw(w, app.GetLogsJson())
    })

    mux.HandleFunc("/logs/clear", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": app.ClearLogs()})
    })

    mux.HandleFunc("/socks5/status", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
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
        if !method(w, r, http.MethodGet) { return }
        if !privateClient(r) { http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden); return }
        status := singBox.Status()
        _, err := os.Stat(singBox.configPath)
        writeJSON(w, map[string]any{"running": status.Running, "pid": status.PID, "configured": err == nil, "log_path": status.LogPath, "error": status.Error})
    })
    mux.HandleFunc("/singbox/config", func(w http.ResponseWriter, r *http.Request) {
        if !privateClient(r) { http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden); return }
        switch r.Method {
        case http.MethodGet:
            input, err := singBox.LoadConfig()
            if err != nil {
                if os.IsNotExist(err) { writeJSON(w, map[string]any{"configured": false}); return }
                http.Error(w, "unable to load sing-box config: "+err.Error(), http.StatusInternalServerError); return
            }
            writeJSON(w, map[string]any{"configured": true, "config": input})
        case http.MethodPut:
            var input singBoxConfigInput
            decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
            decoder.DisallowUnknownFields()
            if err := decoder.Decode(&input); err != nil { http.Error(w, "invalid sing-box config request", http.StatusBadRequest); return }
            if err := singBox.SaveConfig(input); err != nil { http.Error(w, err.Error(), http.StatusUnprocessableEntity); return }
            writeJSON(w, map[string]bool{"ok": true})
        default:
            w.Header().Set("Allow", "GET, PUT")
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        }
    })
    mux.HandleFunc("/singbox/start", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        if !privateClient(r) { http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden); return }
        if err := singBox.Start(r.Context()); err != nil { http.Error(w, err.Error(), http.StatusConflict); return }
        writeJSON(w, map[string]bool{"ok": true})
    })
    mux.HandleFunc("/singbox/stop", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        if !privateClient(r) { http.Error(w, "sing-box control is available only from LAN or Tailscale", http.StatusForbidden); return }
        if err := singBox.Stop(); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
        writeJSON(w, map[string]bool{"ok": true})
    })
    mux.HandleFunc("/singbox/logs", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        if !privateClient(r) { http.Error(w, "sing-box logs are available only from LAN or Tailscale", http.StatusForbidden); return }
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        if err := singBox.CopyLogs(w); err != nil {
            if os.IsNotExist(err) { w.WriteHeader(http.StatusNoContent); return }
            http.Error(w, "unable to read sing-box log", http.StatusInternalServerError)
        }
    })

    mux.HandleFunc("/deploy", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        if !privateClient(r) {
            http.Error(w, "deployment is available only from LAN or Tailscale", http.StatusForbidden)
            return
        }
        var raw json.RawMessage
        if err := json.NewDecoder(r.Body).Decode(&raw); err != nil { http.Error(w, "bad json", http.StatusBadRequest); return }
        writeJSON(w, map[string]bool{"ok": libs.DeployProtocol(string(raw))})
    })
    mux.HandleFunc("/deploy/log", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        if !privateClient(r) {
            http.Error(w, "deployment status is available only from LAN or Tailscale", http.StatusForbidden)
            return
        }
        writeRaw(w, mustJSON(libs.DeployLog()))
    })
    mux.HandleFunc("/deploy/status", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        if !privateClient(r) {
            http.Error(w, "deployment status is available only from LAN or Tailscale", http.StatusForbidden)
            return
        }
        writeJSON(w, map[string]bool{"deploying": libs.DeployBusy()})
    })

    mux.HandleFunc("/updates/core", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": app.UpdateCore()})
    })
    mux.HandleFunc("/updates/core/wait", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": app.UpdateCoreAndWait()})
    })
    mux.HandleFunc("/updates/core/check", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeRaw(w, app.CheckUpdate())
    })
    mux.HandleFunc("/updates/core/status", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, map[string]bool{"downloading": app.IsCoreDownloading()})
    })
    mux.HandleFunc("/updates/lalune", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, app.CheckLaLuneUpdate())
    })
    mux.HandleFunc("/vk/state", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, app.GetVKTokenState())
    })
    mux.HandleFunc("/vk/login", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": app.LoginVK()})
    })
    mux.HandleFunc("/vk/import", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        if !privateClient(r) { http.Error(w, "forbidden", http.StatusForbidden); return }
        var req struct { Token string `json:"token"` }
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
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": app.DeleteVKToken()})
    })
    mux.HandleFunc("/vk/validate", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, app.ValidateVKToken())
    })
    mux.HandleFunc("/vk/auto", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        vkAutoMu.Lock()
        if vkAutoRunning { vkAutoMu.Unlock(); writeRaw(w, "{\"pending\":true}"); return }
        vkAutoRunning = true; vkAutoResult = "{\"pending\":true}"; vkAutoMu.Unlock()
        go func() { result := app.RunVkAutoApiCalls(); vkAutoMu.Lock(); vkAutoResult = result; vkAutoRunning = false; vkAutoMu.Unlock() }()
        writeRaw(w, "{\"pending\":true}")
    })
    mux.HandleFunc("/vk/auto/poll", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        vkAutoMu.Lock(); result := vkAutoResult; vkAutoMu.Unlock(); writeRaw(w, result)
    })
    mux.HandleFunc("/vk/finish", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        var ids []string
        if err := json.NewDecoder(r.Body).Decode(&ids); err != nil { http.Error(w, "bad json", http.StatusBadRequest); return }
        writeJSON(w, map[string]bool{"ok": app.FinishVkCalls(ids)})
    })

    mux.HandleFunc("/captcha/state", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodGet) { return }
        writeJSON(w, captchaState(app))
    })
    mux.HandleFunc("/captcha/result", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        var body struct { SessionToken string `json:"sessionToken"`; Result string `json:"result"` }
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil { http.Error(w, "bad json", http.StatusBadRequest); return }
        writeJSON(w, map[string]bool{"ok": finishCaptcha(app, body.SessionToken, body.Result)})
    })
    mux.HandleFunc("/captcha/cancel", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        captchaMu.Lock(); token := captchaSessionToken; captchaMu.Unlock()
        ok := token != "" && finishCaptcha(app, token, "error:cancelled")
        writeJSON(w, map[string]bool{"ok": ok})
    })
    mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
        if !method(w, r, http.MethodPost) { return }
        writeJSON(w, map[string]bool{"ok": true})
        go func() {
            time.Sleep(100 * time.Millisecond)
            _ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
        }()
    })

    return mux
}


// panelSecurity protects both the UI and every API route with the same
// per-install HTTP Basic credential. The panel is intentionally LAN reachable;
// authentication is therefore mandatory and origin checks prevent browser CSRF.
func panelSecurity(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Cache-Control", "no-store")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Referrer-Policy", "same-origin")

        expected := os.Getenv("LALUNE_PANEL_PASSWORD")
        if expected == "" {
            expected = os.Getenv("CSQTT_WEB_PASS")
        }
        expectedUser := os.Getenv("LALUNE_PANEL_USER")
        if expectedUser == "" {
            expectedUser = os.Getenv("CSQTT_WEB_USER")
        }
        if expectedUser == "" {
            expectedUser = "admin"
        }
        user, password, ok := r.BasicAuth()
        if expected == "" {
            http.Error(w, "panel authentication is not configured", http.StatusServiceUnavailable)
            return
        }
        if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) != 1 ||
            subtle.ConstantTimeCompare([]byte(password), []byte(expected)) != 1 {
            w.Header().Set("WWW-Authenticate", `Basic realm="LaLune CSQTT", charset="UTF-8"`)
            http.Error(w, "authentication required", http.StatusUnauthorized)
            return
        }

        if r.Method != http.MethodGet && r.Method != http.MethodHead {
            origin := r.Header.Get("Origin")
            parsed, err := url.Parse(origin)
            scheme := "http"
            if r.TLS != nil { scheme = "https" }
            if err != nil || parsed.Scheme != scheme || parsed.Host == "" ||
                !strings.EqualFold(parsed.Host, r.Host) || parsed.User != nil {
                http.Error(w, "same-origin request required", http.StatusForbidden)
                return
            }
        }
        next.ServeHTTP(w, r)
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
        if u.Path == "" { u.Path = "/" }
        rr := r.Clone(r.Context())
        rr.URL = &u
        api.ServeHTTP(w, rr)
    })
}

func panelListenAddress() (string, error) {
    port := strings.TrimSpace(os.Getenv("CSQTT_LALUNE_WEB_PORT"))
    if port == "" { return panelListen, nil }
    n, err := strconv.Atoi(port)
    if err != nil || n < 1 || n > 65535 { return "", fmt.Errorf("invalid CSQTT_LALUNE_WEB_PORT %q", port) }
    return net.JoinHostPort("0.0.0.0", strconv.Itoa(n)), nil
}

func main() {
    configuredPanelListen, listenErr := panelListenAddress()
    if listenErr != nil { panic(listenErr) }
    app := NewApp()
    app.startup(context.Background())

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
        if p == "" { p = "index.html" }
        if _, err := fs.Stat(panelFS, p); err != nil { p = "index.html" }
        http.ServeFileFS(w, r, panelFS, p)
    })
    panelSrv := &http.Server{Addr: configuredPanelListen, Handler: panelSecurity(panelHandler), ReadHeaderTimeout: 5 * time.Second}

    errCh := make(chan error, 2)
    go func() { app.core.AddLog(fmt.Sprintf("[INFO] Headless API: http://%s", headlessListen)); errCh <- srv.ListenAndServe() }()
    go func() { app.core.AddLog(fmt.Sprintf("[INFO] LaLune panel: http://%s", configuredPanelListen)); errCh <- panelSrv.ListenAndServe() }()

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