# CSQTT Core deployment contract

The Deploy button uses the original CSQTT v2.1.9 deployment flow, derived from the pinned upstream `app/src/main/assets/deploy.sh`. The generated script is `server-assets/deploy-csqtt-lalune.sh`; it retains the upstream preflight, uploaded-binary validation, systemd activation, network checks, web-panel health check, and uninstall lifecycle. The raw upstream script is retained separately for provenance and is never run directly by the CSQTT Core Deploy button.

- Upstream source: [amurcanov/csqtt v2.1.9 deploy.sh](https://github.com/amurcanov/csqtt/blob/v2.1.9/app/src/main/assets/deploy.sh)
- Server binary: patched from the pinned CSQTT v2.1.9 source; ARM64 and x86_64 assets are bundled for the DeployManager.
- Binary: `/usr/local/bin/csqtt-lalune`
- Unit: `csqtt-lalune.service`
- Config/database: `/etc/csqtt-lalune`
- Helper/runtime/log paths: namespaced under `/usr/local/lib/csqtt-lalune`, `/run/csqtt-lalune`, and `/var/log/csqtt-lalune-install.log`
- UDP peer port: `47000`; WireGuard peer port: `47001`; HTTPS web port: `47002`; local SOCKS5 profile default: `47005`
- TUN/subnet and policy identifiers: `csqtt-lalune0`, `10.67.68.0/24`, policy tables `47001` and `47066`, marks `0x6741/0x6742`.

## Isolation changes to the upstream flow

The adaptation changes only the service-owned resource names, the CSQTT Core ports/subnet, and behavior that could disrupt an unrelated process:

- Namespaces systemd units, executable/library/config/runtime/log/certificate-helper paths, Docker names, sysctl filenames, firewall comments, and legacy helper process names.
- Injects `CSQTT_SYSTEMD_UNIT=csqtt-lalune.service` so panel-triggered restart actions cannot target `csqtt.service`.
- Uses a unique SSH staging directory and protected one-shot credentials; the DeployManager selects the server binary for the remote architecture.
- Checks UDP/47000 and TCP/47002 before cutover. If a listener is unrelated or its owner cannot be identified, deployment fails without terminating it.
- Leaves generic legacy proxy interfaces alone. Firewall cleanup matches only CSQTT Core-owned markers.
- Keeps the upstream server's persistent database/configuration on uninstall, while removing only the namespaced runtime artifacts.

The standalone `install-csqtt-lalune.sh` / `uninstall-csqtt-lalune.sh` files are legacy regression fixtures only; the Deploy button does not invoke them.

**Production deployment remains gated.** The required proof is a green CI run of the actual Deploy API → SSH → original namespaced `deploy.sh` install and uninstall against a disposable ARM64 system VM, including authenticated protocol/TUN checks and preservation of pre-existing CSQTT services, routes, and firewall rules. No production NanoPi deployment is implied or authorized by these tests.
