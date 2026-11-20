//go:build linux

package main

import (
	"sync"
	legacy "lalune-desktop/Libs"
)

// This is the only Go package adapter between the CSQTT Core headless
// HTTP API and the previous deployment implementation. It does not
// replace or change the installed services, config paths or SSH behavior.
type csqttDeployBackend interface {
	DeployProtocol(requestJSON string) bool
	DeployLog() string
	DeployBusy() bool
}

// The injected delegate permits deterministic, non-networked tests.
// nil preserves the original transport implementation until cutover.
type csqttLegacyDeployBackend struct {
	mu sync.Mutex
	preflightError string
	delegate func(string) bool
}

func (backend *csqttLegacyDeployBackend) DeployProtocol(requestJSON string) bool {
	if err := csqttValidateDeployRequest(requestJSON); err != nil {
		backend.mu.Lock()
		// This is a fixed diagnostic, not user-supplied payload or credentials.
		backend.preflightError = "[deploy] " + err.Error() + "\n"
		backend.mu.Unlock()
		return false
	}
	backend.mu.Lock()
	backend.preflightError = ""
	backend.mu.Unlock()
	if backend.delegate != nil {
		return backend.delegate(requestJSON)
	}
	return legacy.DeployProtocol(requestJSON)
}
func (backend *csqttLegacyDeployBackend) DeployLog() string {
	backend.mu.Lock()
	message := backend.preflightError
	backend.mu.Unlock()
	if message != "" { return message }
	return legacy.DeployLog()
}
func (backend *csqttLegacyDeployBackend) DeployBusy() bool {
	return legacy.DeployBusy()
}

var csqttDeployService csqttDeployBackend = &csqttLegacyDeployBackend{}
