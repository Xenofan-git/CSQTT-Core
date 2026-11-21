# CSQTT Core8 — actual private ARM64 TPROXY activation/rollback

Date: November 22, 2026. **PASS: real patched ARM64 Core8 activation of TPROXY
with kernel fwmark policy, TCP/UDP rules and complete normal deactivation.**
NOT a TCP/UDP packet delivery or authorized VK Calls TURN end-to-end test.

## Baseline and isolation

- Moscow VPS original validated release branch HEAD236e7b0 untouched.
- Experiment-only branch started at HEAD4aa2943. Separate 9,589,352-byte
  patched Rust ARM64 Core8 2.5.0 ELF SHA256:
  8b6d7b948881e2446218198253b3f24908629b9831b6bf0c119d1ff483c9da1c.
  Includes opt-in KernelNetworking RAII sysctl restoration and TPROXY
  single-kernel-owner readiness-check source patch. Original guest WIRE3 ELF
  SHA256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706.
- Only pre-existing private Debian13 AArch64 QEMU guest
  csqtt-250-core8-isolated-vm-20261121 was booted, hostname
  csqtt-core8-isolated. Guest networking restrict=on; the only host
  forward was TCP SSH 127.0.0.1:22379. NO public UDP, no guest IPv4
  default route, no host network/policy or NanoPi production changes.
- Manual opt-in test: adapt/test-core8-private-arm64-tproxy-active-policy.py.
  Strict guest root/arch/hostname/no-default-route/binary-SHA checks,
  original 12 global sysctl snapshot under external durable guard,
  and exact kernel route/netfilter baseline capture.
- Real guest iptables and iproute2 were available. Initial unprivileged
  staging preflight ended before any Core8 run because iptables lives
  under /usr/sbin absent from the normal user's PATH. Verified that
  root can run it, kernel route and markers remained clean. Actual
  acceptance then ran under guest root as required.
- Guest before: only ip rules priorities 0/32766/32767, no TPROXY table
  30001 or CSQTT_CORE_TPROXY markers. The test handles iproute2's
  expected nonzero status for a nonexistent table 30001.

## Actual real ARM64 activation

- Launched real patched Rust Core8 on guest UDP 127.0.0.1:46881 and
  guest-only HTTPS port 46882 with random ephemeral web/main passwords.
  Local SOCKS5 mock bound exclusively to 127.0.0.1:46781, answered
  a real SOCKS greeting and UDP ASSOCIATE with a private UDP relay
  socket; no public traffic and no VK credentials.
- Authorized the localhost Core8 HTTPS API with its temporary x-api-key.
  Created a SOCKS5 profile then POST /api/local-proxy/activate/{id}
  returned activated=true. Readback route_active=true. Core8 log:
  SOCKS5 route ready on 127.0.0.1:46781 via TPROXY port 10667.
- Real guest kernel policy inspection DURING active route:
  (a) exactly 1 fwmark ip -4 rule using table 30001,
  (b) exactly 2 iptables mangle PREROUTING CSQTT_CORE_TPROXY
      interception entries, TCP and UDP,
  (c) exactly 1 iptables filter INPUT CSQTT_CORE_TPROXY ACCEPT entry,
  (d) ip -4 route show table 30001:
      local default dev lo scope host,
  (e) no guest IPv4 default route added in the main table.
- Core8 startup changed net.ipv4.ip_forward from 0 to 1 as expected.
  When active TPROXY policy was installed, the 12 global sysctl
  parameter snapshot DID NOT change again. This is real activation
  of the patched TPROXY readiness-only kernel ownership path.

## Deactivation and recovery

- POST /api/local-proxy/deactivate returned deactivated=true,
  readback route_active=false. Entire 4-section kernel policy
  snapshot matched baseline: TPROXY_GUEST_POLICY_RESTORED=True.
- Patched Core8 then exited normally with status 0 after SIGTERM.
  BEFORE external guard restoration, actual sysctls compared equal
  to initial baseline: TPROXY_GUEST_RUST_SYSCTL_RESTORED=True.
  External guard ran recovery and returned 0:
  CORE8_STOP_RECOVERY=RESTORED_AND_VERIFIED.
  TPROXY_GUEST_POST_GUARD_POLICY_RESTORED=True and
  TPROXY_GUEST_POST_GUARD_SYSCTL_RESTORED=True.
- Independent guest read-only final state: no fwmark rule or table30001
  route, mangle PREROUTING and filter INPUT only default ACCEPT policy,
  iptables-save no CSQTT_CORE_TPROXY markers, ip_forward=0,
  csqtt-core0 TUN absent, private test ports closed, no pending sysctl
  recovery journal, original guest ELF hash unchanged.
- Guest temp config/profile/credentials and copied experimental ELF/test
  script removed; the standalone patched ELF remains saved ONLY on
  Moscow VPS in separate scratch build directory. Guest gracefully
  powered off; QEMU console Power down; QEMU process/pid and SSH
  127.0.0.1:22379 absent, qcow2 disk preserved. Other CSQTT Deploy
  and HydraRoute Core VMs remained running; panel tunnel active,
  host route unchanged.

## Not yet proven

- No actual user TCP/UDP packet was injected through CSQTT TUN and
  observed at the SOCKS5 relay. The mock served control-plane SOCKS5
  UDP ASSOCIATE and this acceptance proves actual Linux TPROXY policy
  installation, activation, and rollback, NOT data-plane delivery.
- No authorized public VK Calls TURN -> WIRE3 -> Go SOCKS5 -> sing-box
  session; no public UDP stand provisioned.
- No real wrapper/Core8 SIGKILL or power loss recovery test; previously
  blocked test not retried. No production deployment or promotion.
