#!/usr/bin/env python3
"""Produce isolated CSQTT 2.5 Rust sources without changing upstream checkout."""
import argparse, hashlib, shutil
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--upstream-root',required=True,type=Path);p.add_argument('--output',required=True,type=Path);a=p.parse_args()
src=a.upstream_root/'rust-server'; dest=a.output/'rust-server'
assert (src/'Cargo.lock').is_file() and (src/'tun_device.rs').is_file()
if dest.exists(): shutil.rmtree(dest)
shutil.copytree(src,dest,ignore=shutil.ignore_patterns('target','.git'))
shutil.copytree(a.upstream_root/'shared',a.output/'shared',dirs_exist_ok=True)
assets=a.upstream_root/'app/src/main/assets'
asset_dest=a.output/'app/src/main/assets'
asset_dest.mkdir(parents=True,exist_ok=True)
for asset in assets.iterdir():
 if asset.is_file(): shutil.copy2(asset,asset_dest/asset.name)
changed=[]
for file in sorted(dest.rglob('*.rs')):
 s=file.read_text(); old=s
 # Apply only to CSQTT's own IPv4 tunnel address literals, never external networks.
 s=s.replace('10.66.67.', '10.67.69.').replace('Ipv4Addr::from([10, 66, 67,', 'Ipv4Addr::from([10, 67, 69,').replace('csqtt1','csqtt-core0')
 # Distinct TUN firewall comment for independent cleanup; avoid matching legacy rules.
 s=s.replace('CSQTT_TPROXY', 'CSQTT_CORE_TPROXY')
 if s!=old:
  file.write_text(s);changed.append((str(file.relative_to(dest)),hashlib.sha256(s.encode()).hexdigest()[:12]))
# Exercise SOCKS5 UDP ASSOCIATE against a loopback relay in the generated Core test suite.
fixture=Path(__file__).with_name('socks5-udp-loopback-test.rs.txt').read_text()
proxy=dest/'proxy_route.rs'
source=proxy.read_text()
marker='    #[test]\n    fn parses_socks5_udp_ipv4_response()'
assert source.count(marker)==1
proxy.write_text(source.replace(marker,fixture+'\n'+marker))
# Opt-in privileged ARM64 end-to-end TPROXY integration test.
tproxy=dest/'tproxy.rs'
source=tproxy.read_text()
marker='    #[test]\n    fn ports_are_unique_per_runtime_and_in_range()'
assert source.count(marker)==1
fixture=Path(__file__).with_name('tproxy-arm64-privileged-test.rs.txt').read_text()
tproxy.write_text(source.replace(marker,fixture+'\n'+marker))
# Opt-in ARM64 SOCKS5 outage/recovery test.
tproxy=dest/'tproxy.rs'
source=tproxy.read_text()
marker='    #[test]\n    fn ports_are_unique_per_runtime_and_in_range()'
assert source.count(marker)==1
fixture=Path(__file__).with_name('tproxy-arm64-recovery-test.rs.txt').read_text()
tproxy.write_text(source.replace(marker,fixture+'\n'+marker))
assert any(f=='tun_device.rs' for f,_ in changed)
assert any(f=='net_setup.rs' for f,_ in changed)
assert any(f=='model.rs' for f,_ in changed)
for f,_ in changed:
 s=(dest/f).read_text()
 assert '10.66.67.' not in s and 'csqtt1' not in s
print('Core Rust source generated:',dest,'modified',len(changed),'files')
for f,h in changed: print(f,h)
