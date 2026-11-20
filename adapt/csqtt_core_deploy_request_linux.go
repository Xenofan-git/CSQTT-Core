//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strings"
)

// The HTTP Deploy request belongs to CSQTT Core. The transport runner remains
// a separate compatibility adapter until independently verified ARM64 cutover.
type csqttDeployRequest struct {
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	SSHPort      int    `json:"sshPort"`
	User         string `json:"user"`
	UsePassword  bool   `json:"usePassword"`
	Password     string `json:"password"`
	KeyPath      string `json:"keyPath"`
	ManualPorts  bool   `json:"manualPorts"`
	CorePort     int    `json:"corePort"`
	WarpPort     int    `json:"warpPort"`
	ListenPort   int    `json:"listenPort"`
	MainPassword string `json:"mainPassword"`
	WebUser      string `json:"webUser"`
	WebPassword  string `json:"webPassword"`
	DeviceID     string `json:"deviceId"`
	DockerInstall bool  `json:"dockerInstall"`
	Uninstall     bool   `json:"uninstall"`
}

var (
	csqttDeployAlnum = regexp.MustCompile("^[A-Za-z0-9]+$")
	csqttDeployDevice = regexp.MustCompile("^[A-Za-z0-9-]+$")
	csqttDeployHostname = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9.-]*$")
	csqttDeploySSHUser = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_.-]*$")
)

func csqttValidatePort(value int) bool { return value >= 0 && value <= 65535 }

func csqttValidateDeployRequest(raw string) error {
	if strings.TrimSpace(raw) == "null" {
		return errors.New("пустой запрос Deploy")
	}
	var req csqttDeployRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return errors.New("неверный формат запроса Deploy")
	}
	// Preserve the old runner's behavior for other protocols until their
	// separate migration. Only CSQTT Core inputs are validated here.
	if !strings.EqualFold(strings.TrimSpace(req.Protocol), "CSQTT") {
		return nil
	}
	host := strings.TrimSpace(req.Host)
	if host == "" {
		return errors.New("не задан адрес удалённого сервера")
	}
	// Reject command-option and shell-control characters *before* any
	// external SSH tool sees the host. DNS/IP target policy is checked again
	// by the existing runner; this preflight does NOT allow private targets.
	trimHost := strings.Trim(host, "[]")
	if strings.ContainsAny(host, "[]") &&
		!(strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") &&
			strings.Count(host, "[") == 1 && strings.Count(host, "]") == 1 &&
			net.ParseIP(trimHost) != nil) {
		return errors.New("неверный формат адреса сервера")
	}
	if net.ParseIP(trimHost) == nil && !csqttDeployHostname.MatchString(trimHost) {
		return errors.New("неверный формат адреса сервера")
	}
	if strings.HasPrefix(trimHost, "-") || len(host) > 255 {
		return errors.New("неверный формат адреса сервера")
	}
	if !csqttDeploySSHUser.MatchString(strings.TrimSpace(req.User)) {
		return errors.New("неверное имя SSH-пользователя")
	}
	if !csqttValidatePort(req.SSHPort) || !csqttValidatePort(req.CorePort) ||
		!csqttValidatePort(req.WarpPort) || !csqttValidatePort(req.ListenPort) {
		return errors.New("номер порта за пределами диапазона")
	}
	if req.UsePassword && req.Password == "" {
		return errors.New("не задан SSH-пароль")
	}
	if req.DockerInstall {
		return errors.New("Docker-установка CSQTT Core отключена")
	}
	if req.Uninstall { return nil }
	if !csqttDeployAlnum.MatchString(req.MainPassword) ||
		!csqttDeployAlnum.MatchString(req.WebUser) ||
		!csqttDeployAlnum.MatchString(req.WebPassword) {
		return errors.New("данные авторизации CSQTT Core должны содержать только латиницу и цифры")
	}
	if !csqttDeployDevice.MatchString(req.DeviceID) {
		return errors.New("неверный ID устройства CSQTT Core")
	}
	if req.ManualPorts {
		ssh := req.SSHPort
		if ssh == 0 { ssh = 22 }
		core := req.CorePort
		if core == 0 { core = 47000 }
		web := req.WarpPort
		if web == 0 { web = 47002 }
		if ssh == core || ssh == web || core == web {
			return errors.New("SSH, CSQTT и web-порты должны различаться")
		}
	}
	return nil
}
