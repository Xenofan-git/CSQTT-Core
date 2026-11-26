package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type memoryLifecycle struct {
	calls   []string
	fail    string
	receipt lifecycleReceipt
	cancel  context.CancelFunc
}

func (m *memoryLifecycle) step(name string) error {
	m.calls = append(m.calls, name)
	if m.fail == name {
		return errors.New("synthetic failure")
	}
	if m.fail == "cancel_"+name && m.cancel != nil {
		m.cancel()
	}
	return nil
}
func (m *memoryLifecycle) check(_ context.Context, _ lifecycleAction) error { return m.step("check") }
func (m *memoryLifecycle) stage(_ context.Context, _ lifecycleAction) error { return m.step("stage") }
func (m *memoryLifecycle) execute(_ context.Context, _ lifecycleAction) (lifecycleReceipt, error) {
	return m.receipt, m.step("execute")
}
func (m *memoryLifecycle) postcheck(_ context.Context, _ lifecycleAction, result string) error {
	return m.step("postcheck_" + result)
}

func Test253LifecycleOrderAndResult(t *testing.T) {
	for _, tc := range []struct {
		name         string
		action       lifecycleAction
		receipt      lifecycleReceipt
		fail, result string
		calls        []string
		success      bool
	}{
		{"install", lifecycleInstall, lifecycleReceipt{0, "CSQTT_DEPLOY_OK"}, "", "installed", []string{"check", "stage", "execute", "postcheck_installed"}, true},
		{"update", lifecycleUpdate, lifecycleReceipt{0, "CSQTT_CORE_DEPLOY_OK"}, "", "installed", []string{"check", "stage", "execute", "postcheck_installed"}, true},
		{"uninstall", lifecycleRemove, lifecycleReceipt{0, "CSQTT_UNINSTALL_OK"}, "", "uninstalled", []string{"check", "stage", "execute", "postcheck_uninstalled"}, true},
		{"preflight refusal", lifecycleInstall, lifecycleReceipt{}, "check", "unknown", []string{"check"}, false},
		{"stage failure", lifecycleInstall, lifecycleReceipt{}, "stage", "unknown", []string{"check", "stage"}, false},
		{"uninstall script stage failure", lifecycleRemove, lifecycleReceipt{}, "stage", "unknown", []string{"check", "stage"}, false},
		{"lost execution", lifecycleInstall, lifecycleReceipt{}, "execute", "unknown", []string{"check", "stage", "execute"}, false},
		{"postcheck fails", lifecycleInstall, lifecycleReceipt{0, "CSQTT_DEPLOY_OK"}, "postcheck_installed", "unknown", []string{"check", "stage", "execute", "postcheck_installed"}, false},
		{"rollback restored", lifecycleUpdate, lifecycleReceipt{30, "CSQTT_DEPLOY_ERROR|cutover|fixture\nCSQTT_DEPLOY_ROLLED_BACK"}, "", "restored", []string{"check", "stage", "execute", "postcheck_restored"}, false},
		{"rollback not verified", lifecycleUpdate, lifecycleReceipt{30, "CSQTT_DEPLOY_ROLLED_BACK"}, "postcheck_restored", "unknown", []string{"check", "stage", "execute", "postcheck_restored"}, false},
		{"rollback failure", lifecycleInstall, lifecycleReceipt{31, "CSQTT_DEPLOY_ROLLBACK_FAILED"}, "", "rollback_failed", []string{"check", "stage", "execute"}, false},
		{"wrong marker", lifecycleRemove, lifecycleReceipt{0, "CSQTT_DEPLOY_OK"}, "", "unknown", []string{"check", "stage", "execute"}, false},
		{"duplicate marker", lifecycleInstall, lifecycleReceipt{0, "CSQTT_DEPLOY_OK\nCSQTT_DEPLOY_OK"}, "", "unknown", []string{"check", "stage", "execute"}, false},
		{"error and success", lifecycleInstall, lifecycleReceipt{0, "CSQTT_DEPLOY_ERROR|cutover|fixture\nCSQTT_DEPLOY_OK"}, "", "unknown", []string{"check", "stage", "execute"}, false},
		{"exit 73", lifecycleInstall, lifecycleReceipt{73, "CSQTT_DEPLOY_OK"}, "", "error", []string{"check", "stage", "execute"}, false},
		{"exit 30 without marker", lifecycleUpdate, lifecycleReceipt{30, ""}, "", "error", []string{"check", "stage", "execute"}, false},
		{"uninstall code 30", lifecycleRemove, lifecycleReceipt{30, "CSQTT_DEPLOY_ROLLED_BACK"}, "", "error", []string{"check", "stage", "execute"}, false},
		{"unbounded output", lifecycleInstall, lifecycleReceipt{0, strings.Repeat("x", 65537)}, "", "unknown", []string{"check", "stage", "execute"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &memoryLifecycle{receipt: tc.receipt, fail: tc.fail}
			got, err := simulateDeployLifecycle(context.Background(), tc.action, m)
			if (err == nil) != tc.success || got.result != tc.result || !reflect.DeepEqual(m.calls, tc.calls) {
				t.Fatalf("result=%v err=%v calls=%v", got, err, m.calls)
			}
			confirmed := len(got.stages) > 0 && got.stages[len(got.stages)-1] == lifecycleConfirmed
			if confirmed != (tc.success || tc.result == "restored") {
				t.Fatal("incorrect confirmation")
			}
		})
	}
}
func Test253LifecycleCancellation(t *testing.T) {
	for _, name := range []string{"check", "stage", "execute", "postcheck_installed"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &memoryLifecycle{receipt: lifecycleReceipt{0, "CSQTT_DEPLOY_OK"}, fail: "cancel_" + name, cancel: cancel}
			out, err := simulateDeployLifecycle(ctx, lifecycleInstall, m)
			if err == nil || out.result != "unknown" || m.calls[len(m.calls)-1] != name {
				t.Fatal("cancellation continued operation")
			}
		})
	}
}
