#!/usr/bin/env python3
"""Manual isolated ARM64 data-plane acceptance: real TUN UDP and TCP over TPROXY.

Must execute as root ONLY in the preexisting restricted QEMU guest. Uses
pidfd_getfd to duplicate the Core8-owned *single-queue* TUN descriptor, so
it never alters production binary or opens a second TUN queue. Injects tiny
crafted IPv4 UDP and TCP packets representing a synthetic VPN client, then
verifies SOCKS5 UDP relay data and TCP CONNECT/echo in a guest-local mock.
Never opens public UDP or changes the default route. All kernel mutations
belong to the existing, private guest Core8 and are restored on stop.
"""
import argparse
import ctypes
import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import random
import select
import socket
import socketserver
import ssl
import stat
import struct
import subprocess
import sys
import threading
import time
import uuid

BASE_SCRIPT=Path("/home/csqttlab/test-tproxy-active-policy.py")
spec=importlib.util.spec_from_file_location("policy_test",BASE_SCRIPT)
policy=importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)

policy.SOCKS_PORT=46784
policy.UDP_PORT=46884
policy.WEB_PORT=46885
policy.BASE_URL=f"https://127.0.0.1:{policy.WEB_PORT}"
policy.OUT=Path("/var/lib/csqtt-core-private/tproxy-dataplane-acceptance")

CLIENT="10.67.69.29"
REMOTE="198.18.0.42"  # RFC2544 benchmark space, NEVER sent publicly
UDP_SOURCE_PORT=39251
UDP_TARGET_PORT=45456
# A fresh TCP 4-tuple per run prevents FIN-WAIT of a previous synthetic
# test client from colliding with the next SYN.
TCP_SOURCE_PORT=random.randint(43000,60999)
TCP_TARGET_PORT=45457
UDP_PROBE=b"CSQTT_CORE8_REAL_TUN_UDP_"+uuid.uuid4().hex.encode("ascii")
TCP_PROBE=b"CSQTT_CORE8_REAL_TUN_TCP_"+uuid.uuid4().hex.encode("ascii")


def check(cond,msg):
    if not cond:raise RuntimeError(msg)


