//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
 "syscall"
)

// Transaction contains confidential old configuration and panel data;
// it must never be printed, transmitted, or stored outside a private 0600 file.
const singBoxTxnVersion = 1
const singBoxTxnMaxFile = 32 << 20
const singBoxTxnMaxJournal = 128 << 20

type singBoxTransaction struct {
	Version         int
	HadConfig       bool
	HadPanel        bool
	OldConfig       []byte
	OldPanel        []byte
	OldConfigSHA256 string
	OldPanelSHA256  string
	NewConfigSHA256 string
	NewPanelSHA256  string
}

func singBoxHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func syncSingBoxDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func readSingBoxRegular(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("unsafe nonregular Sing-box file %s", path)
	}
	if info.Size() > singBoxTxnMaxFile {
		return nil, false, fmt.Errorf("Sing-box file too large %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if len(data) > singBoxTxnMaxFile {
		return nil, false, fmt.Errorf("Sing-box file too large %s", path)
	}
	return data, true, nil
}
func validateSingBoxOldPanel(config []byte, hasConfig bool, panel []byte, hasPanel bool) error {
	if !hasPanel {
		return nil
	}
	if !hasConfig {
		return fmt.Errorf("Sing-box panel exists without config")
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(panel, &state); err != nil {
		return fmt.Errorf("invalid previous Sing-box panel: %w", err)
	}
	var version int
	var expectedHash string
	if err := json.Unmarshal(state["version"], &version); err != nil {
		return fmt.Errorf("invalid previous Sing-box panel version: %w", err)
	}
	if err := json.Unmarshal(state["config_sha256"], &expectedHash); err != nil {
		return fmt.Errorf("invalid previous Sing-box panel hash: %w", err)
	}
	if version != 1 || expectedHash != singBoxHash(config) {
		return fmt.Errorf("existing Sing-box config/panel state already differ; refusing update")
	}
	return nil
}
func (m *SingBoxManager) transactionPath() string { return m.configPath + ".transaction.json" }

func (m *SingBoxManager) writeTransactionJournal(newConfig, newPanel []byte) error {
	if _, err := os.Lstat(m.transactionPath()); err == nil {
		return fmt.Errorf("pending Sing-box transaction requires recovery")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldConfig, hasConfig, err := readSingBoxRegular(m.configPath)
	if err != nil {
		return err
	}
	oldPanel, hasPanel, err := readSingBoxRegular(m.panelStatePath())
	if err != nil {
		return err
	}
	if err := validateSingBoxOldPanel(oldConfig, hasConfig, oldPanel, hasPanel); err != nil {
		return err
	}
	j := singBoxTransaction{
		Version: singBoxTxnVersion, HadConfig: hasConfig, HadPanel: hasPanel,
		OldConfig: oldConfig, OldPanel: oldPanel,
		OldConfigSHA256: singBoxHash(oldConfig), OldPanelSHA256: singBoxHash(oldPanel),
		NewConfigSHA256: singBoxHash(newConfig), NewPanelSHA256: singBoxHash(newPanel),
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.configPath)
	f, err := os.CreateTemp(dir, ".sing-box-txn-staging-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.transactionPath()); err != nil {
		return err
	}
	// Durably publish the rollback journal BEFORE publishing the new pair.
	return syncSingBoxDirectory(dir)
}
func (m *SingBoxManager) readTransactionJournal() (*singBoxTransaction, error) {
	info, err := os.Lstat(m.transactionPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > singBoxTxnMaxJournal {
		return nil, fmt.Errorf("unsafe Sing-box transaction journal: requires regular private 0600 file")
	}
	data, err := os.ReadFile(m.transactionPath())
	if err != nil {
		return nil, fmt.Errorf("read Sing-box transaction: %w", err)
	}
	if len(data) > singBoxTxnMaxJournal {
		return nil, fmt.Errorf("oversized Sing-box transaction journal")
	}
	var j singBoxTransaction
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("invalid Sing-box transaction journal: %w", err)
	}
	if j.Version != singBoxTxnVersion || len(j.OldConfig) > singBoxTxnMaxFile ||
		len(j.OldPanel) > singBoxTxnMaxFile || j.OldConfigSHA256 != singBoxHash(j.OldConfig) ||
		j.OldPanelSHA256 != singBoxHash(j.OldPanel) {
		return nil, fmt.Errorf("corrupt Sing-box transaction journal; refusing recovery")
	}
	for _, h := range []string{j.NewConfigSHA256, j.NewPanelSHA256} {
		b, e := hex.DecodeString(h)
		if e != nil || len(b) != 32 || h != hex.EncodeToString(b) {
			return nil, fmt.Errorf("invalid Sing-box target SHA256")
		}
	}
	if !j.HadConfig && (len(j.OldConfig) != 0 || j.HadPanel) {
		return nil, fmt.Errorf("invalid old Sing-box config in journal")
	}
	if !j.HadPanel && len(j.OldPanel) != 0 {
		return nil, fmt.Errorf("invalid old Sing-box panel in journal")
	}
	if err := validateSingBoxOldPanel(j.OldConfig, j.HadConfig, j.OldPanel, j.HadPanel); err != nil {
		return nil, err
	}
	return &j, nil
}
func writeSingBoxRecoveredFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".sing-box-restore-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func restoreSingBoxPrevious(path string, data []byte, hadFile bool) error {
	if _, _, err := readSingBoxRegular(path); err != nil {
		return err
	}
	if hadFile {
		return writeSingBoxRecoveredFile(path, data)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (m *SingBoxManager) clearTransactionJournal() error {
	if err := os.Remove(m.transactionPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncSingBoxDirectory(filepath.Dir(m.configPath))
}
func (m *SingBoxManager) recoverPendingTransaction() error {
	j, err := m.readTransactionJournal()
	if err != nil {
		return err
	}
	if j == nil {
		return nil
	}
	config, hasConfig, err := readSingBoxRegular(m.configPath)
	if err != nil {
		return err
	}
	panel, hasPanel, err := readSingBoxRegular(m.panelStatePath())
	if err != nil {
		return err
	}
	// A changed third-party config is NOT evidence of our interrupted rename:
	// refuse to erase it automatically, preserving the transaction for review.
	if hasConfig && singBoxHash(config) != j.OldConfigSHA256 && singBoxHash(config) != j.NewConfigSHA256 {
		return fmt.Errorf("Sing-box active config changed outside interrupted transaction; refusing recovery")
	}
	if hasPanel && singBoxHash(panel) != j.OldPanelSHA256 && singBoxHash(panel) != j.NewPanelSHA256 {
		return fmt.Errorf("Sing-box panel changed outside interrupted transaction; refusing recovery")
	}
	// Fully published pair wins; a crash before journal cleanup is not rollback.
	if hasConfig && hasPanel && singBoxHash(config) == j.NewConfigSHA256 && singBoxHash(panel) == j.NewPanelSHA256 {
		return m.clearTransactionJournal()
	}
	if hasConfig == j.HadConfig && hasPanel == j.HadPanel &&
		(!hasConfig || singBoxHash(config) == j.OldConfigSHA256) &&
		(!hasPanel || singBoxHash(panel) == j.OldPanelSHA256) {
		return m.clearTransactionJournal()
	}
	// Replaying the old pair is idempotent across repeated interrupted recoveries.
	if err := restoreSingBoxPrevious(m.configPath, j.OldConfig, j.HadConfig); err != nil {
		return fmt.Errorf("recover old Sing-box config: %w", err)
	}
	if err := restoreSingBoxPrevious(m.panelStatePath(), j.OldPanel, j.HadPanel); err != nil {
		return fmt.Errorf("recover old Sing-box panel: %w", err)
	}
	if err := syncSingBoxDirectory(filepath.Dir(m.configPath)); err != nil {
		return err
	}
	return m.clearTransactionJournal()
}
func (m *SingBoxManager) recoverPendingTransactionForLoad() error {
	m.mu.Lock()
	configuring, running := m.configuring, m.cmd != nil
	m.mu.Unlock()
	if configuring {
		return fmt.Errorf("Sing-box config save is in progress")
	}
	if running {
		if j, err := m.readTransactionJournal(); err != nil {
			return err
		} else if j != nil {
			return fmt.Errorf("cannot recover Sing-box transaction while process runs")
		}
		return nil
	}
	return m.recoverPendingTransaction()
}

// An independent durable journal records the live-apply commit decision.
// Publishing config/panel does NOT imply that the new listener was healthy.
const singBoxLiveApplyVersion = 1

type singBoxLiveApplyJournal struct {
 Version int
 OldConfig []byte
 OldPanel []byte
 OldConfigSHA256 string
 OldPanelSHA256 string
 NewConfigSHA256 string
 NewPanelSHA256 string
}

func (m *SingBoxManager) liveApplyJournalPath() string { return m.configPath + ".live-apply.json" }

func (m *SingBoxManager) readLiveApplyJournal() (*singBoxLiveApplyJournal,error) {
 info,err:=os.Lstat(m.liveApplyJournalPath())
 if errors.Is(err,os.ErrNotExist){return nil,nil}
 if err!=nil{return nil,err}
 if !info.Mode().IsRegular() || info.Mode().Perm()&0077!=0 || info.Size()>singBoxTxnMaxJournal {
  return nil,fmt.Errorf("unsafe sing-box live-apply journal (requires private regular file)")
 }
 data,err:=os.ReadFile(m.liveApplyJournalPath())
 if err!=nil{return nil,err}
 if len(data)>singBoxTxnMaxJournal{return nil,fmt.Errorf("oversized sing-box live-apply journal")}
 var j singBoxLiveApplyJournal
 if err:=json.Unmarshal(data,&j);err!=nil{return nil,fmt.Errorf("invalid sing-box live-apply journal: %w",err)}
 if j.Version!=singBoxLiveApplyVersion || len(j.OldConfig)>singBoxTxnMaxFile || len(j.OldPanel)>singBoxTxnMaxFile ||
  j.OldConfigSHA256!=singBoxHash(j.OldConfig) || j.OldPanelSHA256!=singBoxHash(j.OldPanel) {
  return nil,fmt.Errorf("corrupted sing-box live-apply journal")
 }
 if err:=validateSingBoxOldPanel(j.OldConfig,true,j.OldPanel,true);err!=nil{return nil,err}
 for _,hash:=range []string{j.NewConfigSHA256,j.NewPanelSHA256}{
  raw,e:=hex.DecodeString(hash)
  if e!=nil||len(raw)!=32||hex.EncodeToString(raw)!=hash{return nil,fmt.Errorf("invalid live-apply target SHA256")}
 }
 return &j,nil
}

func (m *SingBoxManager) writeLiveApplyJournal(oldConfig,oldPanel,newConfig,newPanel []byte) error {
 if existing,err:=m.readLiveApplyJournal();err!=nil{return err}else if existing!=nil{
  return fmt.Errorf("prior uncommitted sing-box live apply requires recovery before starting another")
 }
 if len(oldConfig)>singBoxTxnMaxFile||len(oldPanel)>singBoxTxnMaxFile||
  len(newConfig)>singBoxTxnMaxFile||len(newPanel)>singBoxTxnMaxFile {
  return fmt.Errorf("sing-box live-apply snapshot exceeds file size limit")
 }
 if err:=validateSingBoxOldPanel(oldConfig,true,oldPanel,true);err!=nil{return err}
 j:=singBoxLiveApplyJournal{Version:singBoxLiveApplyVersion,
  OldConfig:oldConfig,OldPanel:oldPanel,
  OldConfigSHA256:singBoxHash(oldConfig),OldPanelSHA256:singBoxHash(oldPanel),
  NewConfigSHA256:singBoxHash(newConfig),NewPanelSHA256:singBoxHash(newPanel),
 }
 payload,err:=json.Marshal(j);if err!=nil{return err}
 dir:=filepath.Dir(m.configPath)
 f,err:=os.CreateTemp(dir,".sing-box-live-apply-*.json")
 if err!=nil{return err}
 path:=f.Name()
 defer os.Remove(path)
 if err=f.Chmod(0600);err!=nil{_ = f.Close();return err}
 if _,err=f.Write(payload);err!=nil{_ = f.Close();return err}
 if err=f.Sync();err!=nil{_ = f.Close();return err}
 if err=f.Close();err!=nil{return err}
 if err=os.Rename(path,m.liveApplyJournalPath());err!=nil{return err}
 return syncSingBoxDirectory(dir)
}

// A controller crash can strand the checked candidate's private preflight
// file. Remove ONLY our regular, private, same-user staging files after the
// verified old pair has been recovered. Never follow or delete symlinks.
func (m *SingBoxManager) cleanupRecoveredLivePreflights() error {
 dir:=filepath.Dir(m.configPath)
 names,err:=filepath.Glob(filepath.Join(dir,".sing-box-live-preflight-*.json"))
 if err!=nil{return err}
 // Fail closed before deleting anything if one entry is unsafe.
 for _,name:=range names {
  info,err:=os.Lstat(name)
  if err!=nil{return fmt.Errorf("inspect Sing-box live preflight: %w",err)}
  owner,ok:=info.Sys().(*syscall.Stat_t)
  if !ok || !info.Mode().IsRegular() || info.Mode().Perm()!=0600 ||
      info.Size()>singBoxTxnMaxFile || owner.Uid!=uint32(os.Geteuid()) {
   return fmt.Errorf("unsafe orphan Sing-box live preflight; refusing recovery cleanup")
  }
 }
 for _,name:=range names {
  if err:=os.Remove(name);err!=nil{
   return fmt.Errorf("remove validated orphan Sing-box live preflight: %w",err)
  }
 }
 if len(names)>0{return syncSingBoxDirectory(dir)}
 return nil
}

func (m *SingBoxManager) clearLiveApplyJournal() error {
 if err:=os.Remove(m.liveApplyJournalPath());err!=nil&&!errors.Is(err,os.ErrNotExist){return err}
 return syncSingBoxDirectory(filepath.Dir(m.configPath))
}

// Recovery is idempotent and fail-closed; never overwrite a third-party edit.
// A completely published candidate is still uncommitted until health passes.
func (m *SingBoxManager) recoverInterruptedLiveApply() error {
 j,err:=m.readLiveApplyJournal()
 if err!=nil{return err}
 if j==nil{return nil}
 if err=m.recoverPendingTransaction();err!=nil{return fmt.Errorf("recover inner sing-box transaction: %w",err)}
 config,hasConfig,err:=readSingBoxRegular(m.configPath);if err!=nil{return err}
 panel,hasPanel,err:=readSingBoxRegular(m.panelStatePath());if err!=nil{return err}
 if !hasConfig||!hasPanel{return fmt.Errorf("live-apply recovery refuses missing config or panel; journal preserved")}
 if h:=singBoxHash(config);h!=j.OldConfigSHA256&&h!=j.NewConfigSHA256{
  return fmt.Errorf("live-apply recovery refuses externally modified config; journal preserved")
 }
 if h:=singBoxHash(panel);h!=j.OldPanelSHA256&&h!=j.NewPanelSHA256{
  return fmt.Errorf("live-apply recovery refuses externally modified panel; journal preserved")
 }
 if singBoxHash(config)!=j.OldConfigSHA256 {
  if err:=writeSingBoxRecoveredFile(m.configPath,j.OldConfig);err!=nil{return fmt.Errorf("restore previous sing-box config: %w",err)}
 }
 if singBoxHash(panel)!=j.OldPanelSHA256 {
  if err:=writeSingBoxRecoveredFile(m.panelStatePath(),j.OldPanel);err!=nil{return fmt.Errorf("restore previous sing-box panel: %w",err)}
 }
 if err:=syncSingBoxDirectory(filepath.Dir(m.configPath));err!=nil{return err}
 restoredConfig,_,err:=readSingBoxRegular(m.configPath);if err!=nil{return err}
 restoredPanel,_,err:=readSingBoxRegular(m.panelStatePath());if err!=nil{return err}
 if singBoxHash(restoredConfig)!=j.OldConfigSHA256||singBoxHash(restoredPanel)!=j.OldPanelSHA256{
  return fmt.Errorf("sing-box live-apply rollback verification failed; journal preserved")
 }
 if err:=m.cleanupRecoveredLivePreflights();err!=nil{return err}
 return m.clearLiveApplyJournal()
}

func (m *SingBoxManager) recoverInterruptedLiveApplyForLoad() error {
 m.mu.Lock()
 applying,configuring,running:=m.applying,m.configuring,m.cmd!=nil
 m.mu.Unlock()
 if applying||configuring{return fmt.Errorf("sing-box apply/save in progress")}
 if running {
  if j,err:=m.readLiveApplyJournal();err!=nil{return err}else if j!=nil{
   return fmt.Errorf("cannot recover incomplete sing-box live apply while engine is running")
  }
  return nil
 }
 return m.recoverInterruptedLiveApply()
}
