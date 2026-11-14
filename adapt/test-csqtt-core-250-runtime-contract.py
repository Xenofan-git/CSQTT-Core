#!/usr/bin/env python3
"""Fail closed if original Rust runtime is not namespaced to match Core installer."""
from pathlib import Path
import sys
root=Path(sys.argv[1]) if len(sys.argv)>1 else Path('/srv/development/csqtt-core-e2e/upstream/rust-server')
installer=Path('server-assets/deploy-csqtt-core-250.sh').read_text()
source={n:(root/n).read_text() for n in ('tun_device.rs','net_setup.rs','model.rs')}
checks={
 'tun_device.rs':('pub const TUN_IFACE: &str = "csqtt-core0";', 'pub const TUN_SUBNET: &str = "10.67.69.0/24";'),
 'net_setup.rs':('pub const TUN_IFACE: &str = "csqtt-core0";', 'pub const TUN_ADDR: &str = "10.67.69.1";'),
 'model.rs':('pub const OWNER_TUNNEL_IP: &str = "10.67.69.2";',),
}
assert 'readonly CSQTT_IFACE="csqtt-core0"' in installer
assert '10.67.69.0/24' in installer
missing=[f'{file}: {item}' for file,items in checks.items() for item in items if item not in source[file]]
if missing:
 print('BLOCKED: CSQTT 2.5 runtime is incompatible with Core deploy:')
 for item in missing:print(' -',item)
 sys.exit(1)
print('CSQTT 2.5 Core runtime constants PASS')
