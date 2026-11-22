#!/usr/bin/env python3
"""Static-only Core8 shutdown Result-chain contract and negative tests.

Checks the behavior relevant to systemd Restart=on-failure without running
a daemon or touching the kernel. Optionally inspect an actual derived Rust
source directory supplied by --source-root.
"""
import argparse
from pathlib import Path

DATAPLANE_ERROR = '''Err(anyhow!(
                    "dataplane worker did not stop within the bounded 6s grace; external recovery required"
                ))'''
PROTOCOL_PROPAGATE = '''        if let Some(runtime) = self.dataplane.take() {
            runtime.shutdown()?;
        }'''
MAIN_PROPAGATE = '''    if let Err(error) = protocol_runtime.shutdown().await
        && terminal_error.is_none()
    {
        terminal_error = Some(error);
    }'''
MAIN_RETURN = '''    match terminal_error {
        Some(error) => Err(error),
        None => Ok(()),
    }'''
RESULT_MATCH = '''        match (signal_result, join_result) {
            (_, Err(error)) => Err(error),
            (Err(error), Ok(())) => Err(error),
            (Ok(()), Ok(())) => Ok(()),
        }'''


def validate(dataplane: str, protocol: str, main: str):
    for name, text, needle in (
        ("dataplane",dataplane,DATAPLANE_ERROR),
        ("dataplane_result",dataplane,RESULT_MATCH),
        ("protocol",protocol,PROTOCOL_PROPAGATE),
        ("main_capture",main,MAIN_PROPAGATE),
        ("main_return",main,MAIN_RETURN),
    ):
        if text.count(needle)!=1:
            raise ValueError("source contract not uniquely present: "+name)
    return True


def rejects(*args):
    try:
        validate(*args)
    except ValueError:
        return True
    raise AssertionError("contract unexpectedly accepted")


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--source-root",type=Path)
    opts=parser.parse_args()
    dp="prefix\n"+DATAPLANE_ERROR+"\n"+RESULT_MATCH+"\n"
    pr="prefix\n"+PROTOCOL_PROPAGATE+"\n"
    ma="prefix\n"+MAIN_PROPAGATE+"\n"+MAIN_RETURN+"\n"
    assert validate(dp,pr,ma)
    print("OFFLINE_EXIT_ERROR_CHAIN_COMPLETE_PASS")
    assert rejects(dp.replace(DATAPLANE_ERROR,"Ok(())"),pr,ma)
    print("OFFLINE_TIMEOUT_FALSE_SUCCESS_REJECT_PASS")
    assert rejects(dp+DATAPLANE_ERROR,pr,ma)
    print("OFFLINE_DUPLICATE_ERROR_REJECT_PASS")
    assert rejects(dp.replace(RESULT_MATCH,"Ok(())"),pr,ma)
    print("OFFLINE_RESULT_SWALLOW_REJECT_PASS")
    assert rejects(dp,pr.replace(PROTOCOL_PROPAGATE,"runtime.shutdown().ok();"),ma)
    print("OFFLINE_PROTOCOL_ERROR_SWALLOW_REJECT_PASS")
    assert rejects(dp,pr,ma.replace(MAIN_PROPAGATE,"terminal_error=None;"))
    print("OFFLINE_MAIN_ERROR_DROP_REJECT_PASS")
    assert rejects(dp,pr,ma.replace(MAIN_RETURN,"Ok(())"))
    print("OFFLINE_MAIN_FALSE_SUCCESS_REJECT_PASS")
    if opts.source_root:
        root=opts.source_root
        assert validate((root/"dataplane.rs").read_text(),
                        (root/"protocol.rs").read_text(),
                        (root/"main.rs").read_text())
        print("REAL_DERIVED_SOURCE_ERROR_PROPAGATION_CHAIN_PRESENT")
    print("CORE8_DATAPLANE_TIMEOUT_ERROR_CHAIN_OFFLINE_7_OF_7_PASS")


if __name__=="__main__":
    main()
