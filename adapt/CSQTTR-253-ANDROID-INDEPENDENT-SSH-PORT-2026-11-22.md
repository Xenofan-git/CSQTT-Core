# CSQTT Core: selective Android SSH-port decoupling from CSQTTR 2.5.3

Date: 2026-11-22. Experimental, not production and not a full 2.5.3 upgrade.

## Verified source comparison

Afteroid/csqttr v2.5.0 tag
fd936ef087c34cc46f26111dc2361f43b8b03c07
already had an SSH-port setting, but it was only visible
inside the global 'Manual ports' switch, and the original
resolveServerPorts(false, sshPort, ...) returned
DEFAULT_SSH_PORT=22 regardless of the saved value. Switching
manual ports OFF also reset sshPort to 22. Thus a remote VPS
configured with nonstandard SSH port could not be used together
with default CSQTT peer/web ports.

Actual 2.5.3 ui/legacy/DeployTab.kt, DeployScreen.kt fixed this
by always validating the SSH port and keeping its control outside
the manual peer/web port fields. This is the only subset ported.

## Conservative changes to the already-approved v2.5.0 UI

Second strict source adapter
adapt/patch-csqttr-253-android-independent-ssh-port-250.py,
run AFTER the prior SSH host-key confirmation adapter:

- Validate SSH port on Install and Uninstall irrespective of
  whether manually configured peer/web ports are enabled.
- Return the selected valid SSH port even when peer/web ports
  use defaults; keep unchanged original 46000/46002 defaults.
- Turning the 'Manual ports' peer/web switch OFF no longer
  discards the SSH port saved for the VPS; peer/web are reset
  to default as before.
- Show a standalone existing-theme DeployTextField for SSH port
  in BOTH SSH-password and SSH-key authentication modes;
  never show duplicate SSH field in manual peer/web controls.
- Keep per-profile SettingsStore as-is; 2.5.0 ALREADY scoped
  DEPLOY_SSH_PORT with getProfileKey(DEPLOY_SSH_PORT, profile).
- No changes to protocol WIRE-3, Go SOCKS5, sing-box, remote
  host credentials, server installer, TUN, default route,
  original app branding, or unrelated profile settings.

Source pins: upstream original DeployScreen.kt SHA256
e638a850d2bf0b1a04d3601307451e35243cfcbd58831335131d8bf3daceaa14,
and post-first-host-key-patch DeployTab.kt SHA256
88cad066b94110f139a1a8774134a663bbc528e9d8e8e1d5da9ceee557a03748.
Combined derived DeployTab.kt SHA256
e75b35a9eab728f614c12a80c1bccdcad8879a58c8e0b8ad68ea5a02e90289d8;
DeployScreen.kt SHA256
444a6d4117e75f9a35d73e1d483e63130e8165d059013e057a1d852327620f0b.
Prior derived SshHostKeyStore.kt SHA256 unchanged
ce9b4c3a2bbdb348b3f8b5f89a270c2eaaeba364783db3a8f975b971bccd46bc.

## Actual tests

1. Extracted exactly 3 original 2.5.0 Kotlin source files
   into separate isolated Moscow scratch and verified SHA256.
2. Applied first host-key confirmation patch, tested second
   independent SSH-port patch anchors against first output,
   applied second patch, checked all three output SHA digests.
3. Source-contract test
   adapt/test-csqttr-253-android-independent-ssh-port-offline.py
   PASS 5 groups: pinned exact source; output SHA; preserve
   independent valid SSH in both manual states; preserve SSH
   when peer/web switch toggles; visible field in key/password
   layouts; fails closed on duplicate/missing anchors.
4. The dedicated Kotlin/Compose CI workflow checks out fixed
   original 2.5.0 and applies both patches before compilation.
   Its run status must be verified separately. No device UI
   screenshots or host-key rotation integration verified here.

## Profile migration deferred, not silently copied

Actual CSQTTR 2.5.3 contains broad independent
DeployScopedDataStore, ReadOnlyProfileDataStore, profile slot
allocation and saveDeployProfileDraft which links a server to a
connection profile, writes credentials, and updates revisions.
It is NOT equivalent to a one-line safe correction, and risks
overwriting different profiles' settings in our seven-tab
hybrid. Existing original CSQTT 2.5.0 already has per-profile
connection/deploy secrets and saved custom SSH port. Do not
import this broad data model until client-side profile mapping
and migration tests exist. Do not replace approved UI with V2.
