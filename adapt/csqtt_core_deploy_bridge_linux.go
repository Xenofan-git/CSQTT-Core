//go:build linux

package main

import legacy "lalune-desktop/Libs"

// This is the only Go package adapter between the CSQTT Core headless
// HTTP API and the previous deployment implementation. It does not
// replace or change the installed services, config paths or SSH behavior.
type csqttDeployBackend interface {
	DeployProtocol(requestJSON string) bool
	DeployLog() string
	DeployBusy() bool
}

type csqttLegacyDeployBackend struct{}

func (csqttLegacyDeployBackend) DeployProtocol(requestJSON string) bool {
	return legacy.DeployProtocol(requestJSON)
}
func (csqttLegacyDeployBackend) DeployLog() string {
	return legacy.DeployLog()
}
func (csqttLegacyDeployBackend) DeployBusy() bool {
	return legacy.DeployBusy()
}

var csqttDeployService csqttDeployBackend = csqttLegacyDeployBackend{}
