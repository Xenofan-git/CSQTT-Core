#!/usr/bin/env python3
"""Safe live CLI checks: refuse mutations before touching host state."""
import os
import subprocess
from pathlib import Path
script=Path('server-assets/deploy-csqtt-core-250.sh')
env=os.environ.copy();env.pop('CSQTT_CORE_ALLOW_MUTATION',None)
for action in ([],['install'],['--install'],['uninstall'],['--uninstall']):
    p=subprocess.run(['bash',str(script),*action],env=env,capture_output=True,text=True,timeout=10)
    assert p.returncode==42,(action,p.returncode,p.stderr[-250:])
    assert 'REFUSED:' in p.stderr,action
    print('PASS denied',action or ['default install'])
p=subprocess.run(['bash',str(script),'status'],env=env,capture_output=True,text=True,timeout=10)
assert p.returncode==0,(p.returncode,p.stderr)
assert 'Статус CSQTT' in p.stdout
print('PASS status read-only')
