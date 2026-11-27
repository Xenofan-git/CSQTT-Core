package main

// Platform adaptation of CSQTTR 2.5.3 performDeploy/performUninstall.
// Sequence: connect, architecture, verified assets, check, upload, execute once,
// confirmed result, independent postcheck, cleanup. No simulation in this path.
import (
	"bytes"
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const lifecycleInstallerSHA = "3ef1b3977d289e4bd878fe9ad87395abf30345c9b54c784bf69876ed33c43529"
const lifecycleInstallerSize int64 = 134626

//go:embed lifecycle_state.py
var lifecycleStateScript string

type serverEvidence struct {
	MachineID   string `json:"machineID"`
	Virtual     string `json:"virtual"`
	OS          string `json:"os"`
	Binary      string `json:"binary"`
	Unit        string `json:"unit"`
	Environment string `json:"environment"`
	Database    string `json:"database"`
	Config      string `json:"config"`
}

func requestedLifecycleAction(r Request) (lifecycleAction, error) {
	a := lifecycleAction(r.Action)
	if a == "" {
		if r.Uninstall {
			a = lifecycleRemove
		} else {
			a = lifecycleInstall
		}
	}
	if a != lifecycleInstall && a != lifecycleUpdate && a != lifecycleRemove || r.Uninstall && a != lifecycleRemove {
		return "", errors.New("неподдерживаемая или противоречивая операция")
	}
	return a, nil
}

// Operator configuration is not supplied by the API. The existing mutation
// gate is necessary but insufficient: bind it to one endpoint and machine ID.
// No localhost/private-network validation exception is introduced here.
func requireLifecycleScope(r Request) (string, error) {
	port := r.SSHPort
	if port == 0 {
		port = 22
	}
	identity := os.Getenv("CSQTT_CORE_DEPLOY_LAB_MACHINE_ID")
	if os.Getenv("CSQTT_CORE_ALLOW_MUTATION") != "isolated-lab-only" ||
		os.Getenv("CSQTT_CORE_DEPLOY_LAB_TARGET") != r.Host+":"+strconv.Itoa(port) ||
		!regexp.MustCompile("^[a-f0-9]{32}$").MatchString(identity) {
		return "", ErrIsolationRequired
	}
	return identity, nil
}

func (t *remoteTransport) lifecycleEvidence(ctx context.Context, identity string) (serverEvidence, error) {
	var evidence serverEvidence
	child, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	cmd := t.command(child, false, "python3 -")
	cmd.Stdin = strings.NewReader(lifecycleStateScript)
	result, err := execResult(child, cmd, nil, nil)
	if err != nil || result.exitStatus != 0 {
		return evidence, errors.New("независимая проверка конфигурации/SQLite не выполнена")
	}
	dec := json.NewDecoder(strings.NewReader(result.stdout))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&evidence); err != nil {
		return evidence, errors.New("некорректное подтверждение состояния сервера")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || evidence.MachineID != identity ||
		(evidence.Virtual != "qemu" && evidence.Virtual != "kvm") || (evidence.OS != "debian" && evidence.OS != "ubuntu") {
		return evidence, errors.New("целевая машина не соответствует разрешённой изолированной среде")
	}
	for _, value := range []string{evidence.Binary, evidence.Unit, evidence.Environment, evidence.Database, evidence.Config} {
		if value != "" {
			b, e := hex.DecodeString(value)
			if e != nil || len(b) != 32 {
				return evidence, errors.New("неполное подтверждение состояния")
			}
		}
	}
	return evidence, nil
}

func (t *remoteTransport) lifecycleInventory(ctx context.Context, installer []byte) (string, error) {
	child, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var records bytes.Buffer
	if err := t.inventory(child, installer, &records); err != nil {
		return "", err
	}
	return records.String(), nil
}

func checkLifecycleState(a lifecycleAction, inventory string, e serverEvidence) error {
	absent := strings.Contains(inventory, "|state=absent|owner=none")
	if a == lifecycleInstall {
		if !absent || e.Binary != "" || e.Unit != "" || e.Config != "" ||
			!strings.Contains(inventory, "|proto=udp|port=47000|state=free|owner=none") ||
			!strings.Contains(inventory, "|proto=tcp|port=47002|state=free|owner=none") ||
			!strings.Contains(inventory, "|name=csqtt-core0|state=absent|") {
			return errors.New("установка требует пустое место; для существующего сервера выберите обновление")
		}
	} else if a == lifecycleUpdate {
		if absent || e.Binary == "" || e.Unit == "" || e.Environment == "" || e.Database == "" {
			return errors.New("обновление требует подтверждённый runtime, конфигурацию и целую SQLite")
		}
	} else if a == lifecycleRemove && (e.Binary == "" || e.Unit == "" || absent) {
		return errors.New("принадлежность удаляемого runtime не подтверждена")
	}
	return nil
}

func checkLifecyclePost(a lifecycleAction, result, beforeInventory, afterInventory string, before, after serverEvidence) error {
	invalid := errors.New("независимые условия завершения операции не подтверждены")
	if a != lifecycleInstall && (before.Environment != after.Environment || before.Database != after.Database || before.Config != after.Config) {
		return invalid
	}
	if result == "restored" {
		if before.Binary != after.Binary || before.Unit != after.Unit || beforeInventory != afterInventory {
			return invalid
		}
		return nil
	}
	if a == lifecycleRemove {
		if after.Binary != "" || after.Unit != "" ||
			!strings.Contains(afterInventory, "|state=absent|owner=none") ||
			!strings.Contains(afterInventory, "|name=csqtt-core0|state=absent|") ||
			!strings.Contains(afterInventory, "|proto=udp|port=47000|state=free|owner=none") ||
			!strings.Contains(afterInventory, "|proto=tcp|port=47002|state=free|owner=none") {
			return invalid
		}
	} else {
		if after.Binary != expectedServerSHA || after.Unit == "" || after.Environment == "" || after.Database == "" ||
			!strings.Contains(afterInventory, "|state=running|owner=core_candidate") ||
			!strings.Contains(afterInventory, "|proto=udp|port=47000|state=busy|owner=core_candidate") ||
			!strings.Contains(afterInventory, "|proto=tcp|port=47002|state=busy|owner=core_candidate") {
			return invalid
		}
	}
	return nil
}

// Only fixed stage labels are exposed live. Raw remote text may include
// configuration values unknown to the client during preservation-mode update.
func publishLifecycleProgress(w io.Writer, line string) {
	parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
	if len(parts) != 3 || parts[0] != "CSQTT_PROGRESS" {
		return
	}
	p, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || math.IsNaN(p) || p < 0 || p > 1 {
		return
	}
	label := "Работа установщика"
	switch {
	case p < 0.15:
		label = "Подготовка сервера"
	case p < 0.60:
		label = "Подготовка runtime"
	case p < 0.90:
		label = "Установка компонентов"
	default:
		label = "Проверка запуска"
	}
	fmt.Fprintf(w, "CSQTT_PROGRESS|%.3f|%s\n", p, label)
}

func (t *remoteTransport) execInstallerResult(ctx context.Context, workspace string, action lifecycleAction, writer io.Writer) (readOnlyExecResult, error) {
	unknown := readOnlyExecResult{exitStatus: -1}
	identity, err := requireLifecycleScope(t.request)
	if err != nil {
		return unknown, err
	}
	if !regexp.MustCompile("^/tmp/csqtt-deploy-[a-f0-9]{32}$").MatchString(workspace) ||
		(action != lifecycleInstall && action != lifecycleUpdate && action != lifecycleRemove) {
		return unknown, errors.New("неверная операция или staging path")
	}
	port := t.request.SSHPort
	if port == 0 {
		port = 22
	}
	// All interpolated values are validated fixed words, numbers, digests or a
	// generated private path. No passwords or arbitrary commands enter argv.
	script := "set -eu\n" +
		"[ \"$(cat /etc/machine-id)\" = '" + identity + "' ]\n" +
		"case \"$(systemd-detect-virt --vm)\" in qemu|kvm) ;; *) exit 20 ;; esac\n" +
		"[ -d '" + workspace + "' ] && [ ! -L '" + workspace + "' ]\n" +
		"[ \"$(stat -c '%u:%a' '" + workspace + "')\" = 0:700 ]\n" +
		"cd '" + workspace + "'\n" +
		"[ ! -L deploy-csqtt-core-lifecycle.sh ]\n" +
		"printf '%s  %s\\n' '" + lifecycleInstallerSHA + "' deploy-csqtt-core-lifecycle.sh | sha256sum -c --status\n"
	if action != lifecycleRemove {
		script += "[ ! -L csqtt-core8-linux-arm64 ]\nprintf '%s  %s\\n' '" + expectedServerSHA + "' csqtt-core8-linux-arm64 | sha256sum -c --status\n"
	}
	script += "exec env CSQTT_CORE_ALLOW_MUTATION=isolated-lab-only CSQTT_CORE_VERIFIED_BINARY_SHA256=" + expectedServerSHA +
		" CSQTT_PEER_PORT=47000 CSQTT_WEB_PORT=47002 CSQTT_SSH_PORT=" + strconv.Itoa(port) +
		" CSQTT_DEPLOY_MODE=systemd CSQTT_DEPLOY_WORKSPACE=" + workspace +
		" bash ./deploy-csqtt-core-lifecycle.sh " + string(action) + "\n"
	cmd := t.command(ctx, false, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	return execResult(ctx, cmd, func(line string) { publishLifecycleProgress(writer, line) }, nil)
}

func runRemoteLifecycle(ctx context.Context, reader io.Reader, writer io.Writer, assets string) (err error) {
	r, err := decodeDeployRequest(reader)
	if err != nil {
		return err
	}
	action, err := requestedLifecycleAction(r)
	if err != nil {
		return err
	}
	if err = validate(r); err != nil {
		return err
	}
	identity, err := requireLifecycleScope(r)
	if err != nil {
		return err
	}
	// CSQTTR performUninstall needs only the verified removal script.
	// Do not require a replacement server ELF or install-only baseline.
	var server []byte
	if action != lifecycleRemove {
		_, server, err = verifiedAssets(assets)
		if err != nil {
			return err
		}
	}
	installer, err := readVerifiedAsset(filepath.Join(assets, "deploy-csqtt-core-lifecycle.sh"), lifecycleInstallerSize, lifecycleInstallerSHA)
	if err != nil {
		return errors.New("lifecycle candidate не прошёл проверку SHA-256")
	}
	t, err := newRemoteTransport(r)
	if err != nil {
		return err
	}
	defer t.close()
	fmt.Fprintln(writer, "CSQTT_PROGRESS|0.02|Подключение и проверка архитектуры")
	arch, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = t.requireRemoteARM64(arch)
	cancel()
	if err != nil {
		return err
	}
	before, err := t.lifecycleEvidence(ctx, identity)
	if err != nil {
		return err
	}
	inventory, err := t.lifecycleInventory(ctx, installer)
	if err != nil {
		return err
	}
	if err = checkLifecycleState(action, inventory, before); err != nil {
		return err
	}
	var config preservedUpdateConfig
	if action == lifecycleInstall {
		config, err = prepareUpstreamDeployConfig(action, r, nil, nil, false)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(writer, "CSQTT_PROGRESS|0.06|Подготовка и передача проверенных файлов")
	s, err := t.openSFTP(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	if err = s.workspace(ctx); err != nil {
		return err
	}
	uncertain := false
	defer func() {
		if uncertain {
			err = errors.Join(err, fmt.Errorf("исход удалённой команды неизвестен; staging сохранён до проверки: %s", s.dir))
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), stagingCleanupTimeout)
		defer cancel()
		err = errors.Join(err, cleanupRemoteStage(cleanup, t, s))
	}()
	files := []struct {
		name string
		data []byte
	}{{"deploy-csqtt-core-lifecycle.sh", installer}}
	if action != lifecycleRemove {
		files = append(files, struct {
			name string
			data []byte
		}{"csqtt-core8-linux-arm64", server})
	}
	if action == lifecycleInstall {
		files = append(files, struct {
			name string
			data []byte
		}{"csqtt-core.env", config.webEnvironment}, struct {
			name string
			data []byte
		}{"deploy-overrides.json", config.deployOverrides})
	}
	for _, file := range files {
		p, e := s.peer(file.name)
		if e != nil {
			return e
		}
		if e = verifyStagedFixture(ctx, p, file.data); e != nil {
			return e
		}
	}
	// Update creates these files from its own existing settings on the server.
	// Register exact names for cleanup before any command is started.
	if action == lifecycleUpdate {
		s.files = append(s.files, s.dir+"/csqtt-core.env", s.dir+"/deploy-overrides.json")
	}
	fmt.Fprintln(writer, "CSQTT_PROGRESS|0.18|Выполнение операции")
	receipt, e := t.execInstallerResult(ctx, s.dir, action, writer)
	if e != nil {
		uncertain = true
		return e
	}
	result := lifecycleReceiptResult(action, lifecycleReceipt{exit: receipt.exitStatus, output: receipt.stdout})
	// Errors on stderr must not be hidden by a success marker on stdout.
	if strings.Contains(receipt.stderr, "CSQTT_DEPLOY_ERROR|") && (result == "installed" || result == "uninstalled") {
		result = "error"
	}
	if result == "error" || result == "unknown" || result == "rollback_failed" {
		if result == "rollback_failed" {
			fmt.Fprintln(writer, "CSQTT_DEPLOY_ROLLBACK_FAILED")
		}
		return fmt.Errorf("установщик не подтвердил операцию (exit=%d, result=%s)", receipt.exitStatus, result)
	}
	fmt.Fprintln(writer, "CSQTT_PROGRESS|0.98|Независимая проверка результата")
	after, e := t.lifecycleEvidence(ctx, identity)
	if e != nil {
		return e
	}
	afterInventory, e := t.lifecycleInventory(ctx, installer)
	if e != nil {
		return e
	}
	if e = checkLifecyclePost(action, result, inventory, afterInventory, before, after); e != nil {
		return e
	}
	cleanup, finish := context.WithTimeout(context.Background(), stagingCleanupTimeout)
	e = cleanupRemoteStage(cleanup, t, s)
	finish()
	if e != nil {
		return e
	}
	if result == "restored" {
		fmt.Fprintln(writer, "CSQTT_DEPLOY_ROLLED_BACK")
		return errors.New("обновление не выполнено; прежнее состояние независимо подтверждено")
	}
	fmt.Fprintln(writer, "CSQTT_PROGRESS|1.0|Операция подтверждена")
	if action == lifecycleRemove {
		fmt.Fprintln(writer, "CSQTT_UNINSTALL_OK")
	} else {
		fmt.Fprintln(writer, "CSQTT_DEPLOY_OK")
	}
	return nil
}
