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
