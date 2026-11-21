#!/usr/bin/env python3
"""Opt-in source-only TPROXY hardening, composed with KernelNetworking RAII.

No system commands: this program ONLY writes to an explicitly supplied
derived proxy_route.rs source file after strict pinned-source anchor checks.
"""
import argparse
from pathlib import Path

ORIGINAL = '''    let _ = command_output("sysctl", &["-w", "net.ipv4.ip_forward=1"]).await;
    let _ = command_output("sysctl", &["-w", "net.ipv4.conf.all.rp_filter=2"]).await;
    let _ = command_output("sysctl", &["-w", "net.ipv4.conf.default.rp_filter=2"]).await;
    let _ = command_output("sysctl", &["-w", "net.ipv4.conf.csqtt-core0.rp_filter=2"]).await;
'''

REPLACEMENT = '''    // The dataplane's KernelNetworking owner is the sole writer/restorer of
    // these parameters. Do not create a second untracked TPROXY sysctl owner.
    ensure_tproxy_kernel_parameters()?;
'''

# This helper is intentionally independent of /proc/sys. It can be compiled and
# unit-tested with temporary files without privileges, network or device access.
CHECKER = '''fn check_tproxy_kernel_parameters(parameters: &[(String, &str)]) -> std::io::Result<()> {
    for (path, expected) in parameters {
        let metadata = std::fs::symlink_metadata(path)?;
        if !metadata.file_type().is_file() {
            return Err(std::io::Error::other(format!(
                "TPROXY kernel parameter is not a regular file: {path}"
            )));
        }
        let actual = std::fs::read_to_string(path)?;
        if actual.trim() != *expected {
            return Err(std::io::Error::other(format!(
                "TPROXY kernel parameter mismatched: {path}"
            )));
        }
    }
    Ok(())
}

'''

READY = '''fn ensure_tproxy_kernel_parameters() -> Result<()> {
    let tun_iface = crate::tun_device::TUN_IFACE;
    let parameters = [
        ("/proc/sys/net/ipv4/ip_forward".to_owned(), "1"),
        ("/proc/sys/net/ipv4/conf/all/rp_filter".to_owned(), "2"),
        ("/proc/sys/net/ipv4/conf/default/rp_filter".to_owned(), "2"),
        (format!("/proc/sys/net/ipv4/conf/{tun_iface}/rp_filter"), "2"),
    ];
    check_tproxy_kernel_parameters(&parameters)
        .context("TPROXY requires KernelNetworking policy without writing sysctl")
}

'''

ANCHOR = "async fn activate_tproxy(runtime_id: u64, port: u16) -> Result<()> {"
def patch_source(source: str) -> str:
    if source.count(ORIGINAL) != 1 or source.count(ANCHOR) != 1:
        raise ValueError("TPROXY sysctl source anchors do not match pinned Core8")
    if "ensure_tproxy_kernel_parameters()" in source:
        raise ValueError("TPROXY sysctl readiness already present; refuse duplicate patch")
    return source.replace(ORIGINAL, REPLACEMENT, 1).replace(
        ANCHOR, CHECKER + READY + ANCHOR, 1
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--check-only", action="store_true")
    args = parser.parse_args()
    if not args.source.is_file() or args.source.name != "proxy_route.rs":
        parser.error("must target existing derived proxy_route.rs")
    current = args.source.read_text(encoding="utf-8")
    updated = patch_source(current)
    if not args.check_only:
        args.source.write_text(updated, encoding="utf-8")
    print("CORE8_TPROXY_SYSCTL_OWNER_" + ("READY" if args.check_only else "APPLIED"))


if __name__ == "__main__":
    main()
