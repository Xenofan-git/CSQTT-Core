# Selective CSQTTR v2.5.3 Android SSH key confirmation — CSQTT Core

Date: 2026-11-22. Experimental source adapter only.

## Exact source comparison

Original Afteroid/csqttr tag v2.5.0:
fd936ef087c34cc46f26111dc2361f43b8b03c07.
Later tag v2.5.3:
84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806.

Actual Kotlin sources compared:
app/src/main/java/com/csqtt/client/SshHostKeyStore.kt;
app/src/main/java/com/csqtt/client/ui/DeployTab.kt;
and 2.5.3 UI confirmation dialog.

v2.5.0 ALREADY supported arbitrary SSH port, first-use SHA256
fingerprint pinning and refusing changed keys, so these were NOT
copied a second time. The missing v2.5.3 feature is a user-visible
old/new fingerprint comparison with deliberate trust of exactly the
presented new SSH server key.

## Deliberately narrow experimental patch

- Strict checksum-pinned original Kotlin 2.5.0 files, with unique
  anchors and no repeated application.
- Only SshHostKeyStore.kt and ui/DeployTab.kt changed.
- The existing TofuVerifier still rejects every mismatched key.
- The warning shows both SHA256 fingerprints, host and SSH port,
  and explains independent verification before accepting.
- The confirmation updates the saved fingerprint only when its
  current value still equals the fingerprint originally shown as
  old (prevents trusting an obsolete warning).
- Cancel does not change stored keys; no automatic retry, install,
  uninstall or remote action after trust.
- Installation/uninstallation failure and exception handlers check
  for the SSH mismatch and show an existing-theme Material3 dialog.
  Original layout, brand and tab design remain unchanged.
- Different profiles and hosts use their own pending UI state;
  matching host:port is required when consuming a pending mismatch.
- Previous TOFU first-connection behavior is preserved.

Exact upstream input SHA256:
HostKeyStore: 63134110bf978e1e2b090bf30480681096da447b2c87831e736cf338e6b5f004
DeployTab: f5a1ebb5f7dc3fa4e7ab294a92285095b9a9b3dc19f0cea227a23a7bc12d7e61

Derived output SHA256 against real pinned original Kotlin:
HostKeyStore: ce9b4c3a2bbdb348b3f8b5f89a270c2eaaeba364783db3a8f975b971bccd46bc
DeployTab: 88cad066b94110f139a1a8774134a663bbc528e9d8e8e1d5da9ceee557a03748

## Validation and release gates

- Original pinned Kotlin extracted in private Moscow scratch at
  /srv/development/isolated-checkouts/.csqttr-253-android-hostkey-250-20261122.
  Applied patch to this scratch only, with recorded derived hashes.
- Pure source-contract tests verify refusal, explicit confirmation,
  stale trusted-key protection, four error paths, no auto-retry,
  double application rejection and missing/duplicate anchor refusal.
- Moscow host has no Kotlin compiler or Android SDK available.
  Therefore no compiled APK, Kotlin integration, Compose UI test,
  real SSH host-key rotation or mobile-device test was completed.
  Green source-only GitHub CI does NOT mean app acceptance.
- Actual integration into the CSQTT Core Android pipeline and
  Kotlin compile/device tests remain REQUIRED release gates.
  Windows/Linux Desktop host-key handling needs independent audit.
- No real VPS, NanoPi, TUN, ports, credentials, panel,
  release candidate or installed mobile application changed.
- Original release candidate HEAD:
  236e7b01446d0f01a58b9ad2d86bee093f5e1fe0.
