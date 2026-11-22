//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func singBoxRollbackTestManager(t *testing.T) *SingBoxManager {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "sing-box")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = check ]; then exit 0; fi\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return NewSingBoxManager(fake, filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
}

func singBoxRollbackTestInput(suffix string) singBoxConfigInput {
	return singBoxConfigInput{
		ListenAddress: "127.0.0.1:19080",
		CSQTTAddress: "127.0.0.1:19081",
		DirectDomainSuffixes: []string{suffix},
	}
}

func assertNoSingBoxStagingFiles(t *testing.T, m *SingBoxManager) {
	t.Helper()
	dir := filepath.Dir(m.configPath)
	for _, pattern := range []string{".sing-box-config-*.json", ".sing-box-panel-state-*.json"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil { t.Fatal(err) }
		if len(matches) != 0 { t.Fatalf("staged files remain after transaction: %q", matches) }
	}
}

func TestSingBoxSaveConfigRollbackPreservesBothFiles(t *testing.T) {
	m := singBoxRollbackTestManager(t)
	old := singBoxRollbackTestInput(".previous.example")
	old.Rules = []singBoxRouteRule{{ID:"disabled-backup", Outbound:singBoxDirectTag, Enabled:false}}
	old.SOCKS5 = []singBoxSOCKS5Outbound{{ID:"backup-server", Server:"127.0.0.1", ServerPort:18080, Username:"user", Password:"private-password", Enabled:false}}
	if err := m.SaveConfig(old); err != nil { t.Fatal(err) }
	previousConfig, _ := os.ReadFile(m.configPath)
	previousSidecar, _ := os.ReadFile(m.panelStatePath())

	m.renameOverride = func(from, to string) error {
		if to == m.panelStatePath() { return os.ErrPermission }
		return os.Rename(from, to)
	}
	err := m.SaveConfig(singBoxRollbackTestInput(".updated.example"))
	if err == nil || !strings.Contains(err.Error(), "previous config restored") {
		t.Fatalf("expected rollback after panel rename failure, got %v", err)
	}
	gotConfig, _ := os.ReadFile(m.configPath)
	gotSidecar, _ := os.ReadFile(m.panelStatePath())
	if string(gotConfig) != string(previousConfig) || string(gotSidecar) != string(previousSidecar) {
		t.Fatal("failed second rename changed old config or panel state")
	}
	loaded, err := m.LoadConfig()
	if err != nil { t.Fatalf("old config cannot be loaded after rollback: %v", err) }
	if !reflect.DeepEqual(loaded, old) { t.Fatalf("settings changed after rollback: %#v", loaded) }
	if !strings.Contains(m.Status().Error, "restored") { t.Fatalf("missing rollback status: %q", m.Status().Error) }
	info, _ := os.Stat(m.configPath)
	if info.Mode().Perm() != 0600 { t.Fatalf("rollback changed config permissions to %o", info.Mode().Perm()) }
	assertNoSingBoxStagingFiles(t, m)
}

func TestSingBoxSaveConfigInitialPanelFailureRemovesNewConfig(t *testing.T) {
	m := singBoxRollbackTestManager(t)
	m.renameOverride = func(from, to string) error {
		if to == m.panelStatePath() { return errors.New("forced panel write failure") }
		return os.Rename(from, to)
	}
	if err := m.SaveConfig(singBoxRollbackTestInput(".new.example")); err == nil {
		t.Fatal("first-time install should refuse panel write failure")
	}
	for _, name := range []string{m.configPath, m.panelStatePath()} {
		if _, err := os.Lstat(name); !os.IsNotExist(err) {
			t.Fatalf("partially installed config remains at %s: %v", name, err)
		}
	}
	assertNoSingBoxStagingFiles(t, m)
}

