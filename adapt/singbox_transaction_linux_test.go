//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testSingBoxCrashManager(t *testing.T) (*SingBoxManager, singBoxConfigInput) {
	t.Helper()
	m := singBoxRollbackTestManager(t)
	old := singBoxRollbackTestInput(".old.example")
	old.Rules = []singBoxRouteRule{{ID: "disabled-backup", Outbound: singBoxDirectTag, Enabled: false}}
	old.SOCKS5 = []singBoxSOCKS5Outbound{{ID: "old-server", Server: "127.0.0.1", ServerPort: 18080, Username: "user", Password: "private-password", Enabled: false}}
	if err := m.SaveConfig(old); err != nil {
		t.Fatal(err)
	}
	return m, old
}

func testPrepareSingBoxCrash(t *testing.T, m *SingBoxManager, input singBoxConfigInput) ([]byte, []byte) {
	t.Helper()
	config, err := buildSingBoxConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.MarshalIndent(singBoxPanelState{Version: 1, ConfigSHA256: singBoxHash(config), Config: input}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	state = append(state, '\n')
	if err := m.writeTransactionJournal(config, state); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(m.transactionPath())
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("transaction journal must be 0600: %v, %v", stat, err)
	}
	return config, state
}

func testJournalGone(t *testing.T, m *SingBoxManager) {
	t.Helper()
	if _, err := os.Lstat(m.transactionPath()); !os.IsNotExist(err) {
		t.Fatalf("unexpected recovery journal after completed reload: %v", err)
	}
}

func TestSingBoxCrashBeforePublishingRetainsOriginal(t *testing.T) {
	m, old := testSingBoxCrashManager(t)
	testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	loaded, err := restarted.LoadConfig()
	if err != nil || !reflect.DeepEqual(loaded, old) {
		t.Fatalf("prepublish recovery: %v, %#v", err, loaded)
	}
	testJournalGone(t, restarted)
}

func TestSingBoxCrashBetweenConfigAndPanelRestoresOldSettings(t *testing.T) {
	m, old := testSingBoxCrashManager(t)
	beforeConfig, _ := os.ReadFile(m.configPath)
	beforePanel, _ := os.ReadFile(m.panelStatePath())
	newConfig, _ := testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
	if err := writeSingBoxRecoveredFile(m.configPath, newConfig); err != nil {
		t.Fatal(err)
	}
	if err := syncSingBoxDirectory(filepath.Dir(m.configPath)); err != nil {
		t.Fatal(err)
	}
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	loaded, err := restarted.LoadConfig()
	if err != nil || !reflect.DeepEqual(loaded, old) {
		t.Fatalf("recovery lost disabled routes / SOCKS5: %v, %#v", err, loaded)
	}
	config, _ := os.ReadFile(m.configPath)
	panel, _ := os.ReadFile(m.panelStatePath())
	if string(config) != string(beforeConfig) || string(panel) != string(beforePanel) {
		t.Fatal("old pair not restored byte-for-byte")
	}
	testJournalGone(t, restarted)
}

func TestSingBoxCrashAfterBothFilesKeepsCommittedPair(t *testing.T) {
	m, _ := testSingBoxCrashManager(t)
	now := singBoxRollbackTestInput(".committed.example")
	newConfig, newPanel := testPrepareSingBoxCrash(t, m, now)
	if err := writeSingBoxRecoveredFile(m.configPath, newConfig); err != nil {
		t.Fatal(err)
	}
	if err := writeSingBoxRecoveredFile(m.panelStatePath(), newPanel); err != nil {
		t.Fatal(err)
	}
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	loaded, err := restarted.LoadConfig()
	if err != nil || !reflect.DeepEqual(loaded, now) {
		t.Fatalf("completed pair was rolled back: %v, %#v", err, loaded)
	}
	testJournalGone(t, restarted)
}

func TestSingBoxCrashFirstSaveBeforeAndBetweenRenames(t *testing.T) {
	for _, phase := range []string{"before", "between"} {
		t.Run(phase, func(t *testing.T) {
			m := singBoxRollbackTestManager(t)
			newConfig, _ := testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
			if phase == "between" {
				if err := writeSingBoxRecoveredFile(m.configPath, newConfig); err != nil {
					t.Fatal(err)
				}
			}
			restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
			if _, err := restarted.LoadConfig(); err == nil {
				t.Fatal("first save crash should not create unpaired config")
			}
			for _, path := range []string{m.configPath, m.panelStatePath()} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("partial first save remains at %s: %v", path, err)
				}
			}
			testJournalGone(t, restarted)
		})
	}
}

