// SPDX-FileCopyrightText: 2026 luminescq
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
//
// CSQTT Core Linux DeployManager bridge. Credentials are sent exclusively
// through child stdin, never through argv, logs or URLs.

package libs

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DeployRequest — данные, приходящие из UI (вкладка "Деплой").
type DeployRequest struct {
	Action        string `json:"action,omitempty"`
	Protocol      string `json:"protocol"`
	Host          string `json:"host"`
	SSHPort       int    `json:"sshPort"`
	User          string `json:"user"`
	UsePassword   bool   `json:"usePassword"`
	Password      string `json:"password"`
	KeyPath       string `json:"keyPath"`
	ManualPorts   bool   `json:"manualPorts"`
	CorePort      int    `json:"corePort"`
	WarpPort      int    `json:"warpPort"`
	ListenPort    int    `json:"listenPort"`
	MainPassword  string `json:"mainPassword"`
	WebUser       string `json:"webUser"`
	WebPassword   string `json:"webPassword"`
	DeviceID      string `json:"deviceId"`
	Uninstall     bool   `json:"uninstall"`
	DockerInstall bool   `json:"dockerInstall"`
}

var (
	deployMu   sync.Mutex
	deployLog  strings.Builder
	deployBusy bool
)

// deployManagerName — имя бинаря DeployManager для текущей ОС.
func deployManagerName() string {
	if runtime.GOOS == "windows" {
		return "deploy-manager.exe"
	}
	return "deploy-manager"
}

// Only the individually reviewed ARM64 inventory-only manager can receive
// the controller's saved SSH and application credentials in this experiment.
// Any change to the helper requires updating and re-reviewing this pin.
// Separate experimental pin; it is usable only with an operator-bound lab scope.
const verifiedARM64LifecycleManagerSHA = "03d312495b9382d1c61966ae14276d947b346d83e8514a62671546ccd3ff4bc1"

func lifecycleScopeConfigured() bool {
	return os.Getenv("CSQTT_CORE_ALLOW_MUTATION") == "isolated-lab-only" &&
		regexp.MustCompile("^[0-9.]+:[0-9]{1,5}$").MatchString(os.Getenv("CSQTT_CORE_DEPLOY_LAB_TARGET")) &&
		regexp.MustCompile("^[a-f0-9]{32}$").MatchString(os.Getenv("CSQTT_CORE_DEPLOY_LAB_MACHINE_ID"))
}
func isVerifiedLifecycleManager(path string) bool {
	if !lifecycleScopeConfigured() {
		return false
	}
	return managerDigestMatches(path, verifiedARM64LifecycleManagerSHA)
}

const verifiedARM64InventoryManagerSHA = "277e98735d6cb87222c9a908a47c193a8917d6535dd5c6958c9aba681356c731"

