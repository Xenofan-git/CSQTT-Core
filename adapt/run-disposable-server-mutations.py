#!/usr/bin/env python3
"""Run destructive API E2E only against a fresh, local, disposable CSQTT process."""
import argparse, os, secrets, socket, subprocess, sys, tempfile, time
from pathlib import Path
p=argparse.ArgumentParser(); p.add_argument('--binary',type=Path,required=True); a=p.parse_args()
assert a.binary.is_file()
with tempfile.TemporaryDirectory(prefix='csqtt-disposable-e2e-') as temp:
    root=Path(temp)
    def port():
        with socket.socket() as s:
            s.bind(('127.0.0.1',0)); return s.getsockname()[1]
    web, udp=port(),port()
    username='e2e-'+secrets.token_hex(6)
    password=secrets.token_urlsafe(24)
    master=secrets.token_urlsafe(24)
    prefix='e2e-'+secrets.token_hex(10)
    env=dict(os.environ, CSQTT_MAIN_PASSWORD=master, CSQTT_WEB_USER=username,
             CSQTT_WEB_PASS=password, CSQTT_WEB_SECRET_PATH=prefix)
    command=[str(a.binary),'--listen',f'127.0.0.1:{udp}', '--web-port',str(web), '--config-dir',str(root)]
    with (root/'server.log').open('w') as log:
        process=subprocess.Popen(command,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
        try:
            import urllib.request, ssl
            url=f'https://127.0.0.1:{web}/{prefix}'
            for _ in range(60):
                if process.poll() is not None: raise RuntimeError('isolated CSQTT process exited before ready')
                try:
                    urllib.request.urlopen(url,context=ssl._create_unverified_context(),timeout=1)
                    break
                except Exception: time.sleep(.5)
            else: raise RuntimeError('isolated CSQTT not ready')
            testenv=dict(env,CSQTT_E2E_BASE_URL=url,CSQTT_E2E_WEB_USER=username,
                         CSQTT_E2E_WEB_PASS=password,CSQTT_E2E_ALLOW_MUTATIONS='isolated-only')
            result=subprocess.run([sys.executable,str(Path(__file__).with_name('test-csqtt-server-mutations-e2e.py'))],env=testenv,timeout=100)
            if result.returncode: raise RuntimeError(f'mutation E2E failed with code {result.returncode}')
        finally:
            process.terminate()
            try: process.wait(timeout=5)
            except subprocess.TimeoutExpired: process.kill();process.wait()
