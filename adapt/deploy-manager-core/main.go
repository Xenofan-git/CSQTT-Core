package main

// Fail-closed CSQTT Core 2.5 DeployManager adapter. A legacy 2.1.9
// installer must not run the CSQTT Core 2.5/2.5.3 server on production VPS.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const expectedInstallerSHA = "18b8fba447a0f6d931f4acea37612326a75aae567e499b4e52ec7d5f6bc0af68"
const expectedServerSHA = "8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d"

var ErrIsolationRequired = errors.New("CSQTT_ISOLATION_REQUIRED")

type Request struct {
	Action        string
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
	ListenPort    int // Compatibility only; inventory uses fixed ports.
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
	if _, err := requestedLifecycleAction(r); err != nil {
		return err
	}
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
	if !r.Uninstall && r.Action != "uninstall" && r.Action != "update" {
		if !alphanumeric(r.MainPassword) || !alphanumeric(r.WebUser) || !alphanumeric(r.WebPassword) || !validDevice(r.DeviceID) {
			return errors.New("неполная авторизация: сохраните пароль туннеля, логин/пароль WEB и ID устройства")
		}
	}
	return nil
}

// Adapted from CSQTTR v2.5.3 ServerCoreCache: trust only pinned
// asset sizes and hashes. Verify the same bytes that will be used,
// rather than opening a checked pathname a second time.
// No download, upload or installation capability is introduced.
const expectedInstallerSize int64 = 128287
const expectedServerSize int64 = 9589352

func readVerifiedAsset(path string, size int64, digest string) ([]byte, error) {
	if size <= 0 || size > 100_000_000 {
		return nil, errors.New("некорректный размер компонента")
	}
	// O_NOFOLLOW rejects a symlink at open time; O_NONBLOCK prevents a
	// replaced FIFO from hanging before the regular-file check.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return nil, fmt.Errorf("неподтверждённый тип или размер %s", filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(f, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size || fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return nil, fmt.Errorf("неподтверждённый SHA256 или размер %s", filepath.Base(path))
	}
	return data, nil
}

func verifiedInstaller(folder string) ([]byte, error) {
	script, _, err := verifiedAssets(folder)
	return script, err
}

func verifiedAssets(folder string) ([]byte, []byte, error) {
	st, err := os.Lstat(folder)
	if err != nil {
		return nil, nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("server-assets не является обычным каталогом")
	}
	script, err := readVerifiedAsset(filepath.Join(folder, "deploy-csqtt-core-250.sh"), expectedInstallerSize, expectedInstallerSHA)
	if err != nil {
		return nil, nil, err
	}
	server, err := readVerifiedAsset(filepath.Join(folder, "csqtt-core8-linux-arm64"), expectedServerSize, expectedServerSHA)
	if err != nil {
		return nil, nil, err
	}
	f, err := elf.NewFile(bytes.NewReader(server))
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Machine != elf.EM_AARCH64 || f.Data != elf.ELFDATA2LSB {
		return nil, nil, errors.New("серверный бинарник не соответствует Linux ARM64")
	}
	return script, server, nil
}

// Decode exactly one bounded request; reject ambiguous keys before use.
// The controller sends listenPort for compatibility, but inventory does
// not use it. No credential values are included in errors.
func decodeDeployRequest(reader io.Reader) (Request, error) {
	var result Request
	invalid := errors.New("некорректный или неоднозначный запрос Deploy")
	const max = 16384
	data, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil || len(data) > max {
		return result, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return result, invalid
	}
	allowed := map[string]bool{"action": true, "protocol": true, "host": true, "sshport": true, "user": true, "usepassword": true, "password": true, "keypath": true, "mainpassword": true, "webuser": true, "webpassword": true, "deviceid": true, "manualports": true, "coreport": true, "warpport": true, "listenport": true, "dockerinstall": true, "uninstall": true}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		key = strings.ToLower(key)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return result, invalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return result, invalid
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return result, invalid
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return result, invalid
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Request{}, invalid
	}
	return result, nil
}

// Canonicalize validated records to the same field order the panel
// expects, without accepting unknown or missing fields.
func canonicalInventoryRecord(kind string, attrs map[string]string) (string, error) {
	order := map[string][]string{
		"server":    {"place", "runtime", "state", "owner"},
		"port":      {"proto", "port", "state", "owner"},
		"interface": {"name", "state", "owner"},
		"database":  {"state", "name"},
	}[kind]
	invalid := errors.New("неподдерживаемые поля inventory")
	if len(order) == 0 || len(attrs) != len(order) {
		return "", invalid
	}
	if kind == "interface" && attrs["owner"] != "unverified" {
		return "", invalid
	}
	fields := []string{"CSQTT_INV", kind}
	for _, key := range order {
		value, ok := attrs[key]
		if !ok || value == "" {
			return "", invalid
		}
		fields = append(fields, key+"="+value)
	}
	return strings.Join(fields, "|"), nil
}

