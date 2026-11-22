#!/usr/bin/env python3
"""No real sysctls, no networking, no Core8 process. Exact Rust source-contract tests."""
import importlib.util
from pathlib import Path

root=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location("timeout_patch",root/"patch-core8-dataplane-timeout-nonzero.py")
p=importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)

sample="header\n"+p.OWNER+"\n"+p.ANCHOR+p.EXPLICIT_RESULT+"footer\n"
after=p.patch(sample)
assert p.ANCHOR not in after
assert after.count(p.REPLACEMENT)==1
assert after.startswith("header\n"+p.OWNER)
assert after.endswith(p.EXPLICIT_RESULT+"footer\n")
assert 'Err(anyhow!(' in after and "external recovery required" in after
print("OFFLINE_TIMEOUT_TRANSFORM_TO_NONZERO_PASS")

def rejects(s):
    try: p.patch(s)
    except ValueError: return
    raise AssertionError("patch accepted incorrect source")
rejects(after)
print("OFFLINE_DOUBLE_APPLY_REJECT_PASS")
rejects(sample.replace(p.OWNER,"unknown owner"))
print("OFFLINE_OWNER_MISSING_REJECT_PASS")
rejects(sample+p.OWNER)
print("OFFLINE_OWNER_DUPLICATE_REJECT_PASS")
rejects(sample.replace(p.ANCHOR,"unsupported deadline branch"))
print("OFFLINE_TIMEOUT_BRANCH_MISSING_REJECT_PASS")
rejects(sample+p.ANCHOR)
print("OFFLINE_TIMEOUT_BRANCH_DUPLICATE_REJECT_PASS")
rejects(sample.replace(p.EXPLICIT_RESULT,"error silently lost"))
print("OFFLINE_RESULT_PROPAGATION_MISSING_REJECT_PASS")
rejects(sample+p.EXPLICIT_RESULT)
print("OFFLINE_RESULT_PROPAGATION_DUPLICATE_REJECT_PASS")
print("CORE8_DATAPLANE_TIMEOUT_NONZERO_OFFLINE_8_OF_8_PASS")
