#!/usr/bin/env python3
"""Compile and execute Core8's shutdown method with isolated Rust worker fixtures.

Runs only disposable std::thread workers; no Core8 daemon, no kernel sysctl,
no SSH, no network, no QEMU, no real subprocess signals. The optional
--source-file extracts the exact method from actual privately patched Rust.
Default reconstructs that method from repo-checked strict source anchors so
GitHub Actions can exercise the result even without derived Rust source.
"""
import argparse
import importlib.util
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("nonzero_patch", ROOT/"patch-core8-dataplane-timeout-nonzero.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

HEAD = '''    pub fn shutdown(mut self) -> Result<()> {
        let signal_result = self.handle.shutdown();
        let join_result = if let Some(join) = self.join.take() {
            let deadline = Instant::now() + Duration::from_secs(6);
            while !join.is_finished() && Instant::now() < deadline {
                std::thread::sleep(Duration::from_millis(10));
            }
            if join.is_finished() {
                join.join().map_err(|_| anyhow!("dataplane thread panicked"))?
            } else {
'''
TAIL = '''        } else {
            Ok(())
        };
'''
MATCH = '''        match (signal_result, join_result) {
            (_, Err(error)) => Err(error),
            (Err(error), Ok(())) => Err(error),
            (Ok(()), Ok(())) => Ok(()),
        }
    }
'''

# Keep the live source branch exact: replace only the timeout body.
TIMEOUT_BRANCH = '''                eprintln!("[DATAPLANE] shutdown timed out (6s bounded grace)");
                Err(anyhow!(
                    "dataplane worker did not stop within the bounded 6s grace; external recovery required"
                ))
            }
'''


def method_from_source(path: Path) -> str:
    source = path.read_text(encoding="utf8")
    needle = "    pub fn shutdown(mut self) -> Result<()> {"
    if source.count(needle) != 1 or source.count(mod.REPLACEMENT) != 1:
        raise ValueError("source missing exact compiled shutdown function or patch")
    index = source.index(needle)
    depth = 0
    inside = False
    for pos in range(index, len(source)):
        if source[pos] == "{":
            depth += 1
            inside = True
        elif source[pos] == "}":
            depth -= 1
            if inside and depth == 0:
                result = source[index:pos + 1]
                if result.count(mod.REPLACEMENT) != 1:
                    raise ValueError("patched timeout not in extracted method")
                return result
    raise ValueError("unbalanced shutdown function source")


def synthetic_method() -> str:
    result = HEAD + TIMEOUT_BRANCH + TAIL + MATCH
    if result.count(mod.REPLACEMENT) != 1:
        raise ValueError("checked branch differs from repo patch")
    return result


RUST_TEMPLATE_HEAD = r'''
use std::time::{Duration, Instant};
use std::thread;
use std::marker::PhantomData;

macro_rules! anyhow { ($($args:tt)*) => { format!($($args)*) }; }
type Result<T> = std::result::Result<T, String>;
struct DataplaneHandle<C> { signal_error: bool, marker: PhantomData<C> }
impl<C> DataplaneHandle<C> {
    fn shutdown(&self) -> Result<()> {
        if self.signal_error { Err("signal failed".to_owned()) } else { Ok(()) }
    }
}
struct DataplaneRuntime<C> {
    handle: DataplaneHandle<C>,
    join: Option<thread::JoinHandle<Result<()>>>,
}
impl<C: Send + 'static> DataplaneRuntime<C> {
'''
RUST_TEMPLATE_TAIL = r'''
}
fn trial(name: &str) -> bool {
    let work = match name {
        "timeout" => thread::spawn(|| {
            thread::sleep(Duration::from_secs(9));
            Ok(())
        }),
        "success" => thread::spawn(|| Ok(())),
        "worker-error" => thread::spawn(|| Err("worker failed".to_owned())),
        "worker-panic" => thread::spawn(|| panic!("inert worker panic")),
        "signal-error" => thread::spawn(|| Ok(())),
        _ => return false,
    };
    let runtime: DataplaneRuntime<()> = DataplaneRuntime {
        handle: DataplaneHandle {
            signal_error: name == "signal-error", marker: PhantomData,
        },
        join: Some(work),
    };
    let start = Instant::now();
    let result = runtime.shutdown();
    let elapsed = start.elapsed();
    let ok = match name {
        "success" => result.is_ok(),
        "timeout" => elapsed >= Duration::from_secs(5)
            && elapsed < Duration::from_secs(8)
            && result.as_ref().err().map(|s| s.contains("external recovery required")).unwrap_or(false),
        "worker-error" => result.as_ref().err().map(|s| s.contains("worker failed")).unwrap_or(false),
        "worker-panic" => result.as_ref().err().map(|s| s.contains("panicked")).unwrap_or(false),
        "signal-error" => result.as_ref().err().map(|s| s.contains("signal failed")).unwrap_or(false),
        _ => false,
    };
    println!("CASE={} RESULT={} ELAPSED_MS={}", name, if ok {"PASS"} else {"FAIL"}, elapsed.as_millis());
    ok
}
fn main() {
    let mut okay=true;
    for mode in ["success","worker-error","worker-panic","signal-error","timeout"] {
        if !trial(mode) { okay=false; }
    }
    if !okay { std::process::exit(2); }
    println!("CORE8_OFFLINE_COMPILED_SHUTDOWN_5_OF_5_PASS");
}
'''


def main():
    ap=argparse.ArgumentParser()
    ap.add_argument("--source-file",type=Path)
    ap.add_argument("--rustc",default=shutil.which("rustc"))
    args=ap.parse_args()
    if not args.rustc:
        raise RuntimeError("Rust compiler required for fixture")
    method=method_from_source(args.source_file) if args.source_file else synthetic_method()
    if "Duration::from_secs(6)" not in method or "external recovery required" not in method:
        raise ValueError("source does not contain required bounded timeout and failure")
    with tempfile.TemporaryDirectory(prefix="csqtt-rust-shutdown-offline-") as temp:
        path=Path(temp)
        src=path/"fixture.rs"
        out=path/"fixture"
        src.write_text(RUST_TEMPLATE_HEAD+method+RUST_TEMPLATE_TAIL,encoding="utf8")
        subprocess.run([args.rustc,"--edition=2021","-O",str(src),"-o",str(out)],
                       check=True,timeout=75,capture_output=True,text=True)
        test=subprocess.run([str(out)],check=True,timeout=45,capture_output=True,text=True)
        if test.stdout.count("RESULT=PASS")!=5 or "5_OF_5_PASS" not in test.stdout:
            raise RuntimeError("not all compiled fixture cases passed: "+test.stdout)
        print(test.stdout.strip())
        print("COMPILED_SOURCE="+("exact-actual-rust-method" if args.source_file else "repository-anchor-fixture"))


if __name__=="__main__":
    main()
