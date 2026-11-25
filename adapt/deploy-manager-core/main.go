package main

// Fail-closed CSQTT Core 2.5 DeployManager adapter. The old LaLune 2.1.9
// installer must not run the CSQTT Core 2.5/2.5.3 server on production VPS.
import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const expectedInstallerSHA = "18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68"
const expectedServerSHA = "8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d"

var ErrIsolationRequired = errors.New("CSQTT_ISOLATION_REQUIRED")

type Request struct {
	Protocol      string
	Host          string
	SSHPort       int
	User          string
	UsePassword   bool
	Password      string
	KeyPath       string
	MainPassword  string
	WebUser       string
	WebPassword   string
	DeviceID      string
	ManualPorts   bool
	CorePort      int
	WarpPort      int
	DockerInstall bool
	Uninstall     bool
}

func alphanumeric(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func validDevice(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
func validTarget(host string) error {
	if len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, " \r\n\t/\\@:;") {
		return errors.New("некорректный адрес VPS")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return errors.New("требуется проверенный IP сервера; DNS-домены пока не допускаются из-за риска смены адреса")
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return errors.New("локальный или зарезервированный адрес не разрешён")
	}
	if ip.IsPrivate() {
		_, ts4, _ := net.ParseCIDR("100.64.0.0/10")
		_, ts6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
		if !(ts4.Contains(ip) || ts6.Contains(ip)) {
			return errors.New("частная сеть не разрешена для удалённого Deploy")
		}
	}
	ifs, _ := net.InterfaceAddrs()
	for _, addr := range ifs {
		var candidate net.IP
		switch a := addr.(type) {
		case *net.IPNet:
			candidate = a.IP
		case *net.IPAddr:
			candidate = a.IP
		}
		if candidate != nil && candidate.Equal(ip) {
			return errors.New("Deploy на собственный адрес устройства запрещён")
		}
	}
	return nil
}
func validate(r Request) error {
	if !strings.EqualFold(r.Protocol, "CSQTT") {
		return errors.New("поддерживается только CSQTT Core, не другие протоколы")
	}
	if err := validTarget(r.Host); err != nil {
		return err
	}
	if r.SSHPort == 0 {
		r.SSHPort = 22
	}
	if r.SSHPort < 1 || r.SSHPort > 65535 {
		return errors.New("неверный SSH-порт")
	}
	if r.User != "root" {
		return errors.New("серверный установщик требует root; непроверенный пользователь запрещён")
	}
	if r.DockerInstall {
		return errors.New("Docker Deploy отключён: нет подтверждённых испытаний")
	}
	if r.ManualPorts && (r.CorePort != 47000 || r.WarpPort != 47002) {
		return errors.New("изолированная сборка поддерживает только UDP47000/TCP47002")
	}
	if r.UsePassword {
		if len(r.Password) == 0 || len(r.Password) > 1024 || strings.ContainsAny(r.Password, "\x00\r\n") {
			return errors.New("SSH-пароль не задан или некорректен")
		}
	} else if len(r.KeyPath) == 0 || !filepath.IsAbs(r.KeyPath) {
		return errors.New("требуется абсолютный путь к SSH-ключу")
	}
	if !r.Uninstall {
		if !alphanumeric(r.MainPassword) || !alphanumeric(r.WebUser) || !alphanumeric(r.WebPassword) || !validDevice(r.DeviceID) {
			return errors.New("неполная авторизация: сохраните пароль туннеля, логин/пароль WEB и ID устройства")
		}
	}
	return nil
}
func shaFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("небезопасный файл: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func checkAssets(folder string) error {
	st, err := os.Lstat(folder)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("server-assets не является обычным каталогом")
	}
	files := []struct{ name, sha string }{
		{"deploy-csqtt-core-250.sh", expectedInstallerSHA},
		{"csqtt-core8-linux-arm64", expectedServerSHA},
	}
	for _, item := range files {
		got, err := shaFile(filepath.Join(folder, item.name))
		if err != nil {
			return err
		}
		if got != item.sha {
			return fmt.Errorf("неподтверждённый SHA256 %s", item.name)
		}
	}
	f, err := elf.Open(filepath.Join(folder, "csqtt-core8-linux-arm64"))
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Machine != elf.EM_AARCH64 {
		return errors.New("серверный бинарник не соответствует Linux ARM64")
	}
	return nil
}
func run(reader io.Reader, writer io.Writer, assets string) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 16384))
	var r Request
	if err := decoder.Decode(&r); err != nil {
		return fmt.Errorf("некорректный запрос Deploy: %w", err)
	}
	if err := validate(r); err != nil {
		return err
	}
	if err := checkAssets(assets); err != nil {
		return fmt.Errorf("пакет серверных компонентов не готов: %w", err)
	}
	// Remote read-only inventory uses the same SHA-pinned installer that
	// passed the prior port-ownership and inventory contracts. No remote
	// file is uploaded and no mutating command is ever called here.
	fmt.Fprintln(writer, "[deploy] Серверный сценарий CSQTT Core 2.5.0/2.5.3 проверен по SHA-256")
	fmt.Fprintln(writer, "[deploy] Проверяем удалённое окружение (только чтение)…")
	if err := remoteInventory(r, filepath.Join(assets, "deploy-csqtt-core-250.sh"), writer); err != nil {
		return fmt.Errorf("не удалось проверить окружение по SSH: %w", err)
	}
	fmt.Fprintln(writer, "[deploy] Инвентаризация получена. Установка на VPS НЕ запускалась")
	return ErrIsolationRequired
}

