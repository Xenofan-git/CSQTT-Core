#!/usr/bin/env python3
"""Strict opt-in non-mutating CSQTT Core inventory, inspired by 2.5.3.

Adds only readonly inventory action to existing derived 2.5.0 script.
Never reads credentials or prints complete process argv; does not install.
"""
import argparse
from pathlib import Path

def shell(s):
    return s.replace('@{', '$' + '{')

def once(text, old, new, title):
    old,new=shell(old),shell(new)
    if text.count(old)!=1:
        raise ValueError(f"{title}: expected unique source marker, got {text.count(old)}")
    return text.replace(old,new,1)

INVENTORY=r'''
# CSQTTR 2.5.3 selective read-only server inspection.
# Only emit the end marker when every required facility is available.
# Paths and application credentials are never dumped.
inventory_emit() {
    local type="$1" field
    shift
    printf 'CSQTT_INV|%s' "$type"
    for field in "$@"; do
        field="$(printf '%s' "$field" | tr '\n|' '  ')"
        printf '|%s' "$field"
    done
    printf '\n'
}

inventory_port() {
    local proto="$1" port="$2" listeners="" pids="" pid seen_core=0 seen_foreign=0 owner=""
    case "$proto" in
        udp)
            if ! listeners="$(ss -H -lunp "sport = :$port" 2>/dev/null)"; then
                inventory_emit error reason=udp_socket_query_failed
                return 20
            fi ;;
        tcp)
            if ! listeners="$(ss -H -ltnp "sport = :$port" 2>/dev/null)"; then
                inventory_emit error reason=tcp_socket_query_failed
                return 20
            fi ;;
        *) return 20 ;;
    esac
    # Unlike the legacy convenience helpers, inventory must NOT swallow
    # socket query failures as "port is free".
    pids="$(printf '%s\n' "$listeners" | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -un || true)"
    if [ -z "$listeners" ]; then
        inventory_emit port "proto=$proto" "port=$port" state=free owner=none
        return 0
    fi
    if [ -z "$pids" ]; then
        inventory_emit port "proto=$proto" "port=$port" state=busy owner=unknown
        return 0
    fi
    while IFS= read -r pid; do
        [ -n "$pid" ] || continue
        if csqtt_process_is_owned "$pid"; then seen_core=1; else seen_foreign=1; fi
    done <<< "$pids"
    if [ "$seen_core" = 1 ] && [ "$seen_foreign" = 1 ]; then owner=mixed
    elif [ "$seen_core" = 1 ]; then owner=core_candidate
    elif [ "$seen_foreign" = 1 ]; then owner=foreign
    else owner=unknown
    fi
    inventory_emit port "proto=$proto" "port=$port" state=busy "owner=$owner"
}

do_inventory() {
    # Privileged inspection for reliable ss -p PID visibility;
    # this command never writes data, starts services or changes /proc.
    if ! command -v systemctl >/dev/null 2>&1; then
        inventory_emit error reason=missing_systemctl
        return 20
    fi
    if ! command -v ss >/dev/null 2>&1; then
        inventory_emit error reason=missing_ss
        return 20
    fi
    if ! command -v ip >/dev/null 2>&1; then
        inventory_emit error reason=missing_ip
        return 20
    fi

    local unit_state runtime_state unit_owner iface_state database_state
    unit_state="$(systemctl show --value -p LoadState csqtt-core.service 2>/dev/null || true)"
    case "$unit_state" in
        loaded)
            if csqtt_systemd_unit_is_managed; then
                unit_owner=core_candidate
            else
                unit_owner=foreign
            fi
            runtime_state="$(systemctl show --value -p ActiveState csqtt-core.service 2>/dev/null || true)"
            case "$runtime_state" in
                active|activating|reloading) runtime_state=running ;;
                inactive|failed|deactivating) runtime_state=stopped ;;
                *) runtime_state=unknown ;;
            esac
            ;;
        not-found) unit_owner=none; runtime_state=absent ;;
        *) unit_owner=unknown; runtime_state=unknown ;;
    esac
    inventory_emit server place=core runtime=systemd "state=$runtime_state" "owner=$unit_owner"

    # A named Docker container is not automatically owned by Core:
    # its volume and process identity must be validated separately.
    if command -v docker >/dev/null 2>&1 &&
       docker inspect "$CSQTT_DOCKER_CONTAINER" >/dev/null 2>&1; then
        runtime_state="$(docker inspect --format '{{.State.Running}}' "$CSQTT_DOCKER_CONTAINER" 2>/dev/null || true)"
        case "$runtime_state" in true) runtime_state=running ;; false) runtime_state=stopped ;; *) runtime_state=unknown ;; esac
        inventory_emit server place=core runtime=docker "state=$runtime_state" owner=unverified
    fi

    if ip link show "$CSQTT_IFACE" >/dev/null 2>&1; then iface_state=present; else iface_state=absent; fi
    inventory_emit interface "name=$CSQTT_IFACE" "state=$iface_state" owner=unverified
    if [ -f "$CSQTT_CONFIG_DIR/csqtt.db" ]; then database_state=present; else database_state=absent; fi
    inventory_emit database "state=$database_state" name=csqtt.db

    inventory_port udp "$PEER_PORT" || return 20
    inventory_port tcp "$WEB_PORT" || return 20
    inventory_emit end
    return 0
}
'''

def patch(src):
    if 'readonly CSQTT_IFACE="csqtt-core0"' not in src:
        raise ValueError("not the dedicated Core installer")
    if 'assert_deployment_ports_not_foreign() {' not in src:
        raise ValueError("missing prior 2.5.3 ownership safety patch")
    if 'do_inventory() {' in src: raise ValueError("inventory already present")

    src=once(src,
'''do_status() {''',shell(INVENTORY)+'\ndo_status() {','inventory before existing status')
    src=once(src,
'''main() {
    echo "╔══════════════════════════════════════════════════════════════╗"''',
'''main() {
    if [ "@{1:-}" = inventory ]; then
        if [ "$(id -u)" -ne 0 ]; then
            printf 'CSQTT_INV|error|reason=requires_root\\n'
            return 20
        fi
        do_inventory
        return $?
    fi
    echo "╔══════════════════════════════════════════════════════════════╗"''',
'read-only inventory dispatch before installer banner/logging')
    return src

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument('--source',type=Path,required=True)
    ap.add_argument('--check-only',action='store_true')
    a=ap.parse_args()
    new=patch(a.source.read_text(encoding='utf8'))
    if not a.check_only: a.source.write_text(new,encoding='utf8')
    print('CSQTTR_253_CORE_INVENTORY_'+('ANCHORS_PASS' if a.check_only else 'PATCH_APPLIED'))

if __name__=='__main__':main()
