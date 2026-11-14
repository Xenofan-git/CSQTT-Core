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
 s=s.replace('10.66.67.', '10.67.69.').replace('csqtt1','csqtt-core0')
 # Distinct TUN firewall comment for independent cleanup; avoid matching legacy rules.
 s=s.replace('CSQTT_TPROXY', 'CSQTT_CORE_TPROXY')
 if s!=old:
  file.write_text(s);changed.append((str(file.relative_to(dest)),hashlib.sha256(s.encode()).hexdigest()[:12]))
assert any(f=='tun_device.rs' for f,_ in changed)
assert any(f=='net_setup.rs' for f,_ in changed)
assert any(f=='model.rs' for f,_ in changed)
for f,_ in changed:
 s=(dest/f).read_text()
 assert '10.66.67.' not in s and 'csqtt1' not in s
print('Core Rust source generated:',dest,'modified',len(changed),'files')
for f,h in changed: print(f,h)
