# CSQTT Linux runtime primitives

This directory contains Linux-specific runtime code written for the CSQTT 2.5.0 port.

The `socks5` package is the local CSQTT egress adapter consumed by sing-box:
- binds only to an explicit loopback IP;
- supports SOCKS5 CONNECT and UDP ASSOCIATE;
- applies Linux `SO_MARK` to outbound TCP, UDP and DNS sockets;
- keeps UDP associations alive for the lifetime of the SOCKS control connection;
- rejects non-loopback exposure and does not create routes or change system DNS.

The package does not configure the TUN device or policy-routing table. Those belong to the separate CSQTT process supervisor and will be implemented/tested as a distinct lifecycle component. No LaLune Go/Wails/Flutter/TUN/SOCKS code is used here.

Run tests from this directory with:

```sh
go test -race ./...
go vet ./...
```