// validDeployManagerExecutable refuses untrusted file types and writable
// helpers. No implicit $PATH/cwd lookup is allowed for privileged Deploy.
func validDeployManagerExecutable(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if st.Mode().Perm()&0022 != 0 {
		return false
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
		parent.Mode().Perm()&0022 != 0 {
		return false
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0111 == 0 {
		return false
	}
	return true
}

// isVerifiedInventoryManager never executes an unreviewed binary. This
// experimental capability is intentionally limited to Linux ARM64 only.
func isVerifiedInventoryManager(path string) bool {
	return managerDigestMatches(path, verifiedARM64InventoryManagerSHA)
}

func managerDigestMatches(path, digest string) bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" ||
		!validDeployManagerExecutable(path) {
		return false
	}
	fp, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fp.Close()
	hasher := sha256.New()
	if _, err = io.Copy(hasher, fp); err != nil {
		return false
	}
	return hex.EncodeToString(hasher.Sum(nil)) == digest
}

// resolveDeployManagerPath accepts only a SHA-pinned, non-writable helper
// next to the running controller or in its dedicated bin/ directory.
func resolveDeployManagerPath() string {
	name := deployManagerName()
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, c := range []string{filepath.Join(dir, name), filepath.Join(dir, "bin", name)} {
		if isVerifiedInventoryManager(c) || isVerifiedLifecycleManager(c) {
			return c
		}
	}
	return ""
}

func deployAppend(line string) {
	deployMu.Lock()
	deployLog.WriteString(line)
	deployLog.WriteString("\n")
	if deployLog.Len() > 131072 {
		last := deployLog.String()
		deployLog.Reset()
		deployLog.WriteString("[deploy] предыдущие записи усечены\n")
		deployLog.WriteString(last[len(last)-65536:])
	}
	deployMu.Unlock()
}

// DeployProtocol запускает деплой протокола. Возвращает false если
// DeployManager уже запущен или бинарь не найден.
func DeployProtocol(reqJSON string) bool {
	deployMu.Lock()
	if deployBusy {
		deployMu.Unlock()
		return false
	}
	deployBusy = true
	deployLog.Reset()
	deployMu.Unlock()
	started := false
	defer func() {
		if !started {
			deployMu.Lock()
			deployBusy = false
			deployMu.Unlock()
		}
	}()
	var req DeployRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		deployAppend("[deploy] ошибка разбора параметров")
		return false
	}
	if !strings.EqualFold(req.Protocol, "CSQTT") || strings.TrimSpace(req.Host) == "" {
		deployAppend("[deploy] ошибка: разрешён только CSQTT Core с адресом VPS")
		return false
	}
	// Inventory-only is a distinct capability, not a destructive operation.
	if (req.Uninstall || req.Action == "uninstall" || req.Action == "update") && DeployMode() != "install" {
		deployAppend("[deploy] ошибка: удаление недоступно в режиме только чтение")
		return false
	}
	binPath := resolveDeployManagerPath()
	if binPath == "" {
		deployAppend("[deploy] не найден deploy-manager для CSQTT Core")
		return false
	}
	assetPath := filepath.Join(filepath.Dir(binPath), "server-assets")
	lifecycle := isVerifiedLifecycleManager(binPath)
	args := []string{"--assets", assetPath}
	timeout := time.Minute
	if lifecycle {
		args = append([]string{"--isolated-lifecycle"}, args...)
		timeout = 31 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.WaitDelay = time.Second
	defer func() {
		if !started {
			cancel()
		}
	}()
	forwarded, err := deployRequestForMode(reqJSON, lifecycle)
	if err != nil {
		deployAppend("[deploy] ошибка: неверная операция")
		return false
	}
	cmd.Stdin = strings.NewReader(forwarded)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		deployAppend("[deploy] ошибка запуска stdout")
		return false
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		deployAppend("[deploy] ошибка запуска stderr")
		return false
	}
	if err = cmd.Start(); err != nil {
		deployAppend("[deploy] не удалось запустить DeployManager")
		return false
	}
	started = true
	if lifecycle {
		deployAppend("[deploy] Операция CSQTT Core в разрешённой лабораторной среде")
	} else {
		deployAppend("[deploy] Проверка окружения CSQTT Core на " + req.Host + " (только чтение)")
	}
	redact := func(line string) string {
		for _, v := range []string{req.Password, req.MainPassword, req.WebPassword} {
			if v != "" {
				line = strings.ReplaceAll(line, v, "[скрыто]")
			}
		}
		return line
	}
	go func() {
		defer cancel()
		var wg sync.WaitGroup
		read := func(reader io.Reader) {
			defer wg.Done()
			scan := bufio.NewScanner(reader)
			scan.Buffer(make([]byte, 4096), 131072)
			for scan.Scan() {
				deployAppend(redact(scan.Text()))
			}
			if scan.Err() != nil {
				deployAppend("[deploy] ошибка чтения журнала DeployManager")
				_ = cmd.Process.Kill()
				_, _ = io.Copy(io.Discard, reader)
			}
		}
		wg.Add(2)
		go read(stdout)
		go read(stderr)
		wg.Wait()
		err := cmd.Wait()
		if err != nil {
			// Exit 73 is explicitly reserved for successful read-only
			// inspection, followed by a denied mutation. It is never an
			// installation success. Require the actual inventory end record.
			if exitErr, ok := err.(*exec.ExitError); ok && !lifecycle &&
				exitErr.ExitCode() == 73 &&
				strings.Contains(DeployLog(), "CSQTT_INV|end") &&
				strings.Contains(DeployLog(), "CSQTT_ISOLATION_REQUIRED") {
				deployAppend("[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась")
			} else {
				deployAppend("[deploy] завершено с ошибкой: " + err.Error())
			}
		} else if lifecycle && ctx.Err() == nil {
			deployAppend("[deploy] завершено успешно")
		} else {
			// The pinned inventory-only manager must return exit 73. An
			// unexpected zero exit is not a verified operation.
			deployAppend("[deploy] завершено с ошибкой: непредусмотренный код завершения проверки")
		}
		deployMu.Lock()
		deployBusy = false
		deployMu.Unlock()
	}()
	return true
}

// DeployMode returns the only capability supported by this experimental
// manager. Presence of a helper does NOT mean remote mutation was approved.
// Future release candidates must change this only after a full acceptance.
func DeployMode() string {
	path := resolveDeployManagerPath()
	if path != "" && isVerifiedLifecycleManager(path) {
		return "install"
	}
	if path == "" {
		return "unavailable"
	}
	return "inventory-only"
}

// DeploySnapshot returns a consistent process state and its log under one lock.
func DeploySnapshot() (bool, string) {
	deployMu.Lock()
	defer deployMu.Unlock()
	return deployBusy, deployLog.String()
}

// DeployLog возвращает накопленный журнал деплоя.
func DeployLog() string {
	deployMu.Lock()
	defer deployMu.Unlock()
	return deployLog.String()
}

// DeployBusy сообщает, идёт ли деплой сейчас.
func DeployBusy() bool {
	deployMu.Lock()
	defer deployMu.Unlock()
	return deployBusy
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Keep the old pinned helper wire format for read-only requests. The
// experimental action field must never break an existing inventory check.
func deployRequestForMode(raw string, lifecycle bool) (string, error) {
	var request DeployRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return "", err
	}
	if request.Action != "" && request.Action != "install" && request.Action != "update" && request.Action != "uninstall" {
		return "", errors.New("invalid deploy action")
	}
	if lifecycle {
		return raw, nil
	}
	if request.Action == "update" || request.Action == "uninstall" || request.Uninstall {
		return "", errors.New("operation requires lifecycle capability")
	}
	if request.Action == "" {
		return raw, nil
	}
	fields := map[string]json.RawMessage{}
	seen := map[string]bool{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", errors.New("invalid request")
	}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		folded := strings.ToLower(key)
		if err != nil || !ok || seen[folded] {
			return "", errors.New("ambiguous request")
		}
		seen[folded] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", err
		}
		if folded != "action" {
			fields[key] = value
		}
	}
	if _, err := decoder.Token(); err != nil {
		return "", err
	}
	data, err := json.Marshal(fields)
	return string(data), err
}
