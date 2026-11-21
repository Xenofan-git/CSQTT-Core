#!/usr/bin/env python3
"""Disposable ARM64 kernel dataplane control, no CSQTT/Rust/Go involved."""
import os
import pathlib
import subprocess
import uuid
import socket
import fcntl
import struct

assert os.environ.get("GITHUB_ACTIONS")=="true"
assert os.environ.get("RUNNER_ENVIRONMENT")=="github-hosted"
assert os.geteuid()==0
assert subprocess.check_output(["uname","-m"],text=True).strip()=="aarch64"
assert pathlib.Path("/dev/net/tun").exists()
suf=uuid.uuid4().hex[:6]
cli='cp'+suf
srv='sp'+suf
v1='c'+suf
v2='s'+suf
outer_default=subprocess.check_output(["ip","-4","route","show","default"],text=True)

def call(a,check=True,timeout=10):
    p=subprocess.run(a,text=True,capture_output=True,timeout=timeout)
    if check and p.returncode:
        raise RuntimeError(f"call {a} exit={p.returncode} stderr={p.stderr[-350:]}")
    return p

def n(cmd,*args,check=True):
    return call(["ip","netns","exec",cmd,*args],check=check)

try:
    call(["ip","netns","add",cli])
    call(["ip","netns","add",srv])
    call(["ip","link","add",v1,"type","veth","peer","name",v2])
    call(["ip","link","set",v1,"netns",cli])
    call(["ip","link","set",v2,"netns",srv])
    n(cli,"ip","link","set",v1,"name","eth0")
    n(srv,"ip","link","set",v2,"name","eth0")
    for net,address in ((cli,"192.168.4.26/24"),(srv,"192.168.4.1/24")):
        n(net,"ip","link","set","lo","up")
        n(net,"ip","addr","add",address,"dev","eth0")
        n(net,"ip","link","set","eth0","up")
    n(cli,"ip","link","add","eth1","type","dummy")
    n(cli,"ip","addr","add","192.168.5.26/24","dev","eth1")
    n(cli,"ip","link","set","eth1","up")
    n(cli,"ip","tuntap","add","dev","csqtt0","mode","tun")
    n(cli,"ip","addr","add","10.67.69.2/32","dev","csqtt0")
    n(cli,"ip","link","set","csqtt0","up")
    n(cli,"ip","link","set","csqtt0","mtu","1300")
    for command in (
        ["route","add","192.168.4.0/24","dev","eth0","table","202"],
        ["route","add","192.168.5.0/24","dev","eth1","table","202"],
        ["route","add","default","dev","csqtt0","table","202"],
        ["rule","add","pref","22020","from","192.168.5.0/24","lookup","202"],
        ["rule","add","pref","22015","fwmark","0x4d53/0xffff","lookup","202"]
    ):
        n(cli,"ip",*command)
    print("ARM64_POLICY_ROUTE",n(cli,"ip","route","get","10.67.69.1","mark","0x4d53").stdout.strip(),flush=True)
    print("ARM64_POLICY_RULES",n(cli,"ip","rule","show").stdout.strip(),flush=True)
    print("ARM64_POLICY_ROUTES",n(cli,"ip","route","show","table","202").stdout.strip(),flush=True)
    code=r'''
import os,socket,fcntl,struct,time,select,subprocess
fd=os.open("/dev/net/tun",os.O_RDWR|os.O_NONBLOCK)
fcntl.ioctl(fd,0x400454ca,struct.pack("16sH",b"csqtt0",0x1001))
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET,socket.SO_MARK,0x4d53)
s.setblocking(False)
print("MARKED_CONNECT_ERRNO",s.connect_ex(("10.67.69.1",47093)),flush=True)
packets=[]
deadline=time.monotonic()+3
while time.monotonic()<deadline:
    ready,_,_=select.select([fd],[],[],.25)
    if ready:
        try: packet=os.read(fd,65535)
        except BlockingIOError:continue
        packets.append((len(packet),packet[0]>>4,packet[9] if len(packet)>9 else 0,
                        ".".join(str(n) for n in packet[16:20]) if len(packet)>=20 else "?"))
print("PRECISE_TUN_IPV4_PACKETS",packets,flush=True)
print("PRECISE_TUN_STATS",subprocess.check_output(["ip","-s","link","show","csqtt0"]).decode()[-420:],flush=True)
if not any(v==4 and proto==6 and dest=="10.67.69.1" for _,v,proto,dest in packets):
    raise SystemExit("NO_MARKED_TCP_IPV4_PACKET_ON_ARM64_KERNEL_TUN")
print("ARM64_NATIVE_TUN_MARKED_IPV4_TCP_SYN_PASS",flush=True)
'''
    check=n(cli,"python3","-c",code,check=False)
    print("ARM64_NATIVE_NETNS_KERNEL_CONTROL_EXIT",check.returncode,check.stdout[-2600:],check.stderr[-900:],flush=True)
    assert check.returncode==0,"native kernel route did not deliver marked TCP SYN to TUN"
finally:
    call(["ip","netns","delete",cli],check=False)
    call(["ip","netns","delete",srv],check=False)
    assert subprocess.check_output(["ip","-4","route","show","default"],text=True)==outer_default
    print("ARM64_NATIVE_KERNEL_CONTROL_CLEANUP_ROUTE_PRESERVED",flush=True)