def sum16(data:bytes)->int:
    if len(data)&1:data+=b"\0"
    x=sum(struct.unpack("!%dH"%(len(data)//2),data))
    while x>>16:x=(x&65535)+(x>>16)
    return (~x)&65535


def ip_packet(proto:int,data:bytes,src=CLIENT,dst=REMOTE)->bytes:
    ip_src=socket.inet_aton(src)
    ip_dst=socket.inet_aton(dst)
    hdr=struct.pack("!BBHHHBBH4s4s",0x45,0,len(data)+20,random.randint(1,65535),0,64,proto,0,ip_src,ip_dst)
    hcs=sum16(hdr)
    hdr=hdr[:10]+struct.pack("!H",hcs)+hdr[12:]
    return hdr+data


def udp_ip(payload:bytes)->bytes:
    hdr=struct.pack("!HHHH",UDP_SOURCE_PORT,UDP_TARGET_PORT,8+len(payload),0)
    return ip_packet(17,hdr+payload)


def tcp_ip(seq:int,ack:int,flags:int,data:bytes=b"")->bytes:
    src=socket.inet_aton(CLIENT);dst=socket.inet_aton(REMOTE)
    header=struct.pack("!HHIIBBHHH",TCP_SOURCE_PORT,TCP_TARGET_PORT,seq&0xffffffff,ack&0xffffffff,
                       5<<4,flags,65535,0,0)
    total=header+data
    pseudo=src+dst+struct.pack("!BBH",0,6,len(total))
    csum=sum16(pseudo+total)
    header=header[:16]+struct.pack("!H",csum)+header[18:]
    return ip_packet(6,header+data)


def parse_ip(packet:bytes):
    if len(packet)<20 or packet[0]>>4!=4:return None
    ihl=(packet[0]&15)*4
    if ihl<20 or len(packet)<ihl:return None
    return (socket.inet_ntoa(packet[12:16]),socket.inet_ntoa(packet[16:20]),packet[9],packet[ihl:])


class Handler(socketserver.BaseRequestHandler):
    def recv_exact(self,n):
        v=b""
        while len(v)<n:
            chunk=self.request.recv(n-len(v))
            if not chunk:raise EOFError()
            v+=chunk
        return v

    def handle(self):
        self.request.settimeout(12)
        try:
            ver,n=self.recv_exact(2)
            methods=self.recv_exact(n)
            check(ver==5 and 0 in methods,"SOCKS5 handshake failed")
            self.request.sendall(b"\x05\x00")
            ver,cmd,rsv,atyp=self.recv_exact(4)
            check(ver==5 and rsv==0 and atyp==1,"non-IPv4 SOCKS5 request")
            address=self.recv_exact(4)
            port=int.from_bytes(self.recv_exact(2),"big")
            if cmd==3:
                relay=self.server.relay.getsockname()
                self.request.sendall(b"\x05\x00\x00\x01"+socket.inet_aton("127.0.0.1")+struct.pack("!H",relay[1]))
                self.server.associations+=1
                self.request.settimeout(1)
                while not self.server.done.is_set():
                    try:
                        if not self.request.recv(1):break
                    except socket.timeout:continue
            elif cmd==1:
                self.server.tcp_destination=(socket.inet_ntoa(address),port)
                self.server.tcp_connect_event.set()
                self.request.sendall(b"\x05\x00\x00\x01"+socket.inet_aton("127.0.0.1")+b"\0\0")
                self.request.settimeout(1)
                while not self.server.done.is_set():
                    try:
                        body=self.request.recv(8192)
                        if not body:break
                        self.server.tcp_received+=body
                        self.server.tcp_data_event.set()
                        self.request.sendall(body)  # echo data; guest only
                    except socket.timeout:continue
            else:
                raise RuntimeError(f"unexpected SOCKS cmd {cmd}")
        except (OSError,EOFError,RuntimeError) as exc:
            self.server.errors.append(str(exc))


class SocksServer(socketserver.ThreadingTCPServer):
    daemon_threads=True
    allow_reuse_address=False

    def __init__(self):
        self.done=threading.Event()
        self.udp_received_event=threading.Event()
        self.tcp_connect_event=threading.Event()
        self.tcp_data_event=threading.Event()
        self.udp_received=[]
        self.udp_replies_sent=0
        self.errors=[]
        self.tcp_received=b""
        self.tcp_destination=None
        self.associations=0
        self.relay=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
        self.relay.bind(("127.0.0.1",0))
        self.relay.settimeout(1)
        super().__init__(("127.0.0.1",policy.SOCKS_PORT),Handler)
        self.worker=threading.Thread(target=self._udp_loop,daemon=True)

    def start(self):
        threading.Thread(target=self.serve_forever,daemon=True).start()
        self.worker.start()

    def _udp_loop(self):
        while not self.done.is_set():
            try:
                frame,peer=self.relay.recvfrom(65535)
                if len(frame)<10 or frame[:4]!=b"\0\0\0\x01":continue
                address=socket.inet_ntoa(frame[4:8])
                port=int.from_bytes(frame[8:10],"big")
                payload=frame[10:]
                self.udp_received.append((address,port,payload))
                self.udp_received_event.set()
                self.relay.sendto(frame[:10]+b"udp-echo:"+payload,peer)
                self.udp_replies_sent+=1
            except socket.timeout:continue
            except OSError:break

    def close_all(self):
        self.done.set()
        self.shutdown()
        self.server_close()
        self.relay.close()
        self.worker.join(timeout=2)


def duplicate_core8_tun(pid:int)->int:
    paths=[]
    for entry in Path(f"/proc/{pid}/fd").iterdir():
        try:target=os.readlink(entry)
        except OSError:continue
        if target=="/dev/net/tun":paths.append(int(entry.name))
    check(len(paths)==1,f"expected exactly one Core8 TUN descriptor, found {paths}")
    pidfd=os.pidfd_open(pid)
    libc=ctypes.CDLL(None,use_errno=True)
    try:
        newfd=libc.syscall(438,pidfd,paths[0],0)
        if newfd<0:raise OSError(ctypes.get_errno(),os.strerror(ctypes.get_errno()))
    finally:os.close(pidfd)
    check(stat.S_ISCHR(os.fstat(newfd).st_mode),"duplicated tun FD not char dev")
    return newfd


def capture_socket(ifname:str):
    sock=socket.socket(socket.AF_PACKET,socket.SOCK_DGRAM,socket.htons(0x0003))
    sock.bind((ifname,socket.htons(0x0003)))
    sock.setblocking(False)
    return sock


def await_packet(sniffer,predicate,timeout=18,tunfd=None):
    deadline=time.monotonic()+timeout
    seen=[]
    while time.monotonic()<deadline:
        fds=[sniffer]+([tunfd] if tunfd is not None else [])
        ready,_,_=select.select(fds,[],[],min(0.9,deadline-time.monotonic()))
        for fd in ready:
            try:
                if fd==sniffer:
                    packet,meta=sniffer.recvfrom(65535)
                    capture="AF_PACKET"
                    kind=meta[2] if len(meta)>2 else "?"
                else:
                    packet=os.read(tunfd,65535)
                    capture="TUN_FD"
                    kind="READ"
            except (BlockingIOError,OSError):
                continue
            parsed=parse_ip(packet)
            if parsed is None:continue
            seen.append((capture,kind,parsed[0],parsed[1],parsed[2],len(parsed[3])))
            if parsed[0]==REMOTE and parsed[1]==CLIENT and predicate(parsed):
                print("TUN_CAPTURE_MATCH="+repr(seen[-1]),flush=True)
                return parsed
    print("TUN_CAPTURE_TOTAL="+str(len(seen)),flush=True)
    print("TUN_CAPTURE_SAMPLE="+repr(seen[-24:]),flush=True)
    return None


def inner(expected, test_case="normal"):
    policy.preflight(expected,inside=True)
    check(test_case in ("normal", "open-tcp"), "unknown test case")
    print("TUN_DATAPLANE_TEST_CASE="+test_case,flush=True)
    OUT=policy.OUT
    before=json.loads((OUT/"sysctl-baseline.json").read_text())
    policy_before=json.loads((OUT/"policy-baseline.json").read_text())
    check(policy.snapshot()==before,"sysctl not baseline")
    check(policy.policy_snapshot()==policy_before,"policy not baseline")
    socks=SocksServer()
    socks.start()
    web_pass=uuid.uuid4().hex*2
    env={**os.environ,"CSQTT_MAIN_PASSWORD":uuid.uuid4().hex*2,"CSQTT_WEB_PASS":web_pass}
    proc=None
    active=False
    tun_fd=None
    sniffer=None
    profile=None
    outcome=None
    try:
        with (OUT/"core8-dataplane.log").open("w") as log:
            proc=subprocess.Popen([str(policy.ELF),"--listen",f"127.0.0.1:{policy.UDP_PORT}",
                "--web-port",str(policy.WEB_PORT),"--config-dir",str(OUT/"config")],
                env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
            for _ in range(34):
                if proc.poll() is not None:break
                if not policy.no_listener(policy.UDP_PORT) and not policy.no_listener(policy.WEB_PORT):break
                time.sleep(.8)
            check(proc.poll() is None and not policy.no_listener(policy.WEB_PORT),
                  f"patched Core8 did not launch (exit={proc.poll()})")
            profile=policy.request("/api/local-proxy",web_pass,{"name":"Synthetic TUN datagram test",
                "port":policy.SOCKS_PORT,"username":"","password":""})[1]["id"]
            activated=policy.post("/api/local-proxy/activate/"+profile,web_pass)[1]
            check(activated.get("activated") is True,"TPROXY API did not activate")
            active=True
            installed=policy.policy_snapshot()
            check(policy.owned_policy(installed),"TPROXY kernel policy not installed")
            check(not policy.run("ip","-4","route","show","default").stdout.strip(),
                  "guest IPv4 default route appeared")
            print("TUN_DATAPLANE_POLICY_ACTIVE=True",flush=True)
            tun_fd=duplicate_core8_tun(proc.pid)
            print("TUN_DATAPLANE_PIDFD_GETFD_PASS=True",flush=True)
            sniffer=capture_socket("csqtt-core0")
            print("TUN_DATAPLANE_AF_PACKET_CAPTURE_READY=True",flush=True)
            ip_udp=udp_ip(UDP_PROBE)
            n=os.write(tun_fd,ip_udp)
            print("TUN_DATAPLANE_UDP_BYTES_INJECTED="+str(n),flush=True)
            check(n==len(ip_udp),"short TUN packet injection")
            check(socks.udp_received_event.wait(23),"SOCKS5 relay did not receive injected UDP datagram")
            frames=[p for p in socks.udp_received if p[2]==UDP_PROBE]
            print("TUN_DATAPLANE_SOCKS5_UDP_FRAME_MATCH="+str(bool(frames)),flush=True)
            check(frames and frames[0][:2]==(REMOTE,UDP_TARGET_PORT),
                  f"SOCKS5 UDP header was wrong: {[(x[0],x[1],len(x[2])) for x in socks.udp_received]}")
            print("TUN_DATAPLANE_UDP_SOCKS5_RELAY_PASS=True",flush=True)
            # Repeat only 12 short, local datagrams while reading from the
            # competing Core8-owned single-queue TUN descriptor. A single
            # reply can otherwise be consumed by Core8 before our reader.
            def udp_pump():
                for _ in range(12):
                    os.write(tun_fd,udp_ip(UDP_PROBE))
                    time.sleep(.16)
            pump=threading.Thread(target=udp_pump,daemon=True)
            pump.start()
            reply=await_packet(sniffer,lambda p: p[2]==17 and b"udp-echo:"+UDP_PROBE in p[3],
                               timeout=12,tunfd=tun_fd)
            pump.join(timeout=4)
            print("TUN_DATAPLANE_UDP_RELAY_REPLY_FRAMES_SENT="+str(socks.udp_replies_sent),flush=True)
            print("TUN_DATAPLANE_UDP_REVERSE_SEEN="+str(reply is not None),flush=True)

            # Handshake an in-memory synthetic TCP peer over the *real* guest TUN.
            client_seq=random.randint(10000,10000000)
            syn=tcp_ip(client_seq,0,0x02)
            print("TUN_DATAPLANE_TCP_SOURCE_PORT="+str(TCP_SOURCE_PORT),flush=True)
            # Repeated SYNs from the SAME synthetic connection avoid races
            # with Core8's normal TUN reader consuming the SYN-ACK first.
            syn_done=threading.Event()
            def syn_pump():
                for _ in range(12):
                    if syn_done.is_set():break
                    os.write(tun_fd,syn)
                    time.sleep(.30)
            syn_thread=threading.Thread(target=syn_pump,daemon=True)
            syn_thread.start()
            print("TUN_DATAPLANE_TCP_SYN_INJECTED=True",flush=True)
            def is_synack(p):
                if p[2]!=6 or len(p[3])<20:return False
                srcport,dstport=struct.unpack("!HH",p[3][:4])
                flags=p[3][13]
                return srcport==TCP_TARGET_PORT and dstport==TCP_SOURCE_PORT and flags&0x12==0x12
            synack=await_packet(sniffer,is_synack,timeout=17,tunfd=tun_fd)
            syn_done.set()
            syn_thread.join(timeout=3)
            print("TUN_DATAPLANE_TCP_SYNACK_SEEN="+str(synack is not None),flush=True)
            if synack:
                server_seq=struct.unpack("!I",synack[3][4:8])[0]
                ack=tcp_ip(client_seq+1,server_seq+1,0x10)
                os.write(tun_fd,ack)
                print("TUN_DATAPLANE_TCP_SOCKS_CONNECT="+str(socks.tcp_connect_event.wait(22)),flush=True)
                if socks.tcp_connect_event.is_set():
                    check(socks.tcp_destination==(REMOTE,TCP_TARGET_PORT),
                          f"wrong SOCKS5 TCP CONNECT destination {socks.tcp_destination}")
                    data=tcp_ip(client_seq+1,server_seq+1,0x18,TCP_PROBE)
                    os.write(tun_fd,data)
                    print("TUN_DATAPLANE_TCP_SOCKS_PAYLOAD="+str(socks.tcp_data_event.wait(22)),flush=True)
                    if socks.tcp_data_event.is_set():
                        check(TCP_PROBE in socks.tcp_received,"TCP payload mismatch")
                        print("TUN_DATAPLANE_TCP_SOCKS5_ECHO_REQUEST_PASS=True",flush=True)
                        echoed=await_packet(sniffer,lambda p:
                            p[2]==6 and TCP_PROBE in p[3],timeout=16,tunfd=tun_fd)
                        print("TUN_DATAPLANE_TCP_ECHO_REPLY_SEEN="+str(echoed is not None),flush=True)
                        if echoed and test_case=="open-tcp":
                            print("TUN_DATAPLANE_TCP_INTENTIONALLY_LEFT_OPEN=True",flush=True)
                        if echoed and test_case=="normal":
                            # Properly finish the synthetic TCP session so
                            # Core8's shutdown need not time out waiting for
                            # an unacknowledged fake client.
                            raw=echoed[3]
                            offset=(raw[12]>>4)*4
                            server_data=raw[offset:]
                            assert TCP_PROBE in server_data
                            next_client_seq=(client_seq+1+len(TCP_PROBE))&0xffffffff
                            next_server_ack=(struct.unpack("!I",raw[4:8])[0]+len(server_data))&0xffffffff
                            os.write(tun_fd,tcp_ip(next_client_seq,next_server_ack,0x10))
                            os.write(tun_fd,tcp_ip(next_client_seq,next_server_ack,0x11))  # FIN/ACK
                            print("TUN_DATAPLANE_TCP_FIN_SENT=True",flush=True)
                            def is_fin(p):
                                if p[2]!=6 or len(p[3])<20:return False
                                sport,dport=struct.unpack("!HH",p[3][:4])
                                return sport==TCP_TARGET_PORT and dport==TCP_SOURCE_PORT and bool(p[3][13]&0x01)
                            fin_response=await_packet(sniffer,is_fin,timeout=12,tunfd=tun_fd)
                            print("TUN_DATAPLANE_TCP_FIN_RECEIVED="+str(fin_response is not None),flush=True)
                            if fin_response:
                                fin_raw=fin_response[3]
                                fin_len=len(fin_raw)-(fin_raw[12]>>4)*4
                                server_ack=(struct.unpack("!I",fin_raw[4:8])[0]+fin_len+1)&0xffffffff
                                os.write(tun_fd,tcp_ip(next_client_seq+1,server_ack,0x10))
                                print("TUN_DATAPLANE_TCP_FIN_ACK_SENT=True",flush=True)
                            time.sleep(1)
                        outcome=(reply is not None,True,echoed is not None)
                    else:outcome=(reply is not None,False,False)
                else:outcome=(reply is not None,False,False)
            else:outcome=(reply is not None,False,False)

            stopped=policy.post("/api/local-proxy/deactivate",web_pass)[1]
            print("TUN_DATAPLANE_API_DEACTIVATED="+str(stopped.get("deactivated")),flush=True)
            check(stopped.get("deactivated") is True,"route deactivation API failed")
            active=False
            for _ in range(35):
                if policy.policy_snapshot()==policy_before:
                    break
                time.sleep(0.2)
            check(policy.policy_snapshot()==policy_before,"routing policy did not return to baseline")
            print("TUN_DATAPLANE_POLICY_RESTORED=True",flush=True)
    finally:
        if sniffer:sniffer.close()
        if tun_fd is not None:os.close(tun_fd)
        if proc is not None:
            if proc.poll() is None and active:
                try:policy.post("/api/local-proxy/deactivate",web_pass)
                except Exception as e:print("TUN_DATAPLANE_DEACTIVATE_WARNING="+str(e),flush=True)
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=37)
            print("TUN_DATAPLANE_CORE8_EXIT="+str(proc.returncode),flush=True)
        socks.close_all()
    print("TUN_DATAPLANE_FINAL_POLICY_RESTORED="+str(policy.policy_snapshot()==policy_before),flush=True)
    after_sysctl=policy.snapshot()
    diff={k:{"before":before[k],"after":after_sysctl[k]} for k in before if before[k]!=after_sysctl[k]}
    print("TUN_DATAPLANE_RUST_SYSCTL_RESTORED="+str(not diff),flush=True)
    print("TUN_DATAPLANE_RUST_SYSCTL_DIFF="+repr(diff),flush=True)
    check(policy.policy_snapshot()==policy_before,"route remains after normal cleanup")
    if test_case=="normal":
        check(not diff,"sysctl not restored before guard")
    else:
        print("TUN_DATAPLANE_OPEN_TCP_RUST_ONLY_RESTORE_STATUS="+
              ("RESTORED" if not diff else "NEEDS_EXTERNAL_GUARD"),flush=True)
    check(outcome is not None,"UDP test incomplete")
    return 0 if all(outcome) else 3  # no false success for partial packet delivery


def outer(expected, test_case="normal"):
    policy.preflight(expected)
    check(test_case in ("normal", "open-tcp"), "unknown test case")
    OUT=policy.OUT
    check(not OUT.exists(),"previous acceptance dir exists; inspect it")
    OUT.mkdir(mode=0o700,parents=True)
    baseline=policy.snapshot()
    route_baseline=policy.policy_snapshot()
    (OUT/"sysctl-baseline.json").write_text(json.dumps(baseline,sort_keys=True))
    (OUT/"policy-baseline.json").write_text(json.dumps(route_baseline,sort_keys=True))
    args=[sys.executable,"-B",str(policy.GUARD),"--state-dir",str(policy.STATE),
          "--sysctl-root","/proc/sys","--boot-id-file","/proc/sys/kernel/random/boot_id",
          "--private-guest","run","--",sys.executable,"-B",str(Path(__file__).resolve()),"inner",expected,test_case]
    result=subprocess.run(args,capture_output=True,text=True,timeout=225)
    print("TUN_DATAPLANE_GUARD_EXIT="+str(result.returncode),flush=True)
    print(result.stdout,flush=True)
    if result.stderr:print("TUN_DATAPLANE_STDERR="+result.stderr[-2500:],flush=True)
    print("TUN_DATAPLANE_POST_GUARD_POLICY_RESTORED="+str(policy.policy_snapshot()==route_baseline),flush=True)
    print("TUN_DATAPLANE_POST_GUARD_SYSCTL_RESTORED="+str(policy.snapshot()==baseline),flush=True)
    print("TUN_DATAPLANE_GUARD_JOURNAL_CLEARED="+str(not (policy.STATE/"pending-sysctls-v1.json").exists()),flush=True)
    check(policy.policy_snapshot()==route_baseline,"routing policy cleanup failed")
    check(policy.snapshot()==baseline,"kernel sysctl cleanup failed")
    check(not (policy.STATE/"pending-sysctls-v1.json").exists(),"pending guard journal")
    return result.returncode


if __name__=="__main__":
    parser=argparse.ArgumentParser()
    parser.add_argument("mode",choices=("outer","inner"))
    parser.add_argument("expected_sha")
    parser.add_argument("test_case",nargs="?",choices=("normal","open-tcp"),default="normal")
    opts=parser.parse_args()
    if opts.test_case=="open-tcp":
        # Separate trial state. Do not alter existing proven normal-path artifacts.
        policy.OUT=Path("/var/lib/csqtt-core-private/tproxy-stalled-tcp-acceptance")
    try:sys.exit(outer(opts.expected_sha,opts.test_case) if opts.mode=="outer" else inner(opts.expected_sha,opts.test_case))
    except Exception as error:
        print("TUN_DATAPLANE_ACCEPTANCE_FAILED="+str(error),file=sys.stderr)
        sys.exit(75)
