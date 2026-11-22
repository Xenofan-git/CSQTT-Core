#!/usr/bin/env python3
"""Opt-in Core8 derived-source patch: do not silently report unjoined dataplane as success.

Only apply to an explicit, separately owned, disposable Rust source copy
after the prior 6-second join-budget patch. No routing or sysctl writes.
"""
import argparse
from pathlib import Path

ANCHOR = '''            } else {
                eprintln!("[DATAPLANE] shutdown timed out (6s bounded grace)");
                Ok(())
            }
'''
REPLACEMENT = '''            } else {
                eprintln!("[DATAPLANE] shutdown timed out (6s bounded grace)");
                Err(anyhow!(
                    "dataplane worker did not stop within the bounded 6s grace; external recovery required"
                ))
            }
'''
OWNER = '''impl<C: Send + 'static> DataplaneRuntime<C> {'''
EXPLICIT_RESULT = '''        match (signal_result, join_result) {
            (_, Err(error)) => Err(error),
            (Err(error), Ok(())) => Err(error),
            (Ok(()), Ok(())) => Ok(()),
        }
'''
def patch(source: str) -> str:
    if source.count(OWNER) != 1:
        raise ValueError("expected a unique DataplaneRuntime owner")
    if source.count(EXPLICIT_RESULT) != 1:
        raise ValueError("missing exact Result-propagation match")
    if source.count(ANCHOR) != 1 or REPLACEMENT in source:
        raise ValueError("expected unique unmodified 6s grace timeout branch")
    # The opt-in Rust RAII kernel sysctl restore must remain a separate
    # concern. Returning Err only changes reporting to caller/systemd.
    return source.replace(ANCHOR, REPLACEMENT, 1)

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--check-only", action="store_true")
    args = parser.parse_args()
    old = args.source.read_text(encoding="utf8")
    updated = patch(old)
    if not args.check_only:
        args.source.write_text(updated, encoding="utf8")
    print("CORE8_DATAPLANE_TIMEOUT_NONZERO_" +
          ("SOURCE_ANCHOR_CHECK_PASS" if args.check_only else "PATCH_APPLIED"))

if __name__ == "__main__":
    main()
