#!/usr/bin/env python3
"""Strict opt-in CSQTTR 2.5.3 port ownership guard for dedicated Core Deploy."""
import argparse
from pathlib import Path

def dollar(s):
    return s.replace('@{', '$'+'{')

def one(src, old, new, label):
    old, new=dollar(old),dollar(new)
    if src.count(old)!=1: raise ValueError(f"{label}: expected one anchor, got {src.count(old)}")
    return src.replace(old,new,1)

def patch(src):
    if 'readonly CSQTT_IFACE="csqtt-core0"' not in src: raise ValueError("wrong Core installer")
    if 'assert_deployment_ports_not_foreign() {' in src: raise ValueError("already patched")
    src=one(src,'assert_peer_port_is_available() {',
'''web_port_listeners() {
    ss -H -ltnp "sport = :@{WEB_PORT}" 2>/dev/null || true
}

web_port_listener_pids() {
    web_port_listeners | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -un || true
}

assert_deployment_ports_not_foreign() {
    command -v ss >/dev/null 2>&1 ||
        die "Cannot verify port ownership: ss missing" "$EXIT_PREFLIGHT_FAILED"
    local listeners pids pid
    listeners="$(peer_port_listeners)"
    if [ -n "$listeners" ]; then
        pids="$(peer_port_listener_pids)"
        [ -n "$pids" ] ||
            die "UDP/@{PEER_PORT} owner unknown" "$EXIT_PREFLIGHT_FAILED"
        while IFS= read -r pid; do
            [ -n "$pid" ] || continue
            csqtt_process_is_owned "$pid" ||
                die "UDP/@{PEER_PORT} owned by unrelated PID $pid" "$EXIT_PREFLIGHT_FAILED"
        done <<< "$pids"
    fi
    listeners="$(web_port_listeners)"
    if [ -n "$listeners" ]; then
        pids="$(web_port_listener_pids)"
        [ -n "$pids" ] ||
            die "TCP/@{WEB_PORT} owner unknown" "$EXIT_PREFLIGHT_FAILED"
        while IFS= read -r pid; do
            [ -n "$pid" ] || continue
            csqtt_process_is_owned "$pid" ||
                die "TCP/@{WEB_PORT} owned by unrelated PID $pid" "$EXIT_PREFLIGHT_FAILED"
        done <<< "$pids"
    fi
}

assert_web_port_is_available() {
    [ -z "$(web_port_listeners)" ] ||
        die "TCP/@{WEB_PORT} still occupied after cleanup" "$EXIT_CUTOVER_FAILED"
}

assert_peer_port_is_available() {''','new read-only ownership guard')
    src=one(src,
'''assert_peer_port_is_available() {
    local listeners
    listeners="$(peer_port_listeners)"
    [ -z "$listeners" ] && return 0
    log_warn "UDP/@{PEER_PORT} занят; выполняется принудительное освобождение"
    force_release_peer_port || die "Не удалось принудительно освободить UDP/@{PEER_PORT}"
    [ -z "$(peer_port_listeners)" ] || die "UDP/@{PEER_PORT} остался занят после принудительной очистки"
}''',
'''assert_peer_port_is_available() {
    local listeners pids pid
    listeners="$(peer_port_listeners)"
    [ -z "$listeners" ] && return 0
    pids="$(peer_port_listener_pids)"
    [ -n "$pids" ] ||
        die "UDP/@{PEER_PORT} owner unknown; refusing cleanup" "$EXIT_CUTOVER_FAILED"
    while IFS= read -r pid; do
        [ -n "$pid" ] || continue
        csqtt_process_is_owned "$pid" ||
            die "UDP/@{PEER_PORT} unrelated PID $pid; refusing cleanup" "$EXIT_CUTOVER_FAILED"
    done <<< "$pids"
    force_release_peer_port || die "Failed owned UDP/@{PEER_PORT} cleanup" "$EXIT_CUTOVER_FAILED"
    [ -z "$(peer_port_listeners)" ] ||
        die "UDP/@{PEER_PORT} occupied after cleanup" "$EXIT_CUTOVER_FAILED"
}''','safe cleanup')
    src=one(src,
'''        while IFS= read -r pid; do
            [ -n "$pid" ] || continue
            found=1
            container="$(docker_container_for_pid "$pid" 2>/dev/null || true)"
''',
'''        while IFS= read -r pid; do
            [ -n "$pid" ] || continue
            csqtt_process_is_owned "$pid" ||
                die "UDP/@{PEER_PORT} changed to unrelated PID $pid; refusing cleanup" "$EXIT_CUTOVER_FAILED"
            found=1
            container="$(docker_container_for_pid "$pid" 2>/dev/null || true)"
''','check immediately before stop')
    src=one(src,
'''    assert_peer_port_is_available

    cleanup_legacy_proxy_interfaces''',
'''    assert_peer_port_is_available
    assert_web_port_is_available

    cleanup_legacy_proxy_interfaces''','post shutdown TCP guard')
    src=one(src,
'''            validate_distinct_network_ports

            local total_started=$SECONDS''',
'''            validate_distinct_network_ports
            # Before apt, sysctl, service stop and TUN cleanup.
            assert_deployment_ports_not_foreign

            local total_started=$SECONDS''','preflight before mutation')
    return src

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument('--source',type=Path,required=True)
    ap.add_argument('--check-only',action='store_true')
    a=ap.parse_args()
    updated=patch(a.source.read_text(encoding='utf8'))
    if not a.check_only: a.source.write_text(updated,encoding='utf8')
    print('SELECTIVE_DEPLOY_OWNER_GUARDS_'+('ANCHORS_PASS' if a.check_only else 'PATCH_APPLIED'))

if __name__=='__main__':main()
