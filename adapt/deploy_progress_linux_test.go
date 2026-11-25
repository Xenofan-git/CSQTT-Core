//go:build linux

package main

import "testing"

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
		{false, "CSQTT_INV|end\nCSQTT_ISOLATION_REQUIRED\n[deploy] предварительная проверка завершена; установка на VPS НЕ запускалась", "preflight"},
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
