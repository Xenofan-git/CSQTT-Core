# Selective CSQTTR 2.5.3 Core8: repeat previously green Linux 2.5.0 WIRE-3 fullchain

Date: 2026-11-22. Experimental Linux ARM64 branch
test/csqtt-core-linux-unified-253-20261122.

## Test source, meaning and scope

Re-used the EXACT three test fixture source files and exact SHA256 hashes
from the previously successful native Linux ARM64 WIRE-3 fullchain
experiment, GitHub Actions run 44912824079 on pinned commit
4d4ac742e2d2afd3a702a7b4b411526a0009f622.

- inject-test-turn-only.py: SHA256
  14b3f70778883e2d91fadf172d64e8b33131307b05bd933692f7b97ad3a77cfa
- patch-test-go-client-hash.py: SHA256
  e48ebb730b5a1406a06c6681881e0f87c614bd80735154b02a38120ec9c22515
- run-fullchain-arm64.py: SHA256
  4e407bf6d2613ff38d074c908473f9da55d7f978e6110a70c83657c17d008c43

No changes were made to these three prior successful test fixtures.
Added a branch-only variant of the 161-line previously verified native
ARM64 GitHub Actions workflow. It differs only by:
1. Using the new final 4-component Linux experimental bundle artifact
   from GitHub run 45038262207, not the old 2.5.0 test server.
2. Enforcing new Core8 SHA256
   362a27927764e4e728b6327ce3b62a229c376ae3512fdce3359c4d3bd9b8017e,
   original pinned client SHA and source commit 4211ac5e3d1356699c84057f608035ae669e7cc7.
3. Including Sing-box transaction journal implementation in Go build,
   because the independently tested new manager requires this source.
4. Using native Go 1.26.10 on the disposable GitHub ARM64 runner.

The upstream original Rust 2.5.0 client is built in a test-only
configuration that substitutes ephemeral synthetic TURN credentials.
The production client ELF and real VK authentication remain
unchanged and are NOT asserted to be tested by this scenario.

The prior successful test verifies, in separate no-default-route
network namespaces with veth and local synthetic credentials,
Go HTTP connect -> original Rust client -> Core8 WIRE-3 ->
TUN -> Go SOCKS5 -> real sing-box TCP/UDP, including negative
Direct bypass, HTTP and UDP echo and cleanup. The new test
must produce independent results after replacing the server ELF.

No NanoPi, public TURN/VK, existing VPNs, host default route,
systemd service or external accounts are touched. Test-only
namespace sysctl changes are NOT production configuration advice.

## Already independently green before this fullchain

- Actual QEMU ARM64 Sing-box + Go SOCKS5 TCP, UDP, DNS and
  routing tests 10/10 PASS with full race suite PASS locally.
- Native GitHub ARM64 Sing-box real-loopback with Go race suite
  and new Core8 under no-default-route netns startup/restart,
  live UDP/HTTPS, TLS handshake, process cleanup PASS
  (CI run 45039312527).
- Exact experimental four-component bundle 45038262207 SUCCESS;
  initial source/Deploy/sing-box/native Core8 CI 45037804052 SUCCESS.

This fullchain remains a TEST until its separate CI result is
confirmed and recorded. No installation or release promotion.

## Completed selective 2.5.3 Core8 WIRE-3 synthetic TURN fullchain

GitHub Actions **45039689081 SUCCESS** on new server SHA256
362a27927764e4e728b6327ce3b62a229c376ae3512fdce3359c4d3bd9b8017e.
Actual native ARM64 CI flow markers:
- FULLCHAIN_ISOLATED_VETH_TWO_NETNS_NO_GLOBAL_DEFAULT_PASS
- FULLCHAIN_DIRECT_BYPASS_NEGATIVE_CONTROL_PASS
- FULLCHAIN_NATIVE_GO_SUPERVISOR_API_LIVE_PASS
- FULLCHAIN_WIRE3_TUNCONF_ACTIVE_WORKER_GO_TUN_AND_SOCKS_PASS
- FULLCHAIN_ISOLATED_GO_TUN_POLICY_202_AND_FWMARK_PASS
- FULLCHAIN_MARKED_ROUTE_TARGETS_REAL_GO_TUN_PASS
- FULLCHAIN_END_TO_END_GO_RUST250_CORE8_TUN_SOCKS_SINGBOX_TCP_PASS
- FULLCHAIN_GO_SOCKS5_UDP_WIRE3_RESULT 0
- FULLCHAIN_SINGBOX_GO_SOCKS5_UDP_WIRE3_RESULT 0
- FULLCHAIN_END_TO_END_GO_RUST250_CORE8_TUN_SOCKS_SINGBOX_UDP_PASS
- FULLCHAIN_STOP_AND_CLEANUP_PASS
- FULLCHAIN_HOST_DEFAULT_ROUTE_UNCHANGED_PASS

Original Rust 2.5 client is rebuilt with *test-only* synthetic
TURN fixture. This is NOT original release ELF with live VK TURN
credentials. Both guest namespaces lack default routes; the
running NanoPi and VPS routing were not changed.
