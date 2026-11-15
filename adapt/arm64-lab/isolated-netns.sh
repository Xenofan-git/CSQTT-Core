#!/usr/bin/env bash
# Run ONLY on disposable ARM64 test VM. Rootless SSH user with passwordless sudo.
set -euo pipefail
namespace=csqtt-lab
iface=csqtt-lab-host
cleanup() {
  sudo -n iptables -t mangle -D PREROUTING -i "$iface" -p tcp -j TPROXY --on-port 47997 --tproxy-mark 0x77/0xff 2>/dev/null || true
  sudo -n iptables -t mangle -D PREROUTING -i "$iface" -p udp -j TPROXY --on-port 47997 --tproxy-mark 0x77/0xff 2>/dev/null || true
  sudo -n ip rule del fwmark 0x77/0xff lookup 177 priority 177 2>/dev/null || true
  sudo -n ip route flush table 177 2>/dev/null || true
  sudo -n ip netns del "$namespace" 2>/dev/null || true
  sudo -n ip link del "$iface" 2>/dev/null || true
}
if [[ ${1:-} == --cleanup ]]; then cleanup; exit 0; fi
[[ ${1:-} == --run ]] || { echo 'Usage: isolated-netns.sh --run /tmp/recovery-run.sh'; exit 2; }
shift
[[ $# -gt 0 ]] || exit 2
[[ $(uname -m) == aarch64 ]] || { echo 'Only isolated ARM64 VM supported'; exit 2; }
[[ ! -e /sys/class/net/$iface ]] || { echo 'Test interface already exists'; exit 2; }
trap cleanup EXIT INT TERM
sudo -n ip netns add "$namespace"
sudo -n ip link add "$iface" type veth peer name csqtt-lab-peer
sudo -n ip link set csqtt-lab-peer netns "$namespace"
sudo -n ip addr add 172.29.79.1/30 dev "$iface"
sudo -n ip link set "$iface" up
sudo -n ip netns exec "$namespace" ip addr add 172.29.79.2/30 dev csqtt-lab-peer
sudo -n ip netns exec "$namespace" ip link set csqtt-lab-peer up
sudo -n ip netns exec "$namespace" ip link set lo up
sudo -n ip netns exec "$namespace" ip route add default via 172.29.79.1
sudo -n ip rule add fwmark 0x77/0xff lookup 177 priority 177
sudo -n ip route add local 0.0.0.0/0 dev lo table 177
sudo -n iptables -t mangle -A PREROUTING -i "$iface" -p tcp -j TPROXY --on-port 47997 --tproxy-mark 0x77/0xff
sudo -n iptables -t mangle -A PREROUTING -i "$iface" -p udp -j TPROXY --on-port 47997 --tproxy-mark 0x77/0xff
"$@"
