#!/usr/bin/env bash
# Reproducible build of the upstream CSQTT 2.5 server, not a production installer.
set -euo pipefail
: "${CSQTT_250_UPSTREAM:?Point to the extracted original CSQTT 2.5.0 source}"
: "${CSQTT_250_TARGET_DIR:?Point to a dedicated writable Cargo target directory}"
: "${CARGO_HOME:?Set the isolated cargo home with Rust 1.97.1 installed}"
command -v aarch64-linux-gnu-gcc >/dev/null
command -v aarch64-linux-gnu-g++ >/dev/null
export CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_LINKER=aarch64-linux-gnu-gcc
export CC_aarch64_unknown_linux_gnu=aarch64-linux-gnu-gcc
export CXX_aarch64_unknown_linux_gnu=aarch64-linux-gnu-g++
export AR_aarch64_unknown_linux_gnu=aarch64-linux-gnu-ar
export CARGO_TARGET_DIR="$CSQTT_250_TARGET_DIR"
export CARGO_BUILD_JOBS="${CARGO_BUILD_JOBS:-2}"
cd "$CSQTT_250_UPSTREAM/rust-server"
"$CARGO_HOME/bin/cargo" +1.97.1 build --locked --release --target aarch64-unknown-linux-gnu
binary="$CARGO_TARGET_DIR/aarch64-unknown-linux-gnu/release/csqtt"
file "$binary"
sha256sum "$binary"
[[ "$(od -An -tu2 -j18 -N2 "$binary" | tr -d ' ')" == 183 ]]