func TestSingBoxCrashDuringRecoveryIsIdempotent(t *testing.T) {
	m, old := testSingBoxCrashManager(t)
	newConfig, newPanel := testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
	if err := writeSingBoxRecoveredFile(m.configPath, newConfig); err != nil {
		t.Fatal(err)
	}
	if err := writeSingBoxRecoveredFile(m.panelStatePath(), newPanel); err != nil {
		t.Fatal(err)
	}
	// Emulate interruption of rollback itself: config was restored, panel
	// still refers to the newly committed state, journal was not removed.
	j, err := m.readTransactionJournal()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSingBoxRecoveredFile(m.configPath, j.OldConfig); err != nil {
		t.Fatal(err)
	}
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	got, err := restarted.LoadConfig()
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("idempotent rollback recovery: %v, %#v", err, got)
	}
	testJournalGone(t, restarted)
}

func TestSingBoxCrashRefusesCorruptedJournalWithoutModification(t *testing.T) {
	m, _ := testSingBoxCrashManager(t)
	original, _ := os.ReadFile(m.configPath)
	testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
	if err := os.WriteFile(m.transactionPath(), []byte("corrupt-json"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	if _, err := restarted.LoadConfig(); err == nil {
		t.Fatal("corrupt journal was accepted")
	}
	config, _ := os.ReadFile(m.configPath)
	if string(config) != string(original) {
		t.Fatal("corrupt recovery journal changed original config")
	}
	if _, err := os.Lstat(m.transactionPath()); err != nil {
		t.Fatal("corrupt journal was removed")
	}
}

func TestSingBoxCrashRejectsSymlinkedOrPermissiveJournal(t *testing.T) {
	for _, kind := range []string{"symlink", "0644"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := testSingBoxCrashManager(t)
			config, _ := os.ReadFile(m.configPath)
			if kind == "symlink" {
				outside := filepath.Join(filepath.Dir(m.configPath), "unrelated.txt")
				if err := os.WriteFile(outside, []byte("not-managed"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, m.transactionPath()); err != nil {
					t.Fatal(err)
				}
			} else {
				testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
				if err := os.Chmod(m.transactionPath(), 0644); err != nil {
					t.Fatal(err)
				}
			}
			restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
			if _, err := restarted.LoadConfig(); err == nil {
				t.Fatal("unsafe journal accepted")
			}
			got, _ := os.ReadFile(m.configPath)
			if string(got) != string(config) {
				t.Fatal("unsafe journal modified config")
			}
		})
	}
}

func TestSingBoxCrashRejectsExternalConfigMutation(t *testing.T) {
	m, _ := testSingBoxCrashManager(t)
	testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
	newData := []byte("{\"external\":\"unrelated changes\"}")
	if err := os.WriteFile(m.configPath, newData, 0600); err != nil {
		t.Fatal(err)
	}
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	_, err := restarted.LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "outside interrupted transaction") {
		t.Fatalf("external update should block recovery, got %v", err)
	}
	current, _ := os.ReadFile(m.configPath)
	if string(current) != string(newData) {
		t.Fatal("external edit was overwritten")
	}
	if _, err := os.Stat(m.transactionPath()); err != nil {
		t.Fatal("recovery journal vanished after external change")
	}
}

func TestSingBoxCrashRecoveredBeforeNewSave(t *testing.T) {
	m, _ := testSingBoxCrashManager(t)
	newConfig, _ := testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".new.example"))
	if err := writeSingBoxRecoveredFile(m.configPath, newConfig); err != nil {
		t.Fatal(err)
	}
	restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
	final := singBoxRollbackTestInput(".final.example")
	if err := restarted.SaveConfig(final); err != nil {
		t.Fatalf("new SaveConfig did not first recover pending transaction: %v", err)
	}
	loaded, err := restarted.LoadConfig()
	if err != nil || !reflect.DeepEqual(loaded, final) {
		t.Fatalf("new config after recovery: %v, %#v", err, loaded)
	}
	testJournalGone(t, restarted)
}

