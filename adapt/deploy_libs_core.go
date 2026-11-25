// SPDX-FileCopyrightText: 2026 luminescq
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
//
// CSQTT Core Linux DeployManager bridge. Credentials are sent exclusively
// through child stdin, never through argv, logs or URLs.

package libs

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// DeployRequest — данные, приходящие из UI (вкладка "Деплой").
type DeployRequest struct {
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
	return hex.EncodeToString(hasher.Sum(nil)) == verifiedARM64InventoryManagerSHA
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
		if isVerifiedInventoryManager(c) {
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
	binPath := resolveDeployManagerPath()
	if binPath == "" {
		deployAppend("[deploy] не найден deploy-manager для CSQTT Core")
		return false
	}
	assetPath := filepath.Join(filepath.Dir(binPath), "server-assets")
	cmd := exec.Command(binPath, "--assets", assetPath)
	cmd.Stdin = strings.NewReader(reqJSON)
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
	deployAppend("[deploy] Проверка окружения CSQTT Core на " + req.Host + " (только чтение)")
	redact := func(line string) string {
		for _, v := range []string{req.Password, req.MainPassword, req.WebPassword} {
			if v != "" {
				line = strings.ReplaceAll(line, v, "[скрыто]")
			}
		}
		return line
	}
	go func() {
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
			if exitErr, ok := err.(*exec.ExitError); ok &&
				exitErr.ExitCode() == 73 &&
				strings.Contains(DeployLog(), "CSQTT_INV|end") &&
				strings.Contains(DeployLog(), "CSQTT_ISOLATION_REQUIRED") {
				deployAppend("[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась")
			} else {
				deployAppend("[deploy] завершено с ошибкой: " + err.Error())
			}
		} else {
			deployAppend("[deploy] завершено успешно; это НЕ подтверждает установку на сервер")
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
	if resolveDeployManagerPath() == "" {
		return "unavailable"
	}
	return "inventory-only"
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
