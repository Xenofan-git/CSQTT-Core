#!/usr/bin/env python3
"""Strict post-backport server runtime isolation for Linux CSQTT Core.

Fix Core-specific defaults and service targets *after* the pinned v2.5.0
derivation, audited Core8 overlay and selective v2.5.3 fixes.
No service starts, installs, network changes, or protocol changes.
"""
from pathlib import Path
import argparse,hashlib

INPUT_SHA = {
    "main.rs":"bab3a9cbac387e520043851251f42025a67b7522dc5b108cd05ed7c0c1d0939a",
    "web_panel.rs":"71b0bc5fa3208d8dd21b5d0f998e973ac5f96bbb94f50f8caf4ecb7b982970ae",
    "csqtt-core-panel.html":"08effb52dccb5de35f29b7605363c5f7d4aafdb87271e057bbbbb673cf9b6b5d",
}
OUTPUT_SHA = {
    "main.rs":"e1751dfacb4a8a0cb264cb3e9b1327363574897edeb03b550c2bacbd763e257d",
    "web_panel.rs":"defc37fa170c99b4a877369ee75d0a416235d2c1c122f548efd77d00093f96d2",
    "csqtt-core-panel.html":"600e4e6ef1153ced8fbc831df692be4e7ad171e81440b6d952f68d3c9104d040",
}
def sub(src,old,new,description,count=1):
    actual=src.count(old)
    if actual!=count: raise ValueError(f"{description}: required {count} exact anchors, found {actual}")
    return src.replace(old,new)
def patch_main(s):
    for old,new,description,count in [
        ('#[command(name = "csqtt", version, about = "Сервер и консоль управления CSQTT")]',
         '#[command(name = "csqtt-core", version, about = "Сервер и консоль управления CSQTT Core")]',
         'cli program name',1),
        ('default_value = "0.0.0.0:46000"','default_value = "0.0.0.0:47000"','UDP default port',1),
        ('default_value_t = 46002','default_value_t = 47002','web default port',1),
        ('default_value = "/etc/csqtt"','default_value = "/etc/csqtt-core"','server default state path',1),
        ('docker start/stop csqtt','docker start/stop csqtt-core','Docker command diagnostic',1),
        ('docker restart csqtt','docker restart csqtt-core','Docker restart diagnostic',1),
        ('для управления службой csqtt нужны права root','для управления службой csqtt-core нужны права root','systemd diagnostic',1),
        ('Выполняется systemctl {action} csqtt...', 'Выполняется systemctl {action} csqtt-core...', 'systemd feedback',1),
        ('command.args([action, "csqtt"])','command.args([action, "csqtt-core"])','systemd start stop target',1),
        ('.args(["--no-block", "restart", "csqtt"])','.args(["--no-block", "restart", "csqtt-core"])','panel restart target',1),
        ('csqtt-panel-restart-', 'csqtt-core-panel-restart-', 'private systemd restart helper',1),
        ('  csqtt start ','  csqtt-core start ','CLI help start',1),
        ('  csqtt stop ', '  csqtt-core stop ','CLI help stop',1),
        ('  csqtt restart ','  csqtt-core restart ','CLI help restart',1),
        ('  csqtt dpi ', '  csqtt-core dpi ','CLI help dpi',2),
        ('  csqtt help ', '  csqtt-core help ','CLI help usage',1),
        ('по умолчанию 0.0.0.0:46000','по умолчанию 0.0.0.0:47000','help UDP default',1),
        ('по умолчанию 46002','по умолчанию 47002','help HTTPS default',1),
        ('по умолчанию /etc/csqtt','по умолчанию /etc/csqtt-core','help config location',1),
        ('csqtt-server must run as root','csqtt-core-server must run as root','root-required diagnostic',1),
    ]:
        s=sub(s,old,new,description,count)
    return s
def patch_web(s):
    return sub(s,'if port == 0 { 46000 } else { port }',
               'if port == 0 { 47000 } else { port }','generated client link fallback')
def patch_panel(s):
    return sub(s,'46000','47000','approved server panel example/default ports',6)
def apply(root,check_only=False):
    transformed={}
    for name,fn in [('main.rs',patch_main),('web_panel.rs',patch_web),
                    ('csqtt-core-panel.html',patch_panel)]:
        p=root/name
        text=p.read_text(encoding='utf8')
        h=hashlib.sha256(text.encode()).hexdigest()
        if h!=INPUT_SHA[name]:
            raise ValueError(f"{name}: unexpected original CSQTT Core patched source {h}")
        new=fn(text)
        derived=hashlib.sha256(new.encode()).hexdigest()
        if OUTPUT_SHA[name].startswith('INSERT_') and not check_only:
            raise ValueError(f"{name}: expected isolated output pin missing")
        if not OUTPUT_SHA[name].startswith('INSERT_') and derived!=OUTPUT_SHA[name]:
            raise ValueError(f"{name}: generated isolate result mismatch {derived}")
        transformed[p]=new
        print("RUNTIME_ISOLATION_EXPECTED_OUTPUT",name,derived)
    if not check_only:
        for p,new in transformed.items():p.write_text(new,encoding='utf8')
        print('CSQTT_CORE_LINUX_RUNTIME_DEFAULTS_ISOLATED_PASS')
    else:
        print('CSQTT_CORE_LINUX_RUNTIME_ISOLATION_SOURCE_CHECK_PASS')
def main():
    a=argparse.ArgumentParser()
    a.add_argument('--source-root',type=Path,required=True)
    a.add_argument('--check-only',action='store_true')
    x=a.parse_args()
    apply(x.source_root.resolve(),x.check_only)
if __name__=="__main__":main()
