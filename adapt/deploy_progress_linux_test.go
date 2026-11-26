//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestDeployProgressSnapshotReportsObservedCompletion(t *testing.T) {
	cases := []struct {
		busy      bool
		log, want string
	}{
		{false, "", "idle"},
		{true, "[deploy] DeployManager запущен", "running"},
		{false, "[deploy] не найден deploy-manager рядом с приложением", "error"},
		{false, "[deploy] завершено с ошибкой: exit status 1", "error"},
		{false, "[deploy] завершено успешно", "completed"},
		{false, "CSQTT_CORE_DEPLOY_OK\n[deploy] завершено успешно", "success"},
		{false, "CSQTT_DEPLOY_OK\n[deploy] завершено успешно", "success"},
		{false, "CSQTT_DEPLOY_ERROR|cutover|test failure\n[deploy] завершено с ошибкой: exit status 30", "error"},
		{false, "CSQTT_DEPLOY_OK\n[deploy] завершено с ошибкой: exit status 30", "error"},
		{false, "CSQTT_ISOLATION_REQUIRED\n[deploy] завершено с ошибкой: exit status 73", "error"},
		{false, "CSQTT_INV|end\nCSQTT_ISOLATION_REQUIRED\n[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась", "unknown"},
		{false, "CSQTT_ISOLATION_REQUIRED\n[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась", "unknown"},
		{false, "CSQTT_INV|end\n[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась", "unknown"},
		{false, "CSQTT_INV|end\nCSQTT_ISOLATION_REQUIRED\n[deploy] завершено с ошибкой: exit status 73", "error"},
		{false, "CSQTT_CORE_DEPLOY_OK\n[deploy] завершено с ошибкой", "error"},
		{false, "[deploy] не удалось запустить DeployManager", "error"},
		{false, "[deploy] DeployManager запущен", "unknown"},
	}
	for _, tt := range cases {
		result := deployProgressSnapshot(tt.busy, tt.log)
		if result["result"] != tt.want || result["deploying"] != tt.busy {
			t.Fatalf("busy=%v log=%q: got=%v want=%s", tt.busy, tt.log, result, tt.want)
		}
	}
}

func TestDeploy253ExactOperationMarkersAndObservedProgress(t *testing.T) {
	cases := []struct{ log, want, rollback string }{
		{"prefix CSQTT_DEPLOY_OK suffix\n[deploy] завершено успешно", "completed", ""},
		{"CSQTT_OTHER_DEPLOY_OK\n[deploy] завершено успешно", "completed", ""},
		{"CSQTT_DEPLOY_OK\nCSQTT_DEPLOY_OK\n[deploy] завершено успешно", "completed", ""},
		{"CSQTT_DEPLOY_OK\nCSQTT_UNINSTALL_OK\n[deploy] завершено успешно", "completed", ""},
		{"CSQTT_UNINSTALL_OK\n[deploy] завершено успешно", "uninstalled", ""},
		{"CSQTT_CORE_UNINSTALL_OK\n[deploy] завершено успешно", "uninstalled", ""},
		{"CSQTT_UNINSTALL_OK\n[deploy] завершено с ошибкой: exit status 1", "error", ""},
		{"CSQTT_DEPLOY_OK\nCSQTT_DEPLOY_ERROR|cutover|fixture\n[deploy] завершено успешно", "error", ""},
		{"[deploy] завершено с ошибкой: exit status 30", "error", ""},
		{"CSQTT_DEPLOY_ROLLED_BACK\n[deploy] завершено с ошибкой: exit status 30", "error", "restored"},
		{"CSQTT_DEPLOY_ROLLBACK_FAILED\n[deploy] завершено с ошибкой: exit status 31", "error", "failed"},
		{"CSQTT_DEPLOY_ROLLED_BACK\nCSQTT_DEPLOY_ROLLBACK_FAILED\n[deploy] завершено с ошибкой: exit status 31", "error", "failed"},
		{"CSQTT_DEPLOY_ROLLED_BACK", "error", ""},
		{"CSQTT_PROGRESS|1|Done", "unknown", ""},
		{"prefix CSQTT_INV|end\nCSQTT_ISOLATION_REQUIRED\n[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась", "unknown", ""},
	}
	for _, c := range cases {
		x := deployProgressSnapshot(false, c.log)
		if x["result"] != c.want {
			t.Errorf("%q: %v", c.log, x)
		}
		got, _ := x["rollback"].(string)
		if got != c.rollback {
			t.Errorf("rollback for %q: %v", c.log, x)
		}
		if _, exists := x["progress"]; exists {
			t.Error("terminal result exposes running progress")
		}
	}
	x := deployProgressSnapshot(true, "CSQTT_PROGRESS|0.15|Подготовка\nCSQTT_PROGRESS|0.6|Бинарник...\nCSQTT_PROGRESS|NaN|bad\nCSQTT_PROGRESS|1.2|bad")
	if x["result"] != "running" || x["progress"] != float64(60) || x["stage"] != "Бинарник..." {
		t.Fatalf("observed progress: %v", x)
	}
	for _, line := range []string{"CSQTT_PROGRESS|-1|bad", "CSQTT_PROGRESS|1e-2|bad", "CSQTT_PROGRESS|Inf|bad", "prefix CSQTT_PROGRESS|0.5|bad", "CSQTT_PROGRESS|0.5|"} {
		x := deployProgressSnapshot(true, line)
		if _, ok := x["progress"]; ok {
			t.Errorf("invalid progress accepted: %q", line)
		}
	}
}

