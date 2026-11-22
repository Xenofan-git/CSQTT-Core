#!/usr/bin/env python3
"""Compile actual CSQTT Core tun_device.rs tests with only tiny packet stubs.

This is an offline native Rust test; does not open TUN, execute Core8 daemon,
change sysctls or touch the default route. Exact source file supplied by
--source-root, not an independently reimplemented scheduler.
"""
import argparse
from pathlib import Path
import subprocess
import tempfile

PACKET_STUB = r'''
pub mod net_setup { pub const TUN_ADDR: &str = "10.67.69.1"; }
pub mod packet {
    pub const TUN_SUBNET_V6_PREFIX: [u8;15] =
        [0xfd,0x66,0,0x67,0,0,0,0,0,0,0,0,0,0,0];
    pub fn extract_dst_ipv4(packet: &[u8]) -> Option<[u8;4]> {
        if packet.len()<20 || packet[0]>>4!=4 { return None; }
        let ihl = (packet[0] & 0x0f) as usize * 4;
        if ihl<20 || packet.len()<ihl { return None; }
        let total=u16::from_be_bytes([packet[2],packet[3]]) as usize;
        if total<ihl { return None; }
        Some([packet[16],packet[17],packet[18],packet[19]])
    }
    pub fn extract_dst_ipv6_slot(packet: &[u8]) -> Option<usize> {
        if packet.len()<40 || packet[0]>>4!=6 {return None;}
        let dest=&packet[24..40];
        if dest[..15]==TUN_SUBNET_V6_PREFIX {Some(dest[15] as usize)}else{None}
    }
}
'''

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument("--source-root",type=Path,required=True)
    ap.add_argument("--rustc",required=True)
    args=ap.parse_args()
    src=(args.source_root/"tun_device.rs").resolve()
    net=(args.source_root/"net_setup.rs").resolve()
    text=src.read_text(encoding="utf8")
    assert 'const SUBNET_PREFIX: [u8; 3] = [10, 67, 69];' in text
    assert 'const PIN_WINDOW:' in text
    assert 'downlink_bulk_udp_with_small_packets_is_not_pinned' in text
    assert 'refill_accumulates_intervals_under_100_microseconds' in text
    assert 'pub const TUN_ADDR: &str = "10.67.69.1";' in net.read_text()
    with tempfile.TemporaryDirectory(prefix="csqttr-253-tun-rust-") as tmp:
        root=Path(tmp)
        main_source=root/"main.rs"
        main_source.write_text(PACKET_STUB+"\n#[path="+repr(str(src)).replace("'","\"")+"]\npub mod tun_device;\n")
        exe=root/"all-tun-native-tests"
        compiler=subprocess.run([args.rustc,"--edition=2024","--test",
                                 "-Awarnings",str(main_source),"-o",str(exe)],
                                capture_output=True,text=True,timeout=75)
        if compiler.returncode:
            raise RuntimeError("Rust compile failed: "+compiler.stderr[-3500:])
        result=subprocess.run([str(exe),"--test-threads=1"],
                              capture_output=True,text=True,timeout=90)
        print(result.stdout[-6500:])
        if result.returncode:
            raise RuntimeError("Rust unit tests failed: "+result.stderr[-1700:])
        for required in ("dedicated_core8_subnet_matches_actual_tun_address",
                         "refill_accumulates_intervals_under_100_microseconds",
                         "downlink_bulk_udp_with_small_packets_is_not_pinned",
                         "downlink_game_udp_mixed_sizes_stay_pinned"):
            assert required+" ... ok" in result.stdout, required
        print("CSQTTR_253_SELECTIVE_REAL_TUN_RUST_UNIT_TESTS_PASS")

if __name__=="__main__":
    main()
