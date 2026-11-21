# CSQTT Core 2.5.0 WIRE-3 — ARM64 release-candidate acceptance (2026-11-21)

## Scope and protection

This is a **tested candidate**, **not** a production deployment or a release promotion. The existing WIRE-4 branch and active NanoPi M-UI/HRCore/Tailscale remain untouched. No system-wide default route, real-host rp_filter, real-host nftables, or DNS modifications have been made as part of the work.

The release-candidate branch is release/csqtt-250-wire3-candidate-20261121. Its source is the original opt-in Go supervisor candidate ed7516a, with one isolated Go Linux TUN raw-fd generator fix. The original CSQTT v2.5.0 Rust client is pinned; the Core8 server uses the matching WIRE-3 revision.

## Binary provenance

| Component | Evidence |
|---|---|
| Original Rust CSQTT 2.5.0 ARM64 client | original source commit fd936ef087c34cc46f26111dc2361f43b8b03c07; SHA-256 3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa |
| Derived Core8 ARM64 server | source commit 91b154011ca16c219f57eef99462d3408dccfeeb; WIRE-3; SHA-256 a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706 |
| sing-box | v1.14.4 ARM64, verified pin |
| Go supervisor | candidate binary produced by CI 44912292825 from source generation including adapt/apply-csqtt-core-linux-tun-rawfd.py |

The experimental bundle contains exactly four ARM64 binaries and a JSON manifest. Its status remains experimental-not-approved-for-production.

## Proven tests

- CI 44891239516: true original Rust 2.5 client ↔ Core8 WIRE-3 session/turn/icmp — PASS; separate Rust-pair acceptance.
- CI 44894005565: real original Rust 2.5 ARM64 ELF launched via opted-in Go supervisor, secrets passed via environment — PASS.
- CI 44907818083: one native isolated ARM64 WIRE-3 chain with Go, test-built original-source Rust client and synthetic local TURN fixture, Core8, Go TUN, SOCKS5, sing-box, HTTP and UDP echo — PASS. It does not use the unchanged release ELF and must not be misrepresented as such.
- CI 44912292825: updated Go TUN native nonblocking raw-fd fix; Go unit, TUN stop/reconnect lifecycle and race checks; exact original-client/Core8/sing-box ELF hashes; four-component ARM64 candidate bundle — PASS.
- CI 44912627426: updated four-component bundle systemd activation, forced failed update and atomic rollback, private Go HTTP and sing-box DIRECT SOCKS smoke — PASS.
- CI 44912386606: isolated A/B demonstrated rp_filter=2 preserved with src_valid_mark and owner-scoped packet marking in the disposable client namespace, with full WIRE-3 TCP/UDP chain — PASS. No production sysctl or nft rules were changed.

## Confirmed root causes and limits

1. Go os.File.Read on Linux /dev/net/tun returned "not pollable" before processing packets. The tested release-candidate generator uses unix.SetNonblock and unix.Read/unix.Write with bounded unix.Poll, including EINTR/EAGAIN handling. The working WIRE-4 version is not changed.
2. In the intentionally route-less test namespace, a loose rp_filter without a main reverse route discarded otherwise valid WIRE-3 SYN+ACK packets. Keeping rp_filter=2 and enabling *test-namespace-only* mark-aware reverse lookup fixed this. A production NanoPi already has an ordinary main default route via Keenetic; the test result **does not authorize disabling or changing production rp_filter, nft, or host routing without a separate need and ownership check**.
3. Full TCP/UDP synthetic TURN E2E demonstrates interoperability but not full byte-identical production client authentication via VK. Original release ELF and original Rust ↔ Core8 pair have separately passed their exact-binary tests.

## Remaining release gate

- **DONE — CI 44912824079 SUCCESS.** A joined native ARM64 Go→Rust 2.5→Core8 WIRE-3→TUN→Go SOCKS5→sing-box TCP/UDP test used the **exact candidate Go TUN source fix generator**, with only synthetic Rust TURN credentials and isolated test-only Go auth/Direct/DNS adaptations. The test fixture explicitly left the production TUN read/write source unchanged. Direct bypass negative check, HTTP marker, UDP echo via both SOCKS5 endpoints, component shutdown and unchanged host route all PASSED. See https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44912824079.
- If needed, make a narrow, fully owned, rollback-safe, host-independent reverse-path policy feature; do not add global route changes or disable rp_filter by default.
- Package the four **already validated binaries** with complete evidence manifest and hashes; do not recompile unchanged source or rerun already-green tests just for metadata. Deploy only after a separate explicit promotion decision.
- **Not yet tested:** production VK/TURN authentication using byte-identical original Rust release ELF and real user credentials as ONE full-chain live session. Its exact ELF startup/RTT pair and the original-source synthetic TURN fullchain have been tested separately. Do not describe test fixture traffic as a production-authentication acceptance.