func run(reader io.Reader, writer io.Writer, assets string) error {
	r, err := decodeDeployRequest(reader)
	if err != nil {
		return err
	}
	if r.Uninstall {
		return errors.New("удаление недоступно в режиме только чтение")
	}
	if err := validate(r); err != nil {
		return err
	}
	script, err := verifiedInstaller(assets)
	if err != nil {
		return fmt.Errorf("пакет серверных компонентов не готов: %w", err)
	}
	// Remote read-only inventory uses the same SHA-pinned installer that
	// passed the prior port-ownership and inventory contracts. No remote
	// file is uploaded and no mutating command is ever called here.
	fmt.Fprintln(writer, "[deploy] Серверный сценарий CSQTT Core 2.5.0/2.5.3 проверен по SHA-256")
	fmt.Fprintln(writer, "[deploy] Проверяем удалённое окружение (только чтение)…")
	if err := remoteInventory(r, script, writer); err != nil {
		return fmt.Errorf("не удалось проверить окружение по SSH: %w", err)
	}
	fmt.Fprintln(writer, "[deploy] Инвентаризация получена. Установка на VPS НЕ запускалась")
	return ErrIsolationRequired
}

// validatedInventory parses a structured, strictly bounded read-only report.
// A well-formed report does NOT authorize install/uninstall.
func validatedInventory(text string) ([]string, error) {
	if len(text) == 0 || len(text) > 65536 {
		return nil, errors.New("пустой или чрезмерный ответ inventory")
	}
	seen := map[string]bool{}
	result := []string{}
	ended := false
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if len(line) > 512 || !strings.HasPrefix(line, "CSQTT_INV|") {
			return nil, errors.New("неожиданная строка inventory")
		}
		for _, ch := range line {
			if ch < 32 || ch > 126 {
				return nil, errors.New("недопустимые символы в inventory")
			}
		}
		if ended {
			return nil, errors.New("лишние строки после CSQTT_INV end")
		}
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			return nil, errors.New("неполная строка inventory")
		}
		kind := fields[1]
		if kind == "error" {
			return nil, errors.New("удалённый сервер сообщил об ошибке inventory")
		}
		if kind == "end" {
			if len(fields) != 2 {
				return nil, errors.New("некорректный end inventory")
			}
			ended = true
			result = append(result, line)
			continue
		}
		attrs := map[string]string{}
		for _, field := range fields[2:] {
			k, v, ok := strings.Cut(field, "=")
			if !ok || k == "" || v == "" {
				return nil, errors.New("пустое поле inventory")
			}
			if _, found := attrs[k]; found {
				return nil, errors.New("повтор поля inventory")
			}
			attrs[k] = v
		}
		key := kind
		switch kind {
		case "server":
			if attrs["place"] != "core" || attrs["runtime"] != "systemd" {
				return nil, errors.New("неожиданный тип серверной службы")
			}
			if attrs["state"] != "running" && attrs["state"] != "stopped" && attrs["state"] != "absent" {
				return nil, errors.New("некорректное состояние службы")
			}
			if (attrs["state"] == "running" || attrs["state"] == "stopped") && attrs["owner"] != "core_candidate" {
				return nil, errors.New("неподтверждённый владелец службы")
			}
			if attrs["state"] == "absent" && attrs["owner"] != "none" {
				return nil, errors.New("неоднозначный владелец отсутствующей службы")
			}
		case "port":
			proto, port := attrs["proto"], attrs["port"]
			if !((proto == "udp" && port == "47000") || (proto == "tcp" && port == "47002")) {
				return nil, errors.New("неожиданный протокол или порт")
			}
			key = kind + ":" + proto + ":" + port
			if attrs["state"] == "free" && attrs["owner"] == "none" {
			} else if attrs["state"] == "busy" && attrs["owner"] == "core_candidate" {
			} else {
				return nil, errors.New("порт занят чужим или неизвестным процессом")
			}
		case "interface":
			if attrs["name"] != "csqtt-core0" || !(attrs["state"] == "present" || attrs["state"] == "absent") {
				return nil, errors.New("неожиданный интерфейс")
			}
			// owner=unverified is informational; never authorizes modification.
		case "database":
			if attrs["name"] != "csqtt.db" || !(attrs["state"] == "present" || attrs["state"] == "absent") {
				return nil, errors.New("неожиданная база данных")
			}
		default:
			return nil, errors.New("неизвестная запись inventory")
		}
		if seen[key] {
			return nil, errors.New("повтор или конфликт записей inventory")
		}
		canonical, err := canonicalInventoryRecord(kind, attrs)
		if err != nil {
			return nil, err
		}
		seen[key] = true
		result = append(result, canonical)
	}
	for _, key := range []string{"server", "port:udp:47000", "port:tcp:47002", "interface", "database"} {
		if !seen[key] {
			return nil, fmt.Errorf("неполный inventory: отсутствует %s", key)
		}
	}
	if !ended {
		return nil, errors.New("inventory не содержит завершающего маркера")
	}
	return result, nil
}