// remoteInventory passes the authenticated session's password only through
// a child environment variable, never argv, logs, files, or a command string.
// OpenSSH accept-new pins first-contact host keys and rejects changed keys.
func remoteInventory(r Request, installerPath string, writer io.Writer) error {
	script, err := os.ReadFile(installerPath)
	if err != nil {
		return err
	}
	port := r.SSHPort
	if port == 0 {
		port = 22
	}
	args := []string{"-T", "-o", "ConnectTimeout=12", "-o", "ConnectionAttempts=1",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "NumberOfPasswordPrompts=1",
		"-p", strconv.Itoa(port)}
	if r.UsePassword {
		args = append(args, "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "PubkeyAuthentication=no")
	} else {
		args = append(args, "-o", "BatchMode=yes", "-i", r.KeyPath)
	}
	args = append(args, r.User+"@"+r.Host, "bash -s inventory")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = bytes.NewReader(script)
	cmd.Stdout = writer
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if r.UsePassword {
		helper, err := os.CreateTemp("", "csqtt-askpass-*.sh")
		if err != nil {
			return err
		}
		path := helper.Name()
		defer os.Remove(path)
		if err = helper.Chmod(0700); err != nil {
			helper.Close()
			return err
		}
		if _, err = helper.WriteString("#!/bin/sh\nprintf '%s' \"$CSQTT_DEPLOY_SSH_PASSWORD\"\n"); err != nil {
			helper.Close()
			return err
		}
		if err = helper.Close(); err != nil {
			return err
		}
		cmd.Env = append(os.Environ(), "SSH_ASKPASS="+path, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=csqtt:0", "CSQTT_DEPLOY_SSH_PASSWORD="+r.Password)
	}
	if err = cmd.Run(); err != nil {
		// Do not include subprocess output in any error: it can contain keys,
		// terminal escapes or values echoed by an untrusted SSH server.
		return fmt.Errorf("SSH-команда inventory завершилась ошибкой (%s)", cmd.ProcessState.String())
	}
	return nil
}

func main() {
	if len(os.Args) != 3 || os.Args[1] != "--assets" {
		fmt.Fprintln(os.Stderr, "[deploy] использование: deploy-manager --assets /path/to/server-assets (JSON через stdin)")
		os.Exit(2)
	}
	err := run(os.Stdin, os.Stdout, os.Args[2])
	if err != nil {
		if errors.Is(err, ErrIsolationRequired) {
			fmt.Fprintln(os.Stderr, "[deploy] CSQTT_ISOLATION_REQUIRED: production-режим установщика не прошёл приёмку; изменять VPS запрещено")
			os.Exit(73)
		}
		fmt.Fprintln(os.Stderr, "[deploy] ошибка:", err)
		os.Exit(2)
	}
}
