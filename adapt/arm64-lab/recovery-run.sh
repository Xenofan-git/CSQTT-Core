#!/usr/bin/env bash
# Run INSIDE the disposable ARM64 VM. Requires preconfigured csqtt-lab netns,
# TPROXY redirection and an isolated mock relay. Never run on production.
set -u
lab=/tmp/csqtt-recovery
mkdir -p "$lab"
echo before >"$lab/phase"
rm -f "$lab/result" "$lab/exit"
sudo -n rm -f /tmp/csqtt-soak-client.verdict
python3 /tmp/csqtt-soak-relay.py >"$lab/relay-before.log" 2>&1 & relay=$!
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
CSQTT_RECOVERY_DURATION=160 sudo -n ip netns exec csqtt-lab python3 -u /tmp/csqtt-recovery-client.py >"$lab/client.log" 2>&1 & client=$!
sleep 25
echo outage >"$lab/phase"
kill -9 "$relay" 2>/dev/null || true
sleep 45
python3 /tmp/csqtt-soak-relay.py >"$lab/relay-after.log" 2>&1 & relay=$!
echo after >"$lab/phase"
wait "$client"; client_rc=$?
if [[ "$client_rc" -eq 0 ]]; then echo PASS | sudo -n tee /tmp/csqtt-soak-client.verdict >/dev/null; else echo FAIL | sudo -n tee /tmp/csqtt-soak-client.verdict >/dev/null; fi
wait "$core"; core_rc=$?
kill "$relay" "$monitor" 2>/dev/null || true
printf 'CLIENT_EXIT=%s CORE_EXIT=%s\n' "$client_rc" "$core_rc" >"$lab/exit"
