# CSQTT Core8 — actual private ARM64 TUN to TPROXY to SOCKS5 TCP/UDP data plane

Date: November 22, 2026 (Moscow local).
Result: PASS for real bidirectional TCP AND UDP packets with one patched
ARM64 Core8 binary, guest-local SOCKS5, exact payload echo, correct TCP
FIN/ACK closure, Linux TPROXY rule removal, and Rust-owned sysctl restoration.

This is NOT a real/public VK TURN, Go SOCKS5 or sing-box joined chain.

## Baseline and isolation

- Initial experimental GitHub branch HEAD cb6055f. Original validated
  release candidate HEAD 236e7b01446d0f01a58b9ad2d86bee093f5e1fe0 unchanged.
- Offline-built separately preserved patched Rust 2.5.0 ARM64 ELF SHA256
  8b6d7b948881e2446218198253b3f24908629b9831b6bf0c119d1ff483c9da1c.
  Guest original validated ELF SHA256
  a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706.
- Existing private Debian13 aarch64 QEMU guest csqtt-core8-isolated,
  QEMU user networking restrict=on with ONLY host 127.0.0.1:22379 SSH
  forwarded. NO public UDP, no guest IPv4 default route or host policy changes.
- Reusable manual opt-in isolated guest script:
  adapt/test-core8-private-arm64-tproxy-tun-packets.py.
  Reuses guest-local route preflight from
  adapt/test-core8-private-arm64-tproxy-active-policy.py and runs under
  an existing tested durable sysctl guard.
- Tun crate 0.8.14 creates a SINGLE-QUEUE interface by default. To inject
  real IP packets without modifying Core8 or opening a second queue,
  the guest root test used pidfd_open plus Linux pidfd_getfd to duplicate
  the actual Core8 /dev/net/tun file descriptor. The feature was verified
  first against a benign test process. No interference with real clients.
- Guest-local SOCKS5 server 127.0.0.1:46784 implements real SOCKS5
  UDP ASSOCIATE and TCP CONNECT plus UDP/TCP echo handling. The synthetic
  client is 10.67.69.29, remote target 198.18.0.42 (RFC2544 benchmark range),
  UDP target45456, TCP target45457. Neither this synthetic packet nor any
  port is exposed publicly. Core8 guest UDP127.0.0.1:46884 and web46885,
  ephemeral randomly generated process-only credentials.

## Successful FOURTH combined guest run — exit 0

Confirmed from actual real patched Core8 ARM64 stdout:

- TUN_DATAPLANE_GUARD_EXIT=0
- TUN_DATAPLANE_POLICY_ACTIVE=True
- TUN_DATAPLANE_PIDFD_GETFD_PASS=True
- TUN_DATAPLANE_UDP_BYTES_INJECTED=85
- TUN_DATAPLANE_SOCKS5_UDP_FRAME_MATCH=True
- TUN_DATAPLANE_UDP_SOCKS5_RELAY_PASS=True
- TUN_DATAPLANE_UDP_RELAY_REPLY_FRAMES_SENT=13
- TUN_DATAPLANE_UDP_REVERSE_SEEN=True
- TUN_CAPTURE_MATCH=(TUN_FD, READ, 198.18.0.42, 10.67.69.29, 17, 74)
- TUN_DATAPLANE_TCP_SOURCE_PORT=53017
- TUN_DATAPLANE_TCP_SYN_INJECTED=True
- TUN_DATAPLANE_TCP_SYNACK_SEEN=True
- TUN_DATAPLANE_TCP_SOCKS_CONNECT=True
- TUN_DATAPLANE_TCP_SOCKS_PAYLOAD=True
- TUN_DATAPLANE_TCP_SOCKS5_ECHO_REQUEST_PASS=True
- TUN_DATAPLANE_TCP_ECHO_REPLY_SEEN=True
- TUN_DATAPLANE_TCP_FIN_SENT=True
- TUN_DATAPLANE_TCP_FIN_RECEIVED=True
- TUN_DATAPLANE_TCP_FIN_ACK_SENT=True
- TUN_DATAPLANE_API_DEACTIVATED=True
- TUN_DATAPLANE_POLICY_RESTORED=True
- TUN_DATAPLANE_CORE8_EXIT=0
- TUN_DATAPLANE_FINAL_POLICY_RESTORED=True
- TUN_DATAPLANE_RUST_SYSCTL_RESTORED=True
- TUN_DATAPLANE_RUST_SYSCTL_DIFF={}
- CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED
- TUN_DATAPLANE_POST_GUARD_POLICY_RESTORED=True
- TUN_DATAPLANE_POST_GUARD_SYSCTL_RESTORED=True
- TUN_DATAPLANE_GUARD_JOURNAL_CLEARED=True

This validates actual TCP SYN/SYN-ACK/ACK, SOCKS5 CONNECT, echoed
TCP application bytes, and full TCP FIN/ACK; UDP packet was physically
injected into TUN, received with the exact destination and payload at
SOCKS5 UDP relay, and reverse reply was observed read from the real TUN.

## Earlier diagnostic attempts (all scoped to same disposable guest)

1. First run: UDP TUN to SOCKS5 payload verified. No reverse response
   or TCP SYNACK captured. Test attempted immediate policy comparison
   before asynchronous Core8 deactivation finished; guard restored baseline.
2. Second run: full TCP CONNECT/echo observed and UDP forward verified.
   Synthetic TCP client failed to close with FIN/ACK, leaving FIN-WAIT-1
   and a Core8 dataplane shutdown timeout. Rust-side sysctl check before
   external recovery was not equal; durable guard restored every value.
3. Third run: repeated UDP probes yielded actual TUN reverse UDP packet
   and Rust restored baseline. TCP SYNACK still not observed, likely
   due competition with Core8 single-queue TUN reader and possible
   reuse of a previous synthetic TCP four-tuple.
4. Fourth run: unique randomized TCP source port, bounded duplicate SYN
   probes, and client FIN/ACK handling. Full TCP and UDP data plane
   succeeded in ONE run, exit0; normal Rust-owned shutdown restored all
   sysctls before the outer guard. Core8 log did not report shutdown
   timeout. The mock sent at most 13 small UDP reverse frames.
- Raw full successful run stdout retained only in experimental Moscow VPS
  scratch directory as tun-dataplane-fourth-PASS-raw.log; SHA256:
  29f7886cead45264ba468963fdc03ebc0eb075b487fe0ca770f33072b56135c5.
  These local test logs contain no passwords or VK credentials.

## Independent final cleanup and scope

- Guest readback after the passing run: sysctl ip_forward=0; ip rules
  only 0,32766,32767; no TPROXY table30001/fwmark/iptables marker;
  CSQTT TUN csqtt-core0 absent; test UDP/TCP/SOCKS sockets closed;
  pending guard journal absent, guest IPv4 default route still absent.
  Guest original ELF SHA256 unchanged.
- Removed only guest test config/credentials/output scripts and copied
  experimental binary. The patched ELF remains in the separate Moscow
  VPS experimental scratch build folder, not installed on NanoPi.
- Private guest powered off gracefully. QEMU log Power down, PID absent,
  SSH 127.0.0.1:22379 listener closed, guest qcow2 preserved. Existing
  CSQTT Deploy and unrelated HydraRoute Core VMs and CSQTT tunnel active.
  Moscow host route and production services unaffected.
- Still UNVERIFIED: authorized live public VK Calls TURN to WIRE3 to
  Go SOCKS5 to sing-box; forced real Core8/wrapper SIGKILL, automatic
  crash restart and power-loss recovery. Earlier forced-kill test was
  blocked and not retried. No production release promotion.
