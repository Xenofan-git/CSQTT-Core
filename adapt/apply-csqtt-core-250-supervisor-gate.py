#!/usr/bin/env python3
"""Test-branch-only opt-in 2.5 Go supervisor launcher on fully adapted pinned source.

This is NOT invoked in the working WIRE-4 production build.
"""
from pathlib import Path

linux = Path("upstream-lalune/Desktop/Linux/app_linux.go")
bridge = Path("upstream-lalune/Desktop/Libs/bridge.go")
s = linux.read_text()
old_start = '''	deviceID := csqttDeviceID(bridge.Core)
	if deviceID == "" {
		bridge.Core.AddLog("[AUTH] ID устройства CSQTT Core не задан; запуск отменён")
		bridge.Core.SetConnected(false)
		return
	}
	cmdArgs = appendCSQTTAuthArgs(cmdArgs, password, deviceID)
	r.startCoreWithSudo(cmdArgs, listenPort, bridge)
'''
new_start = '''	// The approved v2.5 experiment must never use the legacy identity CLI.
	selected250, err := csqtt250CandidateMode()
	if err != nil {
		bridge.Core.AddLog("[CORE] Некорректный режим версии клиента; запуск отменён")
		bridge.Core.SetConnected(false)
		return
	}
	if selected250 {
		binary, verifyErr := csqtt250VerifiedBinaryPath()
		if verifyErr != nil {
			bridge.Core.AddLog("[CORE] Контрольная сумма клиента 2.5.0 не подтверждена")
			bridge.Core.SetConnected(false)
			return
		}
		adaptedArgs, normalizeErr := csqtt250NormalizeSupervisorArgv(cmdArgs)
		if normalizeErr != nil {
			bridge.Core.AddLog("[CORE] Неподдерживаемые параметры клиента 2.5.0")
			bridge.Core.SetConnected(false)
			return
		}
		cmdArgs = append([]string{binary}, adaptedArgs...)
		r.startCoreWithSudo(cmdArgs, listenPort, bridge)
		return
	}
	deviceID := csqttDeviceID(bridge.Core)
	if deviceID == "" {
		bridge.Core.AddLog("[AUTH] ID устройства CSQTT Core не задан; запуск отменён")
		bridge.Core.SetConnected(false)
		return
	}
	cmdArgs = appendCSQTTAuthArgs(cmdArgs, password, deviceID)
	r.startCoreWithSudo(cmdArgs, listenPort, bridge)
'''
assert s.count(old_start) == 1, "LinuxRunner.StartCore credential insertion changed upstream"
s = s.replace(old_start, new_start, 1)
old_launch = '''	logFile := filepath.Join(os.TempDir(), "lalune_core_logs.txt")
	os.Remove(logFile)

	cmdLine := buildCoreCommandLine(cmdArgs, logFile)

	bridge.Core.AddLog("[INFO] Запуск ядра через sudo...")

	var cmd *exec.Cmd
	if os.Geteuid() == 0 {
		cmd = exec.Command("sh", "-c", cmdLine)
	} else {
'''
new_launch = '''	logFile := filepath.Join(os.TempDir(), "lalune_core_logs.txt")
	selected250, modeErr := csqtt250CandidateMode()
	if modeErr != nil {
		bridge.Core.AddLog("[CORE] Клиент 2.5: недопустимый режим; запуск отменён")
		bridge.Core.SetConnected(false)
		return
	}
	if selected250 {
		logFile = filepath.Join(os.TempDir(), "csqtt-core250-lab-only.log")
	}
	os.Remove(logFile)

	cmdLine := buildCoreCommandLine(cmdArgs, logFile)
	if selected250 {
		bridge.Core.AddLog("[INFO] Клиент CSQTT 2.5.0: прямой запуск без оболочки; аргументы скрыты")
	} else {
		bridge.Core.AddLog("[INFO] Запуск ядра через sudo...")
	}

	var cmd *exec.Cmd
	if selected250 {
		password, authErr := loadTunnelAuthPassword()
		if authErr != nil {
			bridge.Core.AddLog("[CORE] Отсутствует пароль CSQTT 2.5")
			bridge.Core.SetConnected(false)
			return
		}
		candidate, launchErr := csqtt250ClientCommand(cmdArgs[0], cmdArgs[1:], password, os.Environ())
		if launchErr != nil {
			bridge.Core.AddLog("[CORE] Не удалось подготовить защищённый запуск 2.5")
			bridge.Core.SetConnected(false)
			return
		}
		cmd = candidate
	} else if os.Geteuid() == 0 {
		cmd = exec.Command("sh", "-c", cmdLine)
	} else {
'''
assert s.count(old_launch) == 1, "LinuxRunner launch block has changed"
s=s.replace(old_launch,new_launch,1)
old_stdout='''	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
'''
new_stdout='''	if selected250 {
		// No shell redirection or argv credentials. The existing TUNCONF
		// log watcher uses exactly this file, which must be private.
		privateLog, logErr := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if logErr != nil {
			_ = stdin.Close()
			bridge.Core.AddLog("[CORE] Не удалось создать приватный журнал 2.5")
			bridge.Core.SetConnected(false)
			return
		}
		defer privateLog.Close()
		cmd.Stdout = privateLog
		cmd.Stderr = privateLog
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
'''
assert s.count(old_stdout)==1
s=s.replace(old_stdout,new_stdout,1)
linux.write_text(s)

b=bridge.read_text()
old_check='''	if _, err := os.Stat(b.Core.GetCorePath()); os.IsNotExist(err) {
		b.Core.AddLog("[API] Ядро не найдено, скачиваю...")
'''
new_check='''	// Candidate v2.5 runs only the explicitly pinned test ELF. Never
	// auto-download or fall back to the legacy WIRE-4 binary.
	if os.Getenv("CSQTT_CORE_250_EXPERIMENT") == "isolated-only" {
		candidate := os.Getenv("CSQTT_250_ORIGINAL_CLIENT_BINARY")
		if candidate == "" {
			b.Core.AddLog("[CORE] Проверенный клиент 2.5.0 не задан")
			b.Core.SetConnected(false)
			return
		}
		if _, err := os.Stat(candidate); err != nil {
			b.Core.AddLog("[CORE] Файл клиента 2.5.0 отсутствует")
			b.Core.SetConnected(false)
			return
		}
	} else if _, err := os.Stat(b.Core.GetCorePath()); os.IsNotExist(err) {
		b.Core.AddLog("[API] Ядро не найдено, скачиваю...")
'''
assert b.count(old_check)==1
b=b.replace(old_check,new_check,1)
old_log='''	b.Core.AddLog(fmt.Sprintf("Команда: %s", strings.Join(cmdArgs, " ")))'''
new_log='''	if os.Getenv("CSQTT_CORE_250_EXPERIMENT") != "" {
		b.Core.AddLog("[CORE] Команда клиента 2.5.0 подготовлена; чувствительные параметры скрыты")
	} else {
		b.Core.AddLog(fmt.Sprintf("Команда: %s", strings.Join(cmdArgs, " ")))
	}'''
assert b.count(old_log)==1
b=b.replace(old_log,new_log,1)
bridge.write_text(b)
print("CORE250_CANDIDATE_SUPERVISOR_GATE_GENERATED_PASS")
