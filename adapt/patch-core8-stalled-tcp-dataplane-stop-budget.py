#!/usr/bin/env python3
"""Opt-in, explicitly bounded Core8 dataplane shutdown grace window.

Never alter a release candidate or original source. This targets only a
separate disposable source copy. The external durable guard remains mandatory
until actual ARM64 stalled-TCP and crash recovery are independently proven.
"""
import argparse
from pathlib import Path

ORIGINAL = '''            let deadline = Instant::now() + Duration::from_secs(1);
            while !join.is_finished() && Instant::now() < deadline {
                std::thread::sleep(Duration::from_millis(10));
            }
            if join.is_finished() {
                join.join()
                    .map_err(|_| anyhow!("dataplane thread panicked"))?
            } else {
                eprintln!("[DATAPLANE] shutdown timed out");
                Ok(())
            }
'''
REPLACEMENT = '''            // A live transparent TCP session can delay reactor cleanup beyond
            // one second. Give the dataplane a bounded chance to drop its
            // owned KernelNetworking sysctl journal before main exits.
            // An external durable restore guard remains necessary on timeout.
            let deadline = Instant::now() + Duration::from_secs(6);
            while !join.is_finished() && Instant::now() < deadline {
                std::thread::sleep(Duration::from_millis(10));
            }
            if join.is_finished() {
                join.join()
                    .map_err(|_| anyhow!("dataplane thread panicked"))?
            } else {
                eprintln!("[DATAPLANE] shutdown timed out (6s bounded grace)");
                Ok(())
            }
'''
CONTEXT = '''impl<C: Send + 'static> DataplaneRuntime<C> {
'''

def patch(src: str) -> str:
    if src.count(CONTEXT) != 1:
        raise ValueError("expected unique dataplane runtime ownership context")
    if src.count(ORIGINAL) != 1:
        raise ValueError("expected exactly one original shutdown wait block")
    if src.count(REPLACEMENT):
        raise ValueError("already patched or duplicate")
    return src.replace(ORIGINAL, REPLACEMENT, 1)

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--source",required=True,type=Path)
    parser.add_argument("--check-only",action="store_true")
    args=parser.parse_args()
    content=args.source.read_text()
    updated=patch(content)
    if not args.check_only:
        args.source.write_text(updated)
    print("CORE8_DATAPLANE_BOUNDED_GRACE_PATCH_"+("CHECK_PASS" if args.check_only else "APPLIED"))
if __name__=="__main__": main()