func TestSingBoxSaveConfigMainRenameFailureLeavesOldPairIntact(t *testing.T) {
	m := singBoxRollbackTestManager(t)
	old := singBoxRollbackTestInput(".before.example")
	if err := m.SaveConfig(old); err != nil { t.Fatal(err) }
	configBefore, _ := os.ReadFile(m.configPath)
	stateBefore, _ := os.ReadFile(m.panelStatePath())
	m.renameOverride = func(from, to string) error {
		if to == m.configPath { return os.ErrPermission }
		return os.Rename(from, to)
	}
	if err := m.SaveConfig(singBoxRollbackTestInput(".after.example")); err == nil {
		t.Fatal("failed config rename should fail SaveConfig")
	}
	configAfter, _ := os.ReadFile(m.configPath)
	stateAfter, _ := os.ReadFile(m.panelStatePath())
	if string(configBefore) != string(configAfter) || string(stateBefore) != string(stateAfter) {
		t.Fatal("failed initial rename changed last-known-good pair")
	}
	assertNoSingBoxStagingFiles(t, m)
}

func TestSingBoxSaveConfigRollbackFailureKeepsRecoverySnapshot(t *testing.T) {
	m := singBoxRollbackTestManager(t)
	old := singBoxRollbackTestInput(".recover.example")
	if err := m.SaveConfig(old); err != nil { t.Fatal(err) }
	previous, _ := os.ReadFile(m.configPath)
	// Simulate a legacy config with overly broad filesystem permissions.
	if err := os.Chmod(m.configPath,0644); err != nil { t.Fatal(err) }
	replacements := 0
	m.renameOverride = func(from, to string) error {
		if to == m.panelStatePath() { return os.ErrPermission }
		if to == m.configPath {
			replacements++
			if replacements > 1 { return os.ErrPermission } // rollback failure only
		}
		return os.Rename(from, to)
	}
	err := m.SaveConfig(singBoxRollbackTestInput(".bad.example"))
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("must report rollback failure, got %v", err)
	}
	recovery, globErr := filepath.Glob(filepath.Join(filepath.Dir(m.configPath), ".sing-box-config-rollback-*.json"))
	if globErr != nil || len(recovery) != 1 {
		t.Fatalf("last known-good recovery snapshot unavailable: %q, %v", recovery, globErr)
	}
	bytes, readErr := os.ReadFile(recovery[0])
	if readErr != nil || string(bytes) != string(previous) {
		t.Fatalf("recovery snapshot does not contain previous config: %v", readErr)
	}
	info, _ := os.Stat(recovery[0])
	if info.Mode().Perm() != 0600 { t.Fatalf("recovery snapshot mode %o", info.Mode().Perm()) }
	if !strings.Contains(m.Status().Error, recovery[0]) {
		t.Fatalf("recovery path not available in manager diagnostic: %q", m.Status().Error)
	}
}

func TestSingBoxSaveConfigRejectsNonRegularPreviousConfig(t *testing.T) {
	m := singBoxRollbackTestManager(t)
	external := filepath.Join(filepath.Dir(m.configPath), "external.txt")
	if err := os.WriteFile(external, []byte("not-managed"), 0600); err != nil { t.Fatal(err) }
	if err := os.Symlink(external, m.configPath); err != nil { t.Fatal(err) }
	if err := m.SaveConfig(singBoxRollbackTestInput(".new.example")); err == nil {
		t.Fatal("symlinked existing config was accepted")
	}
	content, _ := os.ReadFile(external)
	if string(content) != "not-managed" { t.Fatal("unrelated file was changed") }
	if _, err := os.Lstat(m.panelStatePath()); !os.IsNotExist(err) {
		t.Fatalf("unexpected sidecar after rejecting symlink: %v", err)
	}
}

func TestSingBoxSaveConfigSuccessCleansRollbackSnapshot(t *testing.T) {
	m := singBoxRollbackTestManager(t)
	if err := m.SaveConfig(singBoxRollbackTestInput(".old.example")); err != nil { t.Fatal(err) }
	current := singBoxRollbackTestInput(".new.example")
	if err := m.SaveConfig(current); err != nil { t.Fatal(err) }
	got, err := m.LoadConfig()
	if err != nil || !reflect.DeepEqual(got, current) { t.Fatalf("committed pair not round tripped: %#v %v", got, err) }
	assertNoSingBoxStagingFiles(t, m)
}
