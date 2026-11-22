#!/usr/bin/env python3
"""Compile BOTH experimental ownership helpers into one fake-file Rust harness.

No host /proc, network, guest, firewall or real Core8 binary accessed.
"""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).parent
def load(filename, module_name):
    spec = importlib.util.spec_from_file_location(module_name, ROOT / filename)
    obj = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(obj)
    return obj

kernel = load("patch-core8-kernel-sysctl-restore.py", "kernel_owner")
tproxy = load("patch-core8-tproxy-sysctl-ownership.py", "tproxy_owner")

STUBS = r'''
struct FirewallRule;
impl FirewallRule {
    fn install(&self) -> Result<(), std::io::Error> { Ok(()) }
    fn remove(&self) {}
}
const KERNEL_PARAMETERS: &[(&str, &str)] = &[];
fn purge_legacy_mss_clamp() {}
fn managed_firewall_rules(_: &str) -> Vec<FirewallRule> { Vec::new() }
'''

TESTS = r'''
#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;
    use std::time::{SystemTime, UNIX_EPOCH};

    struct Fixture {
        root: PathBuf,
        params: Vec<(String, &'static str)>,
    }

    impl Fixture {
        fn new() -> Self {
            let n = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
            let root = std::env::temp_dir().join(format!("csqtt-ownership-chain-{}-{n}", std::process::id()));
            std::fs::create_dir(&root).unwrap();
            let mut params = Vec::new();
            for (i, (old, expected)) in [
                ("0","1"), ("1","2"), ("1","2"), ("1","2")
            ].into_iter().enumerate() {
                let path = root.join(format!("sysctl{i}"));
                std::fs::write(&path, format!("{old}\n")).unwrap();
                params.push((path.to_str().unwrap().to_string(), expected));
            }
            Self { root, params }
        }

        fn apply(&self, count: usize) -> KernelNetworking {
            let mut changed_parameters = Vec::new();
            for (path, desired) in self.params.iter().take(count) {
                let saved = set_kernel_parameter(path, desired).expect("expect sysctl change");
                changed_parameters.push(saved);
            }
            KernelNetworking { rules: Vec::new(), changed_parameters }
        }

        fn get(&self, i: usize) -> String {
            std::fs::read_to_string(&self.params[i].0).unwrap().trim().to_owned()
        }
    }

    impl Drop for Fixture {
        fn drop(&mut self) {
            std::fs::remove_dir_all(&self.root).unwrap();
        }
    }

    #[test]
    fn two_components_shared_normal_lifecycle() {
        let fixture = Fixture::new();
        let owner = fixture.apply(4);
        assert!(check_tproxy_kernel_parameters(&fixture.params).is_ok());
        assert_eq!(fixture.get(0), "1");
        drop(owner);
        assert_eq!(fixture.get(0), "0");
        assert_eq!(fixture.get(1), "1");
        assert!(check_tproxy_kernel_parameters(&fixture.params).is_err());
    }

    #[test]
    fn unready_kernel_prevents_tproxy_policy_activation() {
        let fixture = Fixture::new();
        let owner = fixture.apply(2);
        assert!(check_tproxy_kernel_parameters(&fixture.params).is_err());
        drop(owner);
        assert_eq!(fixture.get(0), "0");
        assert_eq!(fixture.get(1), "1");
    }

    #[test]
    fn external_mutation_during_lifetime_is_preserved() {
        let fixture = Fixture::new();
        let owner = fixture.apply(4);
        assert!(check_tproxy_kernel_parameters(&fixture.params).is_ok());
        std::fs::write(&fixture.params[1].0, "3\n").unwrap();
        assert!(check_tproxy_kernel_parameters(&fixture.params).is_err());
        drop(owner);
        assert_eq!(fixture.get(0), "0");
        assert_eq!(fixture.get(1), "3");
        assert_eq!(fixture.get(3), "1");
    }

    #[test]
    fn no_owner_write_means_no_rollback() {
        let fixture = Fixture::new();
        for (path, expected) in &fixture.params {
            std::fs::write(path, expected).unwrap();
            assert!(set_kernel_parameter(path, expected).is_none());
        }
        assert!(check_tproxy_kernel_parameters(&fixture.params).is_ok());
        // No owner exists, so external changes must not be restored.
        std::fs::write(&fixture.params[0].0, "0\n").unwrap();
        assert_eq!(fixture.get(0), "0");
    }
}
'''

class IntegrationTests(unittest.TestCase):
    def test_both_patches_have_exact_original_anchors(self):
        a = kernel.ORIGINAL_SET + kernel.ORIGINAL_OWNER
        b = tproxy.ORIGINAL + tproxy.ANCHOR
        self.assertIn("changed_parameters", kernel.patch_source(a))
        self.assertIn("ensure_tproxy_kernel_parameters", tproxy.patch_source(b))
        self.assertNotIn('command_output("sysctl"', tproxy.patch_source(b))

    def test_compiled_cross_component_lifetimes(self):
        rustc = os.environ.get("CSQTT_TEST_RUSTC", "rustc")
        try:
            subprocess.run([rustc, "--version"], timeout=8, check=True,
                           capture_output=True, text=True)
        except (FileNotFoundError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
            self.skipTest("rustc unavailable")
        with tempfile.TemporaryDirectory(prefix="csqtt-cross-owner-") as base:
            src = Path(base) / "test_cross.rs"
            binary = Path(base) / "test_cross"
            src.write_text(kernel.REPLACEMENT_SET + STUBS + kernel.REPLACEMENT_OWNER
                           + tproxy.CHECKER + TESTS, encoding="utf8")
            result = subprocess.run([rustc, "--edition=2021", "--test", str(src),
                                     "-o", str(binary)], check=True,
                                    capture_output=True, text=True, timeout=35)
            test = subprocess.run([str(binary), "--test-threads=1"], timeout=15,
                                  capture_output=True, text=True)
            self.assertEqual(test.returncode, 0, test.stdout + test.stderr)
            self.assertIn("4 passed", test.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
