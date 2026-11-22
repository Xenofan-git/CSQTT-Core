# CSQTT Core Linux ARM64 — rollback on failed Sing-box config update

Date: 2026-11-22. Experimental branch: test/csqtt-core-singbox-config-rollback-20261122.

## Verified defect

The original SingBoxManager.SaveConfig committed the generated config
and SHA256-bound panel sidecar with two separate renames. If the first
rename succeeded and the second failed, the new active config no longer
matched the old panel state, and the next LoadConfig/Start rejected it.

## Scope of Linux-only change

- Prepare an fsynced private recovery snapshot of existing config before
  publishing a replacement. Reject symlinks or nonregular old config.
- Always use mode 0600 for backup even if old config was permissive.
- On panel-sidecar rename failure atomically restore previous config;
  on first-time update remove new config instead.
- On double failure leave a private recovery file and report its path in
  SingBoxManager.Status.Error instead of claiming success.
- Preserve previously disabled routes, disabled SOCKS5 profiles and saved
  credentials on rollback; clean temporary files on success.
- No Android/APK, TUN, default route, service or other network modification.
- Power-loss between the two rename calls is NOT yet covered by crash
  recovery and requires independent testing before production approval.

## Verified tests

- Six new real Go tests: existing-pair rollback, first install failure,
  first rename failure, failed rollback recovery snapshot, symlink
  rejection, successful commit cleanup.
- All existing Linux Sing-box plus SOCKS5 component tests PASS with native
  Go 1.26.10 and with go test -race.
- Compiled an AArch64 static Linux test ELF and ran all six new
  regression tests under qemu-aarch64: PASS.
- No active NanoPi/CSQTT service or other running VM touched.

The unchanged Linux release candidate base is
236e7b01446d0f01a58b9ad2d86bee093f5e1fe0.
