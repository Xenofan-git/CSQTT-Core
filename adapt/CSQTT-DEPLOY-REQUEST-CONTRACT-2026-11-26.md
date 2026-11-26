# DeployManager: request contract, 2026-11-26

Parent d922a16ff2c8c56f6af118cc2c9faaea6e9215ee. Tailnet connector session terminated; Desktop Commander reported both VPS offline. Changes prepared from GitHub source, locally tested extracted new functions with the matching Request type (Go 1.23.2 linux/amd64; race and vet passed, ARM64 test binary cross-compiled only). Full manager build and new integration assertion not yet executed. No production package or helper hash changed.

- Reject ambiguous or oversized request JSON, unknown/duplicate fields and trailing values, with generic errors that do not include credential values.
- Retain controller's listenPort as compatibility-only data; no port behavior changes.
- Reject uninstall directly in inventory-only helper before asset validation.
- Canonicalize known inventory fields and reject unexpected fields and unsupported interface ownership, matching the panel's display contract.
- Existing remote inventory operation is unchanged. No remote file transfer or installer execution implemented or attempted; simulation remains separate.

Amsterdam journals unavailable during this turn; this repo-local note records changes without claiming journal updates. Full live lifecycle remains incomplete.
