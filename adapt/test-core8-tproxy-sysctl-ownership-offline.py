#!/usr/bin/env python3
"""Only offline source-contract and compiled Rust fake-file tests for TPROXY.

No /proc reads/writes, no IPC/network calls and no guest binaries or credentials.
"""
from pathlib import Path
import importlib.util
import os
import subprocess
import tempfile
import unittest

HERE = Path(__file__).parent
PATCH = HERE / "patch-core8-tproxy-sysctl-ownership.py"
spec = importlib.util.spec_from_file_location("tproxy_owner_patch", PATCH)
patch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(patch)

RUST_CASES = r'''
#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn fixtures() -> (PathBuf, Vec<(String, &'static str)>) {
        let unique = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
        let root = std::env::temp_dir().join(format!(
            "csqtt-tproxy-offline-{}-{unique}", std::process::id()
        ));
        std::fs::create_dir(&root).unwrap();
        let mut parameters = Vec::new();
        for (name, value) in [
            ("forwarding", "1"), ("rp_filter_all", "2"),
            ("rp_filter_default", "2"), ("rp_filter_tun", "2"),
        ] {
            let file = root.join(name);
            std::fs::write(&file, format!("{value}\n")).unwrap();
            parameters.push((file.to_str().unwrap().to_string(), value));
        }
        (root, parameters)
    }

    #[test]
    fn policy_valid() {
        let (root, params) = fixtures();
        assert!(check_tproxy_kernel_parameters(&params).is_ok());
        std::fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn wrong_value_fails_without_modification() {
        let (root, params) = fixtures();
        std::fs::write(&params[2].0, "0\n").unwrap();
        assert!(check_tproxy_kernel_parameters(&params).is_err());
        assert_eq!(std::fs::read_to_string(&params[2].0).unwrap(), "0\n");
        std::fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn missing_parameter_fails() {
        let (root, params) = fixtures();
        std::fs::remove_file(&params[1].0).unwrap();
        assert!(check_tproxy_kernel_parameters(&params).is_err());
        assert!(!PathBuf::from(&params[1].0).exists());
        std::fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn symlink_swap_fails_without_touching_target() {
        use std::os::unix::fs::symlink;
        let (root, params) = fixtures();
        let outside = root.join("target");
        std::fs::write(&outside, "2\n").unwrap();
        std::fs::remove_file(&params[0].0).unwrap();
        symlink(&outside, &params[0].0).unwrap();
        assert!(check_tproxy_kernel_parameters(&params).is_err());
        assert_eq!(std::fs::read_to_string(&outside).unwrap(), "2\n");
        std::fs::remove_dir_all(root).unwrap();
    }
}
'''


class TproxySourceTests(unittest.TestCase):
    def test_strict_patch_and_idempotent_refusal(self):
        source = "header\n" + patch.ORIGINAL + patch.ANCHOR + "\nfooter\n"
        result = patch.patch_source(source)
        self.assertIn("check_tproxy_kernel_parameters(&parameters)", result)
        self.assertIn("ensure_tproxy_kernel_parameters()?;", result)
        self.assertNotIn('command_output("sysctl"', result)
        with self.assertRaises(ValueError):
            patch.patch_source(result)

    def test_mismatched_or_duplicate_pinned_anchor_rejected(self):
        with self.assertRaises(ValueError):
            patch.patch_source("no matching source")
        with self.assertRaises(ValueError):
            patch.patch_source(patch.ORIGINAL * 2 + patch.ANCHOR)
        with self.assertRaises(ValueError):
            patch.patch_source(patch.ORIGINAL + patch.ANCHOR * 2)

    def test_real_source_anchors_match_readonly_when_explicit(self):
        source_path = os.environ.get("CSQTT_DERIVED_PROXY_SOURCE")
        if not source_path:
            self.skipTest("requires explicit read-only derived Rust source path")
        source = Path(source_path)
        self.assertEqual(source.name, "proxy_route.rs")
        original = source.read_text(encoding="utf8")
        patched = patch.patch_source(original)
        self.assertIn("let _guard = POLICY_LOCK.lock().await;", patched)
        self.assertIn("cleanup_legacy_proxy_policy().await;", patched)
        self.assertIn("add_tproxy_shared_rules().await?;", patched)
        self.assertIn("add_tproxy_rules(port).await?;", patched)
        self.assertIn("ACTIVE_RUNTIME.store(runtime_id, Ordering::Release);", patched)
        self.assertEqual(source.read_text(encoding="utf8"), original)

    def test_compiled_rust_fake_sysctl_readiness(self):
        rustc = os.environ.get("CSQTT_TEST_RUSTC", "rustc")
        try:
            subprocess.run([rustc, "--version"], check=True, capture_output=True,
                           text=True, timeout=8)
        except (FileNotFoundError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
            self.skipTest("host rustc unavailable")
        with tempfile.TemporaryDirectory(prefix="csqtt-tproxy-rust-") as temp:
            src = Path(temp) / "test_tproxy.rs"
            exe = Path(temp) / "test_tproxy"
            src.write_text(patch.CHECKER + RUST_CASES)
            subprocess.run([rustc, "--edition=2021", "--test", str(src), "-o",
                            str(exe)], check=True, capture_output=True,
                           text=True, timeout=35)
            result = subprocess.run([str(exe), "--test-threads=1"],
                                    capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("4 passed", result.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
