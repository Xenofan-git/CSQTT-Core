#!/usr/bin/env python3
"""ARM64 disposable guest-only Go + Sing-box real local TCP acceptance.

No credentials or VPN connect requests: panel authentication MUST fail closed.
"""
import hashlib
import http.client
import os
from pathlib import Path
import platform
import socket
import sys
import time

def safe():
    if platform.machine() != "aarch64" or socket.gethostname() != "csqtt-core8-isolated" or os.geteuid()==0:
        raise RuntimeError("nonroot disposable guest required")

def once():
    for filename,expected in {
        "/usr/local/libexec/csqtt-core-client250-arm64":"3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa",
        "/usr/local/libexec/csqtt-core8-selective253-arm64":"8cd789e33428f96e183ae3da326dee907496f11536fbaf6cdab5da339262701d",
    }.items():
        if hashlib.sha256(Path(filename).read_bytes()).hexdigest()!=expected:
            raise RuntimeError("offline Rust component SHA mismatch")
    c=http.client.HTTPConnection("127.0.0.1",1062,timeout=3)
    try:
        c.request("GET","/ping")
        response=c.getresponse()
        _=response.read()
        if response.status!=503:
            raise RuntimeError("headless API missing-auth did not fail closed")
    finally:
        c.close()
    with socket.create_connection(("127.0.0.1",19180),timeout=4) as s:
        s.settimeout(4)
        s.sendall(b"\x05\x01\x00")
        if recv(s,2)!=b"\x05\x00":raise RuntimeError("sing-box SOCKS5 auth negotiation failed")
        # Route locally via the actual Sing-box to the guest SSH server.
        s.sendall(bytes([5,1,0,1,127,0,0,1,0,22]))
        header=recv(s,4)
        if header[1]!=0:raise RuntimeError("SOCKS5 actual CONNECT refused")
        length=6 if header[3]==1 else 18 if header[3]==4 else None
        if header[3]==3:length=recv(s,1)[0]+2
        if length is None:raise RuntimeError("SOCKS5 reply invalid")
        recv(s,length)
        banner=recv_until(s,b"\n",100)
        if not banner.startswith(b"SSH-2.0-"):
            raise RuntimeError("no actual TCP application response from SOCKS5")
    print("CSQTT_FULLAPP_QEMU_ARM64_USER_SYSTEMD_GO_AND_SINGBOX_REAL_TCP_PASS",flush=True)
    print("CSQTT_FULLAPP_QEMU_RUST_CLIENT_AND_CORE8_HASH_VERIFIED_NO_VK_CONNECT",flush=True)

def recv(s,n):
    b=bytearray()
    while len(b)<n:
        data=s.recv(n-len(b))
        if not data:raise RuntimeError("TCP closed")
        b.extend(data)
    return bytes(b)

def recv_until(s,end,limit):
    out=bytearray()
    while len(out)<limit:
        b=s.recv(1)
        if not b:raise RuntimeError("TCP banner closed")
        out.extend(b)
        if out.endswith(end):return bytes(out)
    raise RuntimeError("oversized TCP banner")

if __name__=="__main__":
    safe()
    deadline=time.monotonic()+75
    previous=""
    while time.monotonic()<deadline:
        try:
            once()
            sys.exit(0)
        except (OSError,RuntimeError) as e:
            previous=str(e)
            time.sleep(2)
    print("CSQTT_FULLAPP_QEMU_GUEST_BOOT_FAILURE:",previous,flush=True)
    sys.exit(1)
