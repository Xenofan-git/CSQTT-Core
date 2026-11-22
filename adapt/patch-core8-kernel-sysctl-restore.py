#!/usr/bin/env python3
"""Opt-in Core8 kernel sysctl lifecycle patch for a *derived* Rust source tree.

This does not edit pinned upstream Rust and is NOT installed into the validated
release-candidate binary by default. Apply only after isolated review/testing.
"""
import argparse
from pathlib import Path

ORIGINAL_SET = '''fn set_kernel_parameter(path: &str, value: &str) {
    if let Err(error) = std::fs::write(path, value) {
        eprintln!("[NET] {path} = {value} failed: {error}");
    }
}
'''

REPLACEMENT_SET = '''// Record only kernel parameters this instance actually changes.
struct SavedKernelParameter {
    path: String,
    previous: String,
    expected: String,
}

fn kernel_parameter_is_regular(path: &str) -> bool {
    std::fs::symlink_metadata(path)
        .map(|metadata| metadata.file_type().is_file())
        .unwrap_or(false)
}

fn set_kernel_parameter(path: &str, value: &str) -> Option<SavedKernelParameter> {
    if !kernel_parameter_is_regular(path) {
        eprintln!("[NET] refusing non-regular or missing sysctl {path}");
        return None;
    }
    let previous = match std::fs::read_to_string(path) {
        Ok(content) => content.trim().to_string(),
        Err(error) => {
            eprintln!("[NET] cannot snapshot {path}: {error}");
            return None;
        }
    };
    if previous == value {
        return None; // Never restore a value this process did not change.
    }
    if let Err(error) = std::fs::write(path, value) {
        eprintln!("[NET] {path} = {value} failed: {error}");
        return None;
    }
    Some(SavedKernelParameter {
        path: path.to_string(),
        previous,
        expected: value.to_string(),
    })
}

fn restore_kernel_parameter(saved: &SavedKernelParameter) {
    if !kernel_parameter_is_regular(&saved.path) {
        eprintln!("[NET] restore skipped for missing/non-regular {}", saved.path);
        return;
    }
    match std::fs::read_to_string(&saved.path) {
        Ok(current) if current.trim() == saved.expected => {
            if let Err(error) = std::fs::write(&saved.path, &saved.previous) {
                eprintln!("[NET] restore {} failed: {error}", saved.path);
            }
        }
        Ok(current) => {
            eprintln!(
                "[NET] restore skipped for externally modified {} (current={})",
                saved.path, current.trim()
            );
        }
        Err(error) => eprintln!("[NET] read before restore {} failed: {error}", saved.path),
    }
}
'''

ORIGINAL_OWNER = '''struct KernelNetworking {
    rules: Vec<FirewallRule>,
}

impl KernelNetworking {
    fn install(tun_iface: &str) -> Self {
        let _ = std::process::Command::new("modprobe")
            .arg("tcp_bbr")
            .output();
        for (path, value) in KERNEL_PARAMETERS {
            set_kernel_parameter(path, value);
        }
        set_kernel_parameter(
            &format!("/proc/sys/net/ipv4/conf/{tun_iface}/rp_filter"),
            "2",
        );
        purge_legacy_mss_clamp();
        let rules = managed_firewall_rules(tun_iface);
        for rule in &rules {
            if let Err(error) = rule.install() {
                eprintln!("[NET] {error:#}");
            }
        }
        Self { rules }
    }
}

impl Drop for KernelNetworking {
    fn drop(&mut self) {
        for rule in &self.rules {
            rule.remove();
        }
    }
}
'''

REPLACEMENT_OWNER = '''struct KernelNetworking {
    rules: Vec<FirewallRule>,
    changed_parameters: Vec<SavedKernelParameter>,
}

impl KernelNetworking {
    fn install(tun_iface: &str) -> Self {
        let _ = std::process::Command::new("modprobe")
            .arg("tcp_bbr")
            .output();
        let mut changed_parameters = Vec::new();
        for (path, value) in KERNEL_PARAMETERS {
            if let Some(saved) = set_kernel_parameter(path, value) {
                changed_parameters.push(saved);
            }
        }
        if let Some(saved) = set_kernel_parameter(
            &format!("/proc/sys/net/ipv4/conf/{tun_iface}/rp_filter"),
            "2",
        ) {
            changed_parameters.push(saved);
        }
        purge_legacy_mss_clamp();
        let rules = managed_firewall_rules(tun_iface);
        for rule in &rules {
            if let Err(error) = rule.install() {
                eprintln!("[NET] {error:#}");
            }
        }
        Self { rules, changed_parameters }
    }
}

impl Drop for KernelNetworking {
    fn drop(&mut self) {
        for rule in &self.rules {
            rule.remove();
        }
        for saved in self.changed_parameters.iter().rev() {
            restore_kernel_parameter(saved);
        }
    }
}
'''


def patch_source(text: str) -> str:
    if text.count(ORIGINAL_SET) != 1 or text.count(ORIGINAL_OWNER) != 1:
        raise ValueError("Core8 pinned source does not match sysctl lifecycle anchors")
    if "restore_kernel_parameter(" in text:
        raise ValueError("Kernel restore already present; patch refuses duplicate")
    return text.replace(ORIGINAL_SET, REPLACEMENT_SET, 1).replace(
        ORIGINAL_OWNER, REPLACEMENT_OWNER, 1
    )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--check-only", action="store_true")
    args = parser.parse_args()
    if args.source.name != "tokio_io.rs" or not args.source.is_file():
        parser.error("expected explicit existing derived tokio_io.rs source file")
    original = args.source.read_text(encoding="utf-8")
    updated = patch_source(original)
    if not args.check_only:
        args.source.write_text(updated, encoding="utf-8")
    print("CORE8_KERNEL_SYSCTL_RESTORE_PATCH_" + ("READY" if args.check_only else "APPLIED"))


if __name__ == "__main__":
    main()
