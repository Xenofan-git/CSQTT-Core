#!/usr/bin/env python3
"""Bounded startup orphan-policy cleanup budget for derived CSQTT 2.5.0.

Pinned original: outer 1s timeout aborts a cleanup which calls ip/iptables
with an individual 2s timeout per command. Strict anchored replacement only.
No changes to policy operations, command timeouts, runtime defaults or routes.
"""
from pathlib import Path

OLD = """    match tokio::time::timeout(
        Duration::from_secs(1),
        proxy_route::ProxyRoute::cleanup_orphaned_policy(),
    )
    .await
"""
NEW = """    // The policy cleanup invokes multiple ip/iptables commands, each with its
    // own 2s timeout. An outer 1s deadline aborts valid cleanup in mid-flight.
    // Keep this bounded and preserve the existing warn-and-continue behavior.
    match tokio::time::timeout(
        Duration::from_secs(15),
        proxy_route::ProxyRoute::cleanup_orphaned_policy(),
    )
    .await
"""


def apply(path: Path) -> None:
    content = path.read_text(encoding="utf-8")
    if content.count(OLD) != 1:
        raise ValueError("pinned startup timeout block not found exactly once")
    path.write_text(content.replace(OLD, NEW, 1), encoding="utf-8")


if __name__ == "__main__":
    import argparse
    p = argparse.ArgumentParser()
    p.add_argument("--source", type=Path, required=True)
    args = p.parse_args()
    apply(args.source)
