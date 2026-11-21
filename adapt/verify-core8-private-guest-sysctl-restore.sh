#!/usr/bin/env bash
# One-time offline ARM64 Core8 smoke. No host, router or public UDP changes.
set -euo pipefail
umask 077
[ "$(id -u)" = 0 ] || { echo GUEST_ROOT_REQUIRED; exit 2; }
[ "$(hostname)" = csqtt-core8-isolated ] || { echo WRONG_ISOLATED_GUEST; exit 2; }
[ "$(uname -m)" = aarch64 ] || { echo WRONG_ARCH; exit 2; }
[ -z "$(ip -4 route show default)" ] || { echo GUEST_HAS_DEFAULT_ROUTE; exit 2; }
[ "$(sha256sum /home/csqttlab/core8-arm64 | awk '{print $1}')" = a131751f29a5373d3ebf08bc0d3bfe59988a8badaf2854f01eb5561fc903a706 ] || { echo WRONG_CORE8_HASH; exit 2; }
[ "$(/home/csqttlab/core8-arm64 --protocol-revision)" = CSQTT-WIRE-3 ] || { echo WRONG_WIRE_REVISION; exit 2; }
# Before launching, reject existing sockets rather than claiming another daemon as ours.
if ss -H -lun | grep -Fq "127.0.0.1:46871"; then echo GUEST_UDP_PORT_BUSY; exit 2; fi
if ss -H -ltn | grep -Fq ":46872"; then echo GUEST_WEB_PORT_BUSY; exit 2; fi
tmp="$(mktemp -d /tmp/core8-guest-private.XXXXXXXX)"
paths="$tmp/original-sysctls"
: > "$paths"
for p in /proc/sys/net/ipv4/ip_forward /proc/sys/net/ipv6/conf/all/forwarding \
 /proc/sys/net/ipv4/conf/all/rp_filter /proc/sys/net/ipv4/conf/default/rp_filter \
 /proc/sys/net/core/default_qdisc /proc/sys/net/ipv4/tcp_congestion_control \
 /proc/sys/net/ipv4/tcp_sack /proc/sys/net/ipv4/tcp_dsack \
 /proc/sys/net/ipv4/tcp_reordering /proc/sys/net/ipv4/tcp_window_scaling \
 /proc/sys/net/ipv4/tcp_fastopen /proc/sys/net/ipv4/tcp_slow_start_after_idle
do
 if [ -r "$p" ] && [ -w "$p" ] && [ ! -L "$p" ]; then printf '%s|%s\n' "$p" "$(cat "$p")" >> "$paths"; fi
done
if [ ! -s "$paths" ]; then rm -rf "$tmp";echo GUEST_SYSCTL_SNAPSHOT_EMPTY;exit 2;fi
pid=''
cleanup() {
 local initial_status=$? err=0
 trap - EXIT
 if [ -n "$pid" ]; then kill -INT "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
 while IFS='|' read -r path original; do
  if [ -e "$path" ] && [ ! -L "$path" ]; then
   printf '%s\n' "$original" >"$path" 2>/dev/null || err=1
  else
   err=1
  fi
 done < "$paths"
 while IFS='|' read -r path original; do
  if [ ! -e "$path" ] || [ -L "$path" ]; then
   err=1
  elif [ "$(cat "$path")" != "$original" ]; then
   err=1
  fi
 done < "$paths"
 rm -rf "$tmp"
 if [ "$err" -eq 0 ]; then echo GUEST_SYSCTL_SNAPSHOT_RESTORE_PASS;else echo GUEST_SYSCTL_SNAPSHOT_RESTORE_FAIL;initial_status=1;fi
 exit "$initial_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
pass="$(openssl rand -hex 20)"
webpass="$(openssl rand -hex 20)"
env CSQTT_MAIN_PASSWORD="$pass" CSQTT_WEB_PASS="$webpass" timeout -s INT -k 8 80 \
 /home/csqttlab/core8-arm64 --listen 127.0.0.1:46871 --web-port 46872 \
 --config-dir "$tmp/config" > "$tmp/private.log" 2>&1 &
pid="$!"
udp=0
web=0
for _ in $(seq 1 35); do
 if ss -H -lun | grep -F '127.0.0.1:46871' >/dev/null; then udp=1;fi
 if ss -H -ltn | grep -F ':46872' >/dev/null; then web=1;fi
 if [ "$udp" = 1 ] && [ "$web" = 1 ]; then break;fi
 if ! kill -0 "$pid" 2>/dev/null;then break;fi
 sleep 2
done
echo "GUEST_CORE8_PRIVATE_UDP_BIND=$udp"
echo "GUEST_CORE8_PRIVATE_WEB_BIND=$web"
[ "$udp" = 1 ] && [ "$web" = 1 ] && kill -0 "$pid" 2>/dev/null || { echo GUEST_CORE8_SMOKE_FAILED; exit 1; }
[ -z "$(ip -4 route show default)" ] || { echo GUEST_DEFAULT_ROUTE_UNEXPECTED;exit 1; }
kill -INT "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true
pid=''
sleep 2
if ss -H -lun | grep -F '127.0.0.1:46871' >/dev/null;then echo GUEST_UDP_STOP_FAILED;exit 1;fi
echo GUEST_CORE8_PRIVATE_SHUTDOWN_PASS
