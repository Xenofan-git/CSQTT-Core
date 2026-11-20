//go:build linux

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type csqttDeployStub struct {
	calls  []string
	body   string
	result bool
	busy   bool
	logs   string
}

func (stub *csqttDeployStub) DeployProtocol(body string) bool {
	stub.calls = append(stub.calls, "deploy")
	stub.body = body
	return stub.result
}
func (stub *csqttDeployStub) DeployLog() string {
	stub.calls = append(stub.calls, "log")
	return stub.logs
}
func (stub *csqttDeployStub) DeployBusy() bool {
	stub.calls = append(stub.calls, "status")
	return stub.busy
}

func TestCSQTTCoreDeployBridgeIsolatesHTTPPanel(t *testing.T) {
	previous := csqttDeployService
	stub := &csqttDeployStub{result: true, busy: true, logs: "fixture-only"}
	csqttDeployService = stub
	t.Cleanup(func() { csqttDeployService = previous })
	handler := apiHandler(&App{})

	request := httptest.NewRequest(http.MethodPost, "/deploy", strings.NewReader(`{"protocol":"CSQTT","fixture":true}`))
	request.RemoteAddr = "192.168.4.10:5050"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("mock deploy response: %d %s", response.Code, response.Body.String())
	}
	if stub.body != `{"protocol":"CSQTT","fixture":true}` {
		t.Fatalf("request JSON changed crossing API boundary: %q", stub.body)
	}
	for _, path := range []string{"/deploy/log", "/deploy/status"} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "192.168.4.10:5050"
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK { t.Fatalf("%s status=%d", path, response.Code) }
		if path == "/deploy/log" && !strings.Contains(response.Body.String(), "fixture-only") {
			t.Fatalf("deploy log backend not used: %s", response.Body.String())
		}
		if path == "/deploy/status" && !strings.Contains(response.Body.String(), `"deploying":true`) {
			t.Fatalf("deploy busy backend not used: %s", response.Body.String())
		}
	}
	if got := strings.Join(stub.calls, ","); got != "deploy,log,status" {
		t.Fatalf("wrong backend dispatch: %s", got)
	}
}

func TestCSQTTCoreDeployBridgeBlocksPublicRequests(t *testing.T) {
	previous := csqttDeployService
	stub := &csqttDeployStub{result: true}
	csqttDeployService = stub
	t.Cleanup(func() { csqttDeployService = previous })
	handler := apiHandler(&App{})
	for _, path := range []string{"/deploy","/deploy/log","/deploy/status"} {
		method := http.MethodGet
		if path == "/deploy" { method = http.MethodPost }
		request := httptest.NewRequest(method, path, strings.NewReader(`{"protocol":"CSQTT"}`))
		request.RemoteAddr = "8.8.8.8:5353"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("public request %s status=%d", path, response.Code)
		}
	}
	if len(stub.calls) != 0 { t.Fatalf("public client invoked backend: %v", stub.calls) }
}

type csqttSettingsFixture string
func (s csqttSettingsFixture) GetSettingsJson() string { return string(s) }
type csqttNilSettingsFixture struct{}
func (*csqttNilSettingsFixture) GetSettingsJson() string { return `{"deviceId":"should-not-be-read"}` }

func TestCSQTTCoreDeviceIDDoesNotNeedExternalGoPackage(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"valid", `{"deviceId":" node-123 "}`, "node-123"},
		{"empty", `{"deviceId":" "}`, ""},
		{"non-string", `{"deviceId":55}`, ""},
		{"malformed", `{bad`, ""},
		{"absent", `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := csqttDeviceID(csqttSettingsFixture(tc.raw)); got != tc.want {
				t.Fatalf("device ID got %q want %q", got, tc.want)
			}
		})
	}
	var typedNil *csqttNilSettingsFixture
	if got := csqttDeviceID(typedNil); got != "" {
		t.Fatalf("typed nil settings source returned %q", got)
	}
	if got := csqttDeviceID(nil); got != "" {
		t.Fatalf("nil settings source returned %q", got)
	}
}

func TestCSQTTCoreDeployBridgeRefusesMalformedRequestBeforeBackend(t *testing.T) {
	previous := csqttDeployService
	stub := &csqttDeployStub{result: true}
	csqttDeployService = stub
	t.Cleanup(func() { csqttDeployService = previous })
	handler := apiHandler(&App{})
	for _, tc := range []struct {
		name string
		verb string
		body string
		want int
	}{
		{"malformed JSON", http.MethodPost, "{malformed", http.StatusBadRequest},
		{"wrong method", http.MethodGet, "{\"protocol\":\"CSQTT\"}", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.verb, "/deploy", strings.NewReader(tc.body))
			request.RemoteAddr = "192.168.4.10:5050"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want=%d", response.Code, tc.want)
			}
		})
	}
	if len(stub.calls) != 0 {
		t.Fatalf("malformed/method-rejected request invoked deployment: %v", stub.calls)
	}
}
