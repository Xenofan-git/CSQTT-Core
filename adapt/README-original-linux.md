# CSQTT Core — NanoPi ARM64 adaptation

This branch is based on the original Linux backend from Endlad2/LaLune.

Adaptations:
- Linux CSQTT core asset: client-linux-arm64.
- Linux routing no longer changes the main routing table.
- CSQTT Core uses isolated policy routing table 202 with rule priority 22020.
- Traffic sourced from 192.168.5.0/24 is routed through csqtt0.
- A connected route to 192.168.4.0/24 is kept in the CSQTT Core policy table so the client-side policy does not black-hole LAN access.
- Main NanoPi routing and existing table 200/201 mechanisms remain untouched.
- On disconnect, only the CSQTT Core rule/table are removed.

The adaptation is intentionally kept separate from the existing isolated gateway fallback.

Upstream:
https://github.com/Endlad2/LaLune
