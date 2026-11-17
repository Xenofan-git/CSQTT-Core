#!/usr/bin/env python3
"""Opt-in stage timings for pinned CSQTT v2.5.0 startup policy cleanup.

This only adds gated diagnostics to the derived Rust source. It deliberately
does not change command timeouts, rules, ordering, or cleanup error semantics.
"""
from pathlib import Path

ORIGINAL = """    pub async fn cleanup_orphaned_policy() -> Result<()> {
        let _guard = POLICY_LOCK.lock().await;
        ACTIVE_RUNTIME.store(0, Ordering::Release);
        cleanup_shared_policy().await;
        cleanup_legacy_proxy_policy().await;
        verify_proxy_policy_clean().await
    }
"""

INSTRUMENTED = """    pub async fn cleanup_orphaned_policy() -> Result<()> {
        // Diagnostic mode is opt-in and never prints route secrets or keys.
        let diagnostic = std::env::var("CSQTT_CORE_DIAG_POLICY_CLEANUP")
            .ok()
            .as_deref()
            == Some("1");
        let started = Instant::now();
        if diagnostic {
            eprintln!("[PROXY-CLEANUP] stage=lock_wait elapsed_ms=0");
        }
        let _guard = POLICY_LOCK.lock().await;
        if diagnostic {
            eprintln!("[PROXY-CLEANUP] stage=lock_acquired elapsed_ms={}", started.elapsed().as_millis());
        }
        ACTIVE_RUNTIME.store(0, Ordering::Release);
        if diagnostic {
            eprintln!("[PROXY-CLEANUP] stage=shared_start elapsed_ms={}", started.elapsed().as_millis());
        }
        cleanup_shared_policy().await;
        if diagnostic {
            eprintln!("[PROXY-CLEANUP] stage=shared_done elapsed_ms={}", started.elapsed().as_millis());
            eprintln!("[PROXY-CLEANUP] stage=legacy_start elapsed_ms={}", started.elapsed().as_millis());
        }
        cleanup_legacy_proxy_policy().await;
        if diagnostic {
            eprintln!("[PROXY-CLEANUP] stage=legacy_done elapsed_ms={}", started.elapsed().as_millis());
            eprintln!("[PROXY-CLEANUP] stage=verify_start elapsed_ms={}", started.elapsed().as_millis());
        }
        let verification = verify_proxy_policy_clean().await;
        if diagnostic {
            let result = if verification.is_ok() { "ok" } else { "error" };
            eprintln!(
                "[PROXY-CLEANUP] stage=verify_done elapsed_ms={} result={}",
                started.elapsed().as_millis(),
                result
            );
        }
        verification
    }
"""

def apply(path: Path) -> None:
    source = path.read_text(encoding="utf-8")
    if source.count(ORIGINAL) != 1:
        raise ValueError("pinned cleanup function did not match exactly; refuse source mutation")
    path.write_text(source.replace(ORIGINAL, INSTRUMENTED, 1), encoding="utf-8")

if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    args = parser.parse_args()
    apply(args.source)