// boundedInventoryBuffer keeps at most 64 KiB of untrusted SSH output.
type boundedInventoryBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

// Do not embed bytes.Buffer: its promoted ReadFrom would allow io.Copy
// (including os/exec's stream copy) to bypass the bounded Write method.
func (b *boundedInventoryBuffer) Len() int       { return b.buffer.Len() }
func (b *boundedInventoryBuffer) String() string { return b.buffer.String() }
func (b *boundedInventoryBuffer) Write(data []byte) (int, error) {
	if len(data) > 65536-b.Len() {
		b.truncated = true
		return 0, errors.New("переполнение SSH inventory")
	}
	return b.buffer.Write(data)
}

// Only bash -s inventory is ever sent. No remote files are copied or changed.
func remoteInventory(r Request, script []byte, writer io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return remoteInventoryWithContext(ctx, r, script, writer)
}
func remoteInventoryWithContext(ctx context.Context, r Request, script []byte, writer io.Writer) error {
	t, err := newRemoteTransport(r)
	if err != nil {
		return err
	}
	defer t.close()
	return t.inventory(ctx, script, writer)
}

func main() {
	args := os.Args[1:]
	stageOnly := len(args) > 0 && args[0] == "--stage-only"
	lifecycle := len(args) > 0 && args[0] == "--isolated-lifecycle"
	if stageOnly || lifecycle {
		args = args[1:]
	}
	if len(args) != 2 || args[0] != "--assets" {
		fmt.Fprintln(os.Stderr, "[deploy] использование: deploy-manager [--stage-only | --isolated-lifecycle] --assets /path/to/server-assets (JSON через stdin)")
		os.Exit(2)
	}
	var err error
	if lifecycle {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err = runRemoteLifecycle(ctx, os.Stdin, os.Stdout, args[1])
	} else if stageOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		err = runRemoteStage(ctx, os.Stdin, os.Stdout, args[1])
	} else {
		err = run(os.Stdin, os.Stdout, args[1])
	}
	if err != nil {
		if errors.Is(err, ErrIsolationRequired) {
			fmt.Fprintln(os.Stderr, "[deploy] CSQTT_ISOLATION_REQUIRED: production-режим установщика не прошёл приёмку; изменять VPS запрещено")
			os.Exit(73)
		}
		fmt.Fprintln(os.Stderr, "[deploy] ошибка:", err)
		os.Exit(2)
	}
}

func runRemoteStage(ctx context.Context, reader io.Reader, writer io.Writer, assets string) error {
	r, err := decodeDeployRequest(reader)
	if err != nil {
		return err
	}
	if r.Uninstall {
		return errors.New("передача пакета не выполняет удаление")
	}
	if err = validate(r); err != nil {
		return err
	}
	installer, server, err := verifiedAssets(assets)
	if err != nil {
		return errors.New("серверный пакет не прошёл проверку SHA-256/ARM64")
	}
	t, err := newRemoteTransport(r)
	if err != nil {
		return err
	}
	defer t.close()
	archCtx, archCancel := context.WithTimeout(ctx, 10*time.Second)
	err = t.requireRemoteARM64(archCtx)
	archCancel()
	if err != nil {
		return err
	}
	preflight, cancel := context.WithTimeout(ctx, 40*time.Second)
	err = t.inventory(preflight, installer, writer)
	cancel()
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(writer, "[deploy] Передаём проверенный пакет по SFTP; установка не запускается"); err != nil {
		return err
	}
	if err = stageRemoteAssets(ctx, t, installer, server); err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, "CSQTT_STAGE_VERIFIED_AND_CLEANED")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, "[deploy] SHA-256 обоих удалённых файлов подтверждён; временный каталог удалён. Установка не выполнялась")
	return err
}
