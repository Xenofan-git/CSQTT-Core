//go:build linux

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// No test connects to SSH, starts a process, or edits any real host.
func TestCSQTTCoreOwnDeployPreflightRejectsBadRequestsBeforeTransport(t *testing.T) {
	baseline := csqttDeployRequest{
		Protocol: "CSQTT", Host: "198.51.100.25", SSHPort: 22,
		User: "root", UsePassword: true, Password: "ssh-test-only",
		ManualPorts: true, CorePort: 47000, WarpPort: 47002,
		MainPassword: "TunnelTEST42", WebUser: "admin1",
		WebPassword: "WebTEST99", DeviceID: "dev-123",
	}

	tests := []struct {
		name string
		change func(*csqttDeployRequest)
		allowed bool
	}{
		{"valid remote IPv4",func(r *csqttDeployRequest){},true},
		{"valid tailnet IPv4",func(r *csqttDeployRequest){r.Host="100.70.73.57"},true},
		{"valid IPv6",func(r *csqttDeployRequest){r.Host="[2001:db8::1]"},true},
		{"reject malformed bracket host",func(r *csqttDeployRequest){r.Host="[[2001:db8::1]"},false},
		{"reject bracketed DNS name",func(r *csqttDeployRequest){r.Host="[host.example.org]"},false},
		{"uninstall needs no panel password",func(r *csqttDeployRequest){r.Uninstall=true;r.WebPassword="";r.MainPassword="";r.DeviceID=""},true},
		{"empty address",func(r *csqttDeployRequest){r.Host="  "},false},
		{"leading option in host",func(r *csqttDeployRequest){r.Host="--proxy-command"},false},
		{"shell delimiters",func(r *csqttDeployRequest){r.Host="example.org;touch /tmp/nope"},false},
		{"newline in host",func(r *csqttDeployRequest){r.Host="example.org\ninjected"},false},
		{"embedded username",func(r *csqttDeployRequest){r.Host="root@host.example.org"},false},
		{"bad SSH user",func(r *csqttDeployRequest){r.User="root;touch"},false},
		{"empty SSH user",func(r *csqttDeployRequest){r.User=""},false},
		{"invalid SSH port",func(r *csqttDeployRequest){r.SSHPort=70000},false},
		{"invalid network port",func(r *csqttDeployRequest){r.CorePort=-1},false},
		{"same SSH and core port",func(r *csqttDeployRequest){r.CorePort=22},false},
		{"same core and panel port",func(r *csqttDeployRequest){r.WarpPort=47000},false},
		{"missing SSH password",func(r *csqttDeployRequest){r.Password=""},false},
		{"empty panel password",func(r *csqttDeployRequest){r.WebPassword=""},false},
		{"non-ASCII panel user",func(r *csqttDeployRequest){r.WebUser="тест"},false},
		{"invalid device ID",func(r *csqttDeployRequest){r.DeviceID="id/evil"},false},
		{"Docker installation prohibited",func(r *csqttDeployRequest){r.DockerInstall=true},false},
	}

	for _, tc := range tests {
		t.Run(tc.name,func(t *testing.T){
			payload:=baseline
			tc.change(&payload)
			raw,err:=json.Marshal(payload)
			if err!=nil { t.Fatal(err) }
			calls:=0
			backend:=&csqttLegacyDeployBackend{delegate:func(got string)bool{
				calls++
				if got!=string(raw){t.Fatalf("request bytes modified: %s",got)}
				return true
			}}
			got:=backend.DeployProtocol(string(raw))
			if got!=tc.allowed {t.Fatalf("started=%v, expected=%v",got,tc.allowed)}
			expected:=0
			if tc.allowed {expected=1}
			if calls!=expected {t.Fatalf("transport calls=%d expected=%d",calls,expected)}
			if !tc.allowed {
				log:=backend.DeployLog()
				if !strings.HasPrefix(log,"[deploy] ") {
					t.Fatalf("no preflight diagnostic: %q",log)
				}
				for _, secret:=range []string{payload.Password,payload.MainPassword,payload.WebPassword} {
					if secret!="" && strings.Contains(log,secret) {
						t.Fatal("credential leaked into preflight log")
					}
				}
			}
		})
	}
}

func TestCSQTTCoreOwnDeployPreflightMalformedJSON(t *testing.T) {
	calls:=0
	backend:=&csqttLegacyDeployBackend{delegate:func(string)bool{calls++;return true}}
	for _, raw:=range []string{"", "{", "[]", "null", `{"protocol":"CSQTT","host":123}`} {
		if backend.DeployProtocol(raw){t.Fatalf("invalid payload accepted")}
	}
	if calls!=0 {t.Fatalf("transport invoked for malformed input")}
}
