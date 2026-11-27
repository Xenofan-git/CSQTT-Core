package main

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
)

// Derived from CSQTTR v2.5.3 DeployOperations.kt:
// serverArchitectureForMachine and systemdEnvironmentValue.
// Only the already pinned Linux ARM64 server is shipped by this project;
// upstream's additional AMD64/ARMv7 assets are not silently substituted.
func requireARM64Machine(output string) error {
	arch, err := resolveServerArchitecture(output)
	if err != nil {
		return err
	}
	// Existing lifecycle is ARM64-only pending separate acceptance.
	if arch != serverARM64 {
		return errors.New("архитектура VPS не соответствует проверенному ARM64-пакету")
	}
	return nil
}

func systemdEnvironmentValue253(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, ch := range value {
		switch ch {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(ch)
		case '\r', '\n':
			b.WriteByte(' ')
		default:
			b.WriteRune(ch)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Local preparation only: performDeploy creates WEB environment and
// main_password overrides for Install, but not from the form for Update.
// Unlike upstream 2.5.3, the current pinned Core installer requires the
// preserved environment and overrides as upload inputs even on Update.
// Reuse the existing preservation helper, never the entered credentials.
// Nothing here opens a connection, writes files or executes a command.
func prepareUpstreamDeployConfig(action lifecycleAction, r Request, existingEnv, existingOverrides []byte, databaseVerified bool) (preservedUpdateConfig, error) {
	switch action {
	case lifecycleUpdate:
		return preparePreservedUpdateConfig(existingEnv, existingOverrides, databaseVerified)
	case lifecycleRemove:
		return preservedUpdateConfig{}, nil
	case lifecycleInstall:
		if !alphanumeric(r.WebUser) || !alphanumeric(r.WebPassword) || !alphanumeric(r.MainPassword) || r.DockerInstall || r.Uninstall {
			return preservedUpdateConfig{}, errors.New("неподдерживаемая или неполная конфигурация установки")
		}
		host := strings.TrimSpace(r.Host)
		if net.ParseIP(host) == nil {
			return preservedUpdateConfig{}, errors.New("некорректный IP WEB-конфигурации")
		}
		env := "CSQTT_WEB_USER=" + systemdEnvironmentValue253(r.WebUser) + "\n" +
			"CSQTT_WEB_PASS=" + systemdEnvironmentValue253(r.WebPassword) + "\n" +
			"CSQTT_PUBLIC_IP=" + systemdEnvironmentValue253(host) + "\n"
		overrides, err := json.Marshal(struct {
			MainPassword string `json:"main_password"`
		}{r.MainPassword})
		if err != nil {
			return preservedUpdateConfig{}, errors.New("не удалось подготовить конфигурацию установки")
		}
		return preservedUpdateConfig{webEnvironment: []byte(env), deployOverrides: overrides}, nil
	default:
		return preservedUpdateConfig{}, errors.New("неподдерживаемый режим Deploy")
	}
}