// TestSingBoxCrashSubprocessWorker intentionally terminates the process via
// os.Exit at a selected rename boundary. It is launched by its parent test.
func TestSingBoxCrashSubprocessWorker(t *testing.T) {
	phase := os.Getenv("CSQTT_SINGBOX_CRASH_PHASE")
	if phase == "" {
		return
	}
	dir := os.Getenv("CSQTT_SINGBOX_CRASH_DIR")
	if dir == "" {
		t.Fatal("missing isolated crash directory")
	}
	m := NewSingBoxManager(filepath.Join(dir, "sing-box"), filepath.Join(dir, "config.json"), filepath.Join(dir, "sing-box.log"))
	m.renameOverride = func(from, to string) error {
		if to == m.configPath && phase == "before" {
			os.Exit(91)
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		if to == m.configPath && phase == "between" {
			os.Exit(92)
		}
		if to == m.panelStatePath() && phase == "after" {
			os.Exit(93)
		}
		return nil
	}
	if err := m.SaveConfig(singBoxRollbackTestInput(".after-process-death.example")); err != nil {
		t.Fatalf("worker SaveConfig before injected process exit: %v", err)
	}
	t.Fatal("worker unexpectedly exited without injected process death")
}

func TestSingBoxCrashRealProcessExitAtRenameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		phase string
		exit  int
		newer bool
	}{
		{"before", 91, false}, {"between", 92, false}, {"after", 93, true},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			m, old := testSingBoxCrashManager(t)
			child := exec.Command(os.Args[0], "-test.run=^TestSingBoxCrashSubprocessWorker$")
			child.Env = append(os.Environ(),
				"CSQTT_SINGBOX_CRASH_PHASE="+tc.phase,
				"CSQTT_SINGBOX_CRASH_DIR="+filepath.Dir(m.configPath))
			output, err := child.CombinedOutput()
			var status *exec.ExitError
			if !errors.As(err, &status) || status.ExitCode() != tc.exit {
				t.Fatalf("child did not die at expected durable-write boundary, code=%d, err=%v, output=%s", tc.exit, err, string(output))
			}
			marker, statErr := os.Lstat(m.transactionPath())
			if statErr != nil || marker.Mode().Perm() != 0600 {
				t.Fatalf("no durable private journal after abrupt exit: %v, %v", marker, statErr)
			}
			// New manager is a new process incarnation: it is not allowed to
			// remember in-memory state from before os.Exit.
			restarted := NewSingBoxManager(m.binary, m.configPath, m.logPath)
			got, loadErr := restarted.LoadConfig()
			expected := old
			if tc.newer {
				expected = singBoxRollbackTestInput(".after-process-death.example")
			}
			if loadErr != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("wrong config after process death at %s: %v, got=%#v", tc.phase, loadErr, got)
			}
			testJournalGone(t, restarted)
		})
	}
}

func TestSingBoxCrashRecoveredBeforeRuleSetDelete(t *testing.T) {
	m, old := testSingBoxCrashManager(t)
	nextConfig, _ := testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".interrupted.example"))
	if err := writeSingBoxRecoveredFile(m.configPath, nextConfig); err != nil {
		t.Fatal(err)
	}
	// DeleteRuleSet holds m.mu while reading the config. The recovery
	// cannot attempt to acquire that same mutex recursively.
	if err := m.DeleteRuleSet("unregistered"); err != nil {
		t.Fatalf("delete after interrupted save failed: %v", err)
	}
	loaded, err := m.LoadConfig()
	if err != nil || !reflect.DeepEqual(loaded, old) {
		t.Fatalf("Rule Set deletion failed to preserve restored config: %v, %#v", err, loaded)
	}
	testJournalGone(t, m)
}

func TestSingBoxCrashRecoveredBeforeRuleSetUpload(t *testing.T) {
	m, old := testSingBoxCrashManager(t)
	nextConfig, _ := testPrepareSingBoxCrash(t, m, singBoxRollbackTestInput(".interrupted.example"))
	if err := writeSingBoxRecoveredFile(m.configPath, nextConfig); err != nil {
		t.Fatal(err)
	}
	_, err := m.UploadRuleSet("recovered", "recovered.srs", []byte("compiled-srs"))
	if err != nil {
		t.Fatalf("Rule Set upload after interrupted save: %v", err)
	}
	loaded, err := m.LoadConfig()
	if err != nil || !reflect.DeepEqual(loaded, old) {
		t.Fatalf("Rule Set upload failed to preserve restored config: %v, %#v", err, loaded)
	}
	testJournalGone(t, m)
}