All evidence is from isolated ARM64 CI; never infer real-world availability or production readiness from a single control-plane PASS.

## Final validated release-candidate archive (no production rollout)

- Packaging-only native ARM64 CI **44913844376 SUCCESS** on commit 1288b98c8b671332d8bd2bebb6f2bdc7a8e884fb.
- Input was the same already verified four-component bundle from Go release-candidate run44912292825; archive packaging did **not** recompile source or mutate executable bytes. ELF64 AArch64 metadata and per-file manifest SHA-256 were checked again; +x executable permission was restored after GitHub artifact extraction.
- Artifact name: csqtt-core-250-wire3-arm64-validated-rc-20261121, artifact ID 14710648589, 43,643,570 bytes, retention until 2026-12-21 14:25 UTC.
- ZIP file: csqtt-core-250-wire3-arm64-validated-rc.zip.
- Final ZIP SHA-256: 49b780ca5bba99ed9641c5bfe1650a23600922b5f341d705c0375bf1cbbf9906.
- Each binary remains byte-identical to the verified Go, original-client, Core8 and sing-box ELF used as input; bundled manifest, source/CI evidence and per-file SHA256SUMS were added. The output explicitly remains a validated **isolated candidate**, not a production-authorized upgrade.
- Release-candidate tests across 44912292825, 44912627426, 44912824079 and 44912386606 are all green. The original source-built TURN fixture E2E does not replace the still-unperformed production VK-authenticated byte-identical-client fullchain smoke.
- No active NanoPi service, route, DNS, firewall, packet-mark policy or sysctl was modified. Manual promotion decision remains separate from the completed artifact construction.

## Real VK Calls preflight — 2026-11-21

- Separate test branch `test/csqtt-250-original-live-vk-20261121`, commit 0a783f0. Native ARM64 GitHub Actions [44914600867](https://github.com/Xenofan-git/CSQTT-Core/actions/runs/44914600867) **SUCCESS**, running byte-identical original Rust CSQTT 2.5.0 ELF SHA-256 `3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa`.
- The existing GitHub Actions VK Calls hash secret was used in a disposable runner only; the actual hash and VK response payload were not printed or copied to any VPS or NanoPi. Exact output: `REAL_VK_ORIGINAL_250_STATUSES valid`, `REAL_VK_ORIGINAL_250_HASH_PREFLIGHT_PASSED`. This proves hash validation through the live VK Calls API (anonymous token + call preview) only.
- A temporary x86_64-hosted ARM64 QEMU guest was created on the Moscow development VPS to provide a separate native Core8 WIRE-3 test server. The Core8 ELF SHA-256 and protocol revision were verified in the guest. Public UDP test service launch was blocked by available access restrictions, so no remote Core8 WIRE-3 handshake was performed. Guest `csqtt-250-live-vk-lab.service` was explicitly stopped and UDP test port 48779 confirmed closed.
- A subsequent standalone original-client live TURN-credential acquisition experiment was also blocked by available execution restrictions. No workaround that exposes or exports tokens was used. **Real TURN credentials, real VK TURN relay, real Core8 TUNCONF, and a joined VK-authenticated end-to-end TCP/UDP session remain NOT VERIFIED.**
- Existing isolated synthetic-TURN TCP+UDP E2E, corrected Go TUN lifecycle/race and atomic rollback remain green and unchanged; the original ELF real VK hash validation is a new, narrower successful test. Do not promote this release candidate to production on the basis of hash validation alone.


## Byte-identical final ZIP verification via direct Desktop Commander — 2026-11-21

- After Desktop Commander connected directly to Moscow VPS msk-1-vm-vw4n, release branch and historical green tests were read back without modifying the running NanoPi.
- New independent offline-only verification (commit 430fabe1b0e78c0d808a2f5291742fda3e969a1b) reads the EXISTING validated ARM64 ZIP from GitHub Actions 44913844376 without rebuilding or repackaging any ELF: adapt/verify-csqtt-core-250-rc-archive.py.
- GitHub Actions 44918351752 SUCCESS; ZIP original SHA-256 49b780ca5bba99ed9641c5bfe1650a23600922b5f341d705c0375bf1cbbf9906 exactly matches, four pinned AArch64 ELF digests, role/size/manifest, SHA256SUMS, exact file inventory and non-production status all verified. No new candidate binary was produced.
- Six negative inventory tests pass: traversal, absolute names, unrecognized payload, ZIP symlink, duplicate names, and incomplete inventory.
- This is a supply-chain/release-archive integrity gate, NOT proof of live VK TURN authentication, external relay access, WIRE-3 TUNCONF, or TCP/UDP via real VK. Last real VK Calls hash preflight 44914600867 remains the only demonstrated external VK result. No migration/promotion.
