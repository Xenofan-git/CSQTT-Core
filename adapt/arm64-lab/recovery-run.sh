#!/usr/bin/env bash
# Isolated ARM64 VM only; caller provisions csqtt-lab netns and TPROXY rules.
set -u
lab=${CSQTT_LAB_DIR:-/tmp/csqtt-recovery}
cycles=${CSQTT_OUTAGE_CYCLES:-2}
outage_seconds=${CSQTT_OUTAGE_SECONDS:-25}
client_seconds=${CSQTT_RECOVERY_DURATION:-160}
mkdir -p "$lab"
rm -f "$lab/result.json" "$lab/exit" "$lab/resources.log"
sudo -n rm -f /tmp/csqtt-soak-client.verdict
relay=; core=; client=; monitor=
cleanup() {
  local p
  for p in "$client" "$core" "$relay" "$monitor"; do
    if [[ -n "$p" ]]; then kill "$p" 2>/dev/null || true; fi
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM
start_relay() {
  python3 /tmp/csqtt-soak-relay.py >"$lab/relay-$1.log" 2>&1 & relay=$!
  sleep 1
  kill -0 "$relay" 2>/dev/null || { echo 'SOCKS5 relay failed to start'; return 1; }
}
echo before >"$lab/phase"
start_relay before || exit 1
sudo -n /tmp/csqtt-tproxy-test --ignored --exact tproxy::tests::privileged_arm64_mixed_tcp_udp_soak --nocapture >"$lab/core.log" 2>&1 & core=$!
(
  while kill -0 "$core" 2>/dev/null; do
    pid=$(pgrep -f '^/tmp/csqtt-tproxy-test --ignored --exact tproxy::tests::privileged_arm64_mixed_tcp_udp_soak' | head -1)
    if [[ -n "$pid" ]]; then
      rss=$(awk '/VmRSS:/ {print $2}' "/proc/$pid/status" 2>/dev/null)
      fd=$(sudo -n sh -c "ls /proc/$pid/fd 2>/dev/null | wc -l")
      printf '%s rss_kb=%s fds=%s\n' "$(date -u +%FT%TZ)" "$rss" "$fd" >>"$lab/resources.log"
    fi
    sleep 5
  done
) & monitor=$!
sleep 2
CSQTT_LAB_DIR="$lab" CSQTT_RECOVERY_DURATION="$client_seconds" sudo -n --preserve-env=CSQTT_LAB_DIR,CSQTT_RECOVERY_DURATION ip netns exec csqtt-lab python3 -u /tmp/recovery-client.py >"$lab/client.log" 2>&1 & client=$!
sleep 20
for ((i=1; i<=cycles; i++)); do
  echo "outage_$i" >"$lab/phase"
  kill -9 "$relay" 2>/dev/null || true
  wait "$relay" 2>/dev/null || true
  sleep "$outage_seconds"
  start_relay "after_$i" || exit 1
  echo "after_$i" >"$lab/phase"
  if ((i<cycles)); then sleep 25; fi
done
wait "$client"; client_rc=$?
if [[ "$client_rc" -eq 0 ]]; then echo PASS | sudo -n tee /tmp/csqtt-soak-client.verdict >/dev/null; else echo FAIL | sudo -n tee /tmp/csqtt-soak-client.verdict >/dev/null; fi
wait "$core"; core_rc=$?
printf 'CLIENT_EXIT=%s CORE_EXIT=%s\n' "$client_rc" "$core_rc" >"$lab/exit"
[[ "$client_rc" -eq 0 && "$core_rc" -eq 0 ]]
