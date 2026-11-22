#!/usr/bin/env python3
"""Execute actual Rust Core8 model IP functions in a std-only native test fixture.

Functions are extracted byte-for-byte from patched model.rs. Exact real
tun_device.rs route_index / parse_ipv4 are compiled beside minimal Database
mock; no live database or kernel sysctls.
"""
import argparse
from pathlib import Path
import subprocess
import tempfile
import re

def extract(s: str, name: str) -> str:
    pat=r"pub fn "+re.escape(name)+r"\("
    m=re.search(pat,s)
    if not m or len(re.findall(pat,s))!=1:
        raise RuntimeError("function absent or ambiguous: "+name)
    start=m.start()
    first=s.index("{",m.end())
    depth=0
    for i in range(first,len(s)):
        if s[i]=="{":depth+=1
        if s[i]=="}":
            depth-=1
            if depth==0: return s[start:i+1]
    raise RuntimeError("unbalanced: "+name)

STUB=r'''
pub mod net_setup { pub const TUN_ADDR: &str = "10.67.69.1"; }
pub mod packet {
    pub const TUN_SUBNET_V6_PREFIX: [u8;15]=[0xfd,0x66,0,0x67,0,0,0,0,0,0,0,0,0,0,0];
    pub fn extract_dst_ipv4(p:&[u8])->Option<[u8;4]> {
        if p.len()<20 || p[0]>>4 != 4{return None;}
        let h=(p[0]&15) as usize*4;
        if h<20||p.len()<h{return None;}
        Some([p[16],p[17],p[18],p[19]])
    }
    pub fn extract_dst_ipv6_slot(p:&[u8])->Option<usize>{
        if p.len()<40||p[0]>>4!=6{return None;}
        if p[24..39]==TUN_SUBNET_V6_PREFIX{Some(p[39] as usize)}else{None}
    }
}
'''
TESTS=r'''
#[cfg(test)]
mod address_tests {
    use super::*;
    fn entry(ip: &str)->PasswordEntry{PasswordEntry{ip:ip.to_owned()}}
    #[test]
    fn valid_existing_address_kept() {
        let db=Database::default();
        assert_eq!(import_ip(&db,"10.67.69.40"),"10.67.69.40");
    }
    #[test]
    fn reject_foreign_and_reserved() {
        let db=Database::default();
        for bad in ["10.66.67.40","10.67.69.1","10.67.69.2",
                    "10.67.69.251","10.67.68.10","","garbage","fd00::3"]{
            assert!(!ip_in_pool(bad),"{bad}");
            assert_eq!(import_ip(&db,bad),"10.67.69.3");
        }
    }
    #[test]
    fn no_duplicate_imports(){
        let mut db=Database::default();
        db.passwords.insert("a".into(),entry("10.67.69.3"));
        assert_eq!(import_ip(&db,"10.67.69.3"),"10.67.69.4");
        db.passwords.insert("b".into(),entry("10.67.69.4"));
        assert_eq!(import_ip(&db,"10.67.69.4"),"10.67.69.5");
    }
    #[test]
    fn owner_ip_is_separate(){
        let db=Database{main_password:"owner".into(),..Default::default()};
        assert_eq!(resolve_session_ip(&db,"owner"),Some("10.67.69.2".into()));
        assert!(!ip_in_pool("10.67.69.2"));
    }
    #[test]
    fn no_invalid_session_address(){
        let mut db=Database::default();
        db.passwords.insert("foreign".into(),entry("10.66.67.5"));
        assert_eq!(resolve_session_ip(&db,"foreign"),None);
        db.passwords.insert("valid".into(),entry("10.67.69.6"));
        assert_eq!(resolve_session_ip(&db,"valid"),Some("10.67.69.6".into()));
    }
    #[test]
    fn exhausted_pool_fails_without_duplication(){
        let mut db=Database::default();
        for i in 3..=250u8 {
            db.passwords.insert(i.to_string(),entry(&format!("10.67.69.{i}")));
        }
        assert_eq!(get_next_ip(&db),None);
        assert_eq!(import_ip(&db,"10.66.67.3"),"");
    }
}
'''
def main():
    ap=argparse.ArgumentParser()
    ap.add_argument("--source-root",required=True,type=Path)
    ap.add_argument("--rustc",required=True)
    args=ap.parse_args()
    root=args.source_root.resolve()
    model=(root/"model.rs").read_text(encoding="utf8")
    tun=(root/"tun_device.rs").resolve()
    for name in ["ip_in_pool","import_ip","get_next_ip","resolve_session_ip"]:
        if model.count("pub fn "+name+"(")!=1:
            raise RuntimeError("model source missing exact "+name)
    anchor='pub const OWNER_TUNNEL_IP: &str = "10.67.69.2";'
    if model.count(anchor)!=1:raise RuntimeError("wrong owner IP")
    body="\n".join(extract(model,fun) for fun in (
        "ip_in_pool","import_ip","get_next_ip","resolve_session_ip"))
    data=STUB+"\n#[path = "+repr(str(tun)).replace("'","\"")+"]\npub mod tun_device;\n"
    data+="""use std::collections::HashMap;
#[derive(Default)] pub struct PasswordEntry {pub ip: String}
#[derive(Default)] pub struct Database {pub main_password:String,pub passwords: HashMap<String,PasswordEntry>}
fn secret_eq(a: &str,b:&str)->bool{a==b}
"""+anchor+"\n"+body+"\n"+TESTS
    with tempfile.TemporaryDirectory(prefix="csqttr-253-model-rust-") as tmp:
        src=Path(tmp)/"main.rs"; src.write_text(data,encoding='utf8')
        out=Path(tmp)/"model-tests"
        compile=subprocess.run([args.rustc,"--edition=2024","--test","-Awarnings",str(src),"-o",str(out)],
                               capture_output=True,text=True,timeout=80)
        if compile.returncode: raise RuntimeError("compile: "+compile.stderr[-2500:])
        run=subprocess.run([str(out),"--test-threads=1"],capture_output=True,text=True,timeout=90)
        print(run.stdout[-5300:])
        if run.returncode:raise RuntimeError("native test failures: "+run.stderr[-1200:])
        for word in ["valid_existing_address_kept","reject_foreign_and_reserved",
                     "no_duplicate_imports","owner_ip_is_separate",
                     "no_invalid_session_address","exhausted_pool_fails_without_duplication"]:
            assert word+" ... ok" in run.stdout
        print("CSQTTR_253_SELECTIVE_ACTUAL_MODEL_RUST_TESTS_6_OF_6_PASS")

if __name__=="__main__":
    main()