func TestDeploy253InventorySummaryRequiresCompleteReport(t *testing.T) {
	rows := []string{
		"CSQTT_INV|server|place=core|runtime=systemd|state=running|owner=core_candidate",
		"CSQTT_INV|interface|name=csqtt-core0|state=present|owner=unverified",
		"CSQTT_INV|database|state=present|name=csqtt.db",
		"CSQTT_INV|port|proto=udp|port=47000|state=busy|owner=core_candidate",
		"CSQTT_INV|port|proto=tcp|port=47002|state=free|owner=none",
		"CSQTT_INV|end",
	}
	base := strings.Join(rows, "\n")
	done := "\nCSQTT_ISOLATION_REQUIRED\n[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась"
	got := deployProgressSnapshot(false, base+done)
	if got["result"] != "preflight" {
		t.Fatalf("complete report not accepted: %v", got)
	}
	actualDone := strings.Replace(done, "CSQTT_ISOLATION_REQUIRED", "[deploy] CSQTT_ISOLATION_REQUIRED: production-режим установщика не прошёл приёмку; изменять VPS запрещено", 1)
	if x := deployProgressSnapshot(false, base+actualDone); x["result"] != "preflight" {
		t.Fatalf("actual helper terminal format not accepted: %v", x)
	}
	inv, ok := got["inventory"].(map[string]string)
	if !ok || inv["server"] != "running" || inv["interface"] != "present" || inv["database"] != "present" || inv["udp"] != "core_candidate" || inv["tcp"] != "free" || len(inv) != 5 {
		t.Fatalf("unexpected inventory %v", got)
	}
	for _, row := range rows {
		raw := strings.Replace(base, row, "", 1)
		if x := deployInventorySummary(raw); x != nil {
			t.Errorf("incomplete inventory accepted: %q", row)
		}
		if x := deployProgressSnapshot(false, raw+done); x["result"] != "unknown" {
			t.Errorf("incomplete inventory promoted to completed check: %v", x)
		}
	}
	for _, raw := range []string{
		base + "\n" + rows[0],
		rows[0] + "\n" + base,
		strings.Replace(base, "owner=core_candidate", "owner=foreign", 1),
		strings.Replace(base, "state=running", "state=unknown", 1),
		strings.Replace(base, "name=csqtt.db", "name=other.db", 1),
		strings.Replace(base, "port=47002", "port=46002", 1),
		strings.Replace(base, "owner=unverified", "owner=core_candidate", 1),
		strings.Replace(base, "CSQTT_INV|end", "CSQTT_INV|error|reason=fixture\nCSQTT_INV|end", 1),
	} {
		if x := deployInventorySummary(raw); x != nil {
			t.Errorf("invalid inventory accepted %q", raw)
		}
		if x := deployProgressSnapshot(false, raw+done); x["result"] != "unknown" {
			t.Errorf("invalid inventory promoted to completed check: %v", x)
		}
	}
	for _, raw := range []string{
		base + done + "\nCSQTT_DEPLOY_OK",
		base + done + "\nCSQTT_UNINSTALL_OK",
		base + done + "\n[deploy] завершено успешно",
		base + done + "\n" + done,
		strings.Replace(base+done, "CSQTT_ISOLATION_REQUIRED", "prefix CSQTT_ISOLATION_REQUIRED suffix", 1),
		strings.Replace(base+done, "[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась", "prefix [deploy] предварительная проверка завершена; установка на VPS НЕ запускалась suffix", 1),
	} {
		if x := deployProgressSnapshot(false, raw); x["result"] != "unknown" {
			t.Errorf("conflicting terminal evidence promoted to completed check: %v", x)
		}
	}
	for _, x := range []map[string]any{
		deployProgressSnapshot(true, base+done),
		deployProgressSnapshot(false, base),
		deployProgressSnapshot(false, base+done+"\n[deploy] завершено с ошибкой"),
	} {
		if _, ok := x["inventory"]; ok {
			t.Errorf("unconfirmed inventory exposed: %v", x)
		}
	}
}
