#!/usr/bin/env python3
"""Only test opt-in patch and self-contained Rust helper using fake local files.

Never execute Core8, modify /proc, run networking, or contact VK/TURN.
"""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

import importlib.util
SOURCE = Path(__file__).with_name("patch-core8-kernel-sysctl-restore.py")
modspec = importlib.util.spec_from_file_location("kernelrestore_patch", SOURCE)
patch = importlib.util.module_from_spec(modspec)
modspec.loader.exec_module(patch)

RUST_STUBS = r'''
// Minimal offline stubs: no actual kernel network, tun, iptables, or modprobe.
struct FirewallRule;
impl FirewallRule {
    fn install(&self) -> Result<(), std::io::Error> { Ok(()) }
    fn remove(&self) {}
}
const KERNEL_PARAMETERS: &[(&str, &str)] = &[];
fn purge_legacy_mss_clamp() {}
fn managed_firewall_rules(_: &str) -> Vec<FirewallRule> { vec![] }
'''

RUST_TESTS = r'''
#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;
    use std::time::{SystemTime, UNIX_EPOCH};
    fn tmp(label: &str) -> PathBuf {
        let n = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
        let path = std::env::temp_dir().join(format!(
            "csqtt-core8-offline-{label}-{}-{n}", std::process::id()
        ));
        std::fs::write(&path, "0\n").unwrap();
        path
    }

    #[test]
    fn changed_value_is_restored() {
        let file = tmp("restore");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "1").unwrap();
        assert_eq!(std::fs::read_to_string(path).unwrap(), "1");
        restore_kernel_parameter(&saved);
        assert_eq!(std::fs::read_to_string(path).unwrap(), "0");
        std::fs::remove_file(file).unwrap();
    }

    #[test]
    fn original_equal_to_requested_creates_no_ownership() {
        let file = tmp("no-change");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "0");
        assert!(saved.is_none());
        assert_eq!(std::fs::read_to_string(path).unwrap(), "0\n");
        std::fs::remove_file(file).unwrap();
    }

    #[test]
    fn independently_changed_value_is_not_overwritten() {
        let file = tmp("external");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "1").unwrap();
        std::fs::write(path, "2").unwrap();
        restore_kernel_parameter(&saved);
        assert_eq!(std::fs::read_to_string(path).unwrap(), "2");
        std::fs::remove_file(file).unwrap();
    }

    #[test]
    fn removed_target_is_not_recreated() {
        let file = tmp("missing");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "1").unwrap();
        std::fs::remove_file(path).unwrap();
        restore_kernel_parameter(&saved);
        assert!(!file.exists());
    }

    #[cfg(unix)]
    #[test]
    fn symlink_substitution_does_not_touch_outside_file() {
        use std::os::unix::fs::symlink;
        let file = tmp("symlink");
        let outside = tmp("outside");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "1").unwrap();
        std::fs::remove_file(path).unwrap();
        symlink(&outside, path).unwrap();
        restore_kernel_parameter(&saved);
        assert_eq!(std::fs::read_to_string(&outside).unwrap(), "0\n");
        assert!(set_kernel_parameter(path, "1").is_none());
        std::fs::remove_file(file).unwrap();
        std::fs::remove_file(outside).unwrap();
    }

    #[test]
    fn kernel_networking_raii_drop_restores_original() {
        let file = tmp("raii");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "1").unwrap();
        {
            let _owner = KernelNetworking {
                rules: vec![], changed_parameters: vec![saved],
            };
            assert_eq!(std::fs::read_to_string(path).unwrap(), "1");
        }
        assert_eq!(std::fs::read_to_string(path).unwrap(), "0");
        std::fs::remove_file(file).unwrap();
    }

    #[test]
    fn kernel_networking_raii_drop_respects_external_modification() {
        let file = tmp("raii-external");
        let path = file.to_str().unwrap();
        let saved = set_kernel_parameter(path, "1").unwrap();
        {
            let _owner = KernelNetworking {
                rules: vec![], changed_parameters: vec![saved],
            };
            std::fs::write(path, "2").unwrap();
        }
        assert_eq!(std::fs::read_to_string(path).unwrap(), "2");
        std::fs::remove_file(file).unwrap();
    }

    #[test]
    fn absent_path_never_written() {
        let file = tmp("absent");
        std::fs::remove_file(&file).unwrap();
        assert!(set_kernel_parameter(file.to_str().unwrap(), "1").is_none());
        assert!(!file.exists());
    }
}
'''

class SourceContractTests(unittest.TestCase):
    def test_patch_matches_only_original_once(self):
        example = "\n// previous\n" + patch.ORIGINAL_SET + patch.ORIGINAL_OWNER + "\n// next\n"
        updated = patch.patch_source(example)
        self.assertIn("for saved in self.changed_parameters.iter().rev()", updated)
        self.assertIn("restore_kernel_parameter(saved);", updated)
        self.assertIn('changed_parameters.push(saved)', updated)
        self.assertIn("purge_legacy_mss_clamp();", updated)
        self.assertIn("rule.remove();", updated)
        self.assertIn("let rules = managed_firewall_rules(tun_iface);", updated)
        with self.assertRaises(ValueError):
            patch.patch_source(updated)

    def test_mismatch_fails_closed(self):
        with self.assertRaises(ValueError):
            patch.patch_source("something completely different")
        with self.assertRaises(ValueError):
            patch.patch_source(patch.ORIGINAL_SET * 2 + patch.ORIGINAL_OWNER)

    def test_compiled_rust_semantics_only_in_temporary_files(self):
        # The Rust program includes ONLY the helper from the patch, no network
        # services, no system sysctls, no firewall or host-wide side effects.
        explicit = os.environ.get("CSQTT_TEST_RUSTC")
        rustc = explicit if explicit else "rustc"
        try:
            ver = subprocess.run([rustc, "--version"], capture_output=True,
                                 text=True, timeout=8, check=True)
        except (FileNotFoundError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
            self.skipTest("rustc not available; source-anchor checks still run")
        self.assertIn("rustc", ver.stdout)
        with tempfile.TemporaryDirectory(prefix="csqtt-core8-rust-fake-sysctl-") as tmp:
            src = Path(tmp) / "kernel_restore_mock.rs"
            binary = Path(tmp) / "kernel_restore_mock"
            src.write_text(patch.REPLACEMENT_SET + RUST_STUBS + patch.REPLACEMENT_OWNER + RUST_TESTS, encoding="utf8")
            subprocess.run([rustc, "--edition=2021", "--test",
                            str(src), "-o", str(binary)],
                           check=True, capture_output=True, text=True, timeout=35)
            output = subprocess.run([str(binary), "--test-threads=1"],
                                    capture_output=True, text=True, timeout=15)
            self.assertEqual(output.returncode, 0, output.stdout + output.stderr)
            self.assertIn("8 passed", output.stdout)

if __name__ == "__main__":
    unittest.main(verbosity=2)
