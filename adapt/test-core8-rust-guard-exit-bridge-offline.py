#!/usr/bin/env python3
"""Verify compiled Core8 shutdown Result -> actual child exit -> guard recovery.

Entirely off-network: a temporary real Rust binary with the exact derived
DataplaneRuntime::shutdown() method, inert std::thread workers, and a fake
sysctl file tree under TemporaryDirectory. The ORIGINAL durable guard is
executed as a process. Never starts Core8 itself, systemd, SSH, QEMU or any
network listener, and never reads or writes /proc/sys.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import uuid

HERE = Path(__file__).resolve().parent
GUARD = HERE / "core8-private-durable-sysctl-guard.py"
COMPILED = HERE / "test-core8-dataplane-timeout-compiled-offline.py"
g_spec = importlib.util.spec_from_file_location("core8_guard_bridge", GUARD)
guard = importlib.util.module_from_spec(g_spec)
g_spec.loader.exec_module(guard)
c_spec = importlib.util.spec_from_file_location("core8_compiled_bridge", COMPILED)
compiled = importlib.util.module_from_spec(c_spec)
c_spec.loader.exec_module(compiled)

RUST_MAIN = r'''
}
fn main() {
    use std::path::PathBuf;
    let mut argv = std::env::args().skip(1);
    let root=PathBuf::from(argv.next().expect("fake sysctl fixture dir required"));
    let mode=argv.next().expect("safe worker case required");
    if mode!="success" && mode!="timeout" && mode!="worker-error" {
        std::process::exit(70);
    }
    // Only relative paths whose complete inventory is generated from the
    // Python guard's exact allowlisted fake-sysctl mapping are writable.
    for (relative, value) in SYSCTLS {
        std::fs::write(root.join(relative), format!("{}\n",value)).expect("write fake sysctl");
    }
    let thread=std::thread::spawn(move || -> Result<()> {
        match mode.as_str() {
            "success" => Ok(()),
            "worker-error" => Err("test worker error".to_string()),
            "timeout" => {
                std::thread::sleep(Duration::from_secs(9));
                Ok(())
            },
            _ => unreachable!(),
        }
    });
    let runtime: DataplaneRuntime<()> = DataplaneRuntime {
        handle: DataplaneHandle { signal_error:false, marker:PhantomData },
        join:Some(thread)
    };
    match runtime.shutdown() {
        Ok(()) => println!("ACTUAL_RUST_SHUTDOWN_OK"),
        Err(error) => {
            eprintln!("ACTUAL_RUST_SHUTDOWN_ERROR={}",error);
            std::process::exit(42);
        },
    }
}
'''


def assert_root(temp: Path) -> None:
    resolved=temp.resolve()
    permitted=Path(tempfile.gettempdir()).resolve()
    if permitted not in resolved.parents or resolved == permitted:
        raise RuntimeError("fake fixture root must be strictly under temporary directory")


def make_case(root: Path):
    root.mkdir(mode=0o700)
    fake=root/"fake-sysctl"
    fake.mkdir(mode=0o700)
    original={}
    opposite={"1":"0","2":"1","0":"1","fq":"fq_codel",
              "bbr":"cubic","15":"3","3":"1"}
    for rel, wanted in guard.PARAMETERS.items():
        target=fake/rel
        target.parent.mkdir(parents=True,exist_ok=True)
        prior=opposite[wanted]
        target.write_text(prior+"\n",encoding="ascii")
        original[rel]=prior
    (root/"boot-id").write_text(str(uuid.uuid4()),encoding="ascii")
    return fake,original


def compile_fixture(path: Path, method: str, rustc: str):
    items="".join("    ("+json.dumps(key)+", "+json.dumps(val)+"),\n"
                  for key,val in guard.PARAMETERS.items())
    rust=(
       'const SYSCTLS: &[(&str,&str)] = &[\n'+items+'];\n'
       + compiled.RUST_TEMPLATE_HEAD
       + method + RUST_MAIN
    )
    source=path/"bridge.rs"
    binary=path/"compiled-rust-bridge"
    source.write_text(rust,encoding="utf8")
    result=subprocess.run([rustc,"--edition=2021","-O",str(source),"-o",str(binary)],
                          capture_output=True,text=True,timeout=90)
    if result.returncode:
        raise RuntimeError("Rust compilation failed: "+result.stderr[-2500:])
    return binary


def trial(name: str, binary: Path, temp: Path):
    case=temp/name
    fake,original=make_case(case)
    args=[sys.executable,"-B",str(GUARD),
          "--state-dir",str(case/"state"),"--sysctl-root",str(fake),
          "--boot-id-file",str(case/"boot-id"),"run","--",
          str(binary),str(fake),name]
    run=subprocess.run(args,text=True,capture_output=True,timeout=24)
    changed={(k):(fake/k).read_text(encoding="ascii").strip()
             for k in guard.PARAMETERS}
    if changed != original:
        differences=[k for k in guard.PARAMETERS if changed[k]!=original[k]]
        raise RuntimeError("guard did not restore fake sysctl: "+repr(differences))
    if (case/"state"/guard.JOURNAL).exists():
        raise RuntimeError("pending guard journal remains")
    if "CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED" not in run.stdout:
        raise RuntimeError("guard did not confirm restoration: "+run.stdout+run.stderr)
    if name == "success":
        if run.returncode!=0 or "ACTUAL_RUST_SHUTDOWN_OK" not in run.stdout:
            raise RuntimeError("Rust success did not reach zero status: "+run.stdout+run.stderr)
    elif name=="worker-error":
        if run.returncode!=42 or "ACTUAL_RUST_SHUTDOWN_ERROR=test worker error" not in run.stderr:
            raise RuntimeError("Rust worker error not propagated to guard: "+run.stdout+run.stderr)
    elif name=="timeout":
        if run.returncode!=42 or "external recovery required" not in run.stderr:
            raise RuntimeError("Rust bounded timeout not propagated to guard: "+run.stdout+run.stderr)
    print(f"CASE_{name.upper().replace('-','_')}=PASS CHILD_EXIT={run.returncode}"
          " GUARD_RESTORED_ALL=True JOURNAL_CLEARED=True",flush=True)


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--source-file",type=Path)
    parser.add_argument("--rustc",default=shutil.which("rustc"))
    args=parser.parse_args()
    if not args.rustc:
        raise RuntimeError("Rust compiler needed for offline fixture")
    method=(compiled.method_from_source(args.source_file) if args.source_file
            else compiled.synthetic_method())
    if method.count("external recovery required")!=1:
        raise RuntimeError("Rust method is not the hardened timeout variant")
    with tempfile.TemporaryDirectory(prefix="csqtt-core8-guard-rust-bridge-") as tmp:
        root=Path(tmp)
        assert_root(root)
        binary=compile_fixture(root,method,args.rustc)
        for case in ("success","worker-error","timeout"):
            trial(case,binary,root)
    print("CORE8_COMPILED_RUST_TO_GUARD_EXIT_BRIDGE_3_OF_3_PASS")
    print("SOURCE_MODE="+("exact-derived-Rust-method" if args.source_file
                          else "verified-repository-anchors"))


if __name__=="__main__":
    main()
