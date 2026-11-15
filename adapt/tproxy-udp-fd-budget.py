"""Apply fd-aware UDP association admission to generated upstream TPROXY Rust."""
from pathlib import Path

def apply(path: Path) -> None:
    source = path.read_text()
    anchor = '    fn acquire_udp_quota(\n'
    assert source.count(anchor) == 1
    helper = '''    // Each SOCKS5 UDP flow holds a TCP control connection and UDP relay socket.
    // Keep headroom for TCP sessions, listeners, DNS, and transient openings.
    // A low RLIMIT_NOFILE must not be interpreted as permission to open
    // thousands of UDP associations just because the configured flow cap allows it.
    fn udp_fd_flow_budget() -> usize {
        let mut limit = libc::rlimit { rlim_cur: 0, rlim_max: 0 };
        if unsafe { libc::getrlimit(libc::RLIMIT_NOFILE, &mut limit) } != 0 {
            return 128;
        }
        let soft = limit.rlim_cur.min(usize::MAX as libc::rlim_t) as usize;
        // Two descriptors per flow plus an equally large reserve for TCP and
        // in-flight association establishment; reserve 128 for fixed overhead.
        soft.saturating_sub(128).saturating_div(4).max(1).min(super::MAX_UDP_FLOW_LIMIT)
    }

'''
    source = source.replace(anchor, helper + anchor)
    anchor2 = '                                if opening.len() >= MAX_CONCURRENT_OPENINGS {\n'
    assert source.count(anchor2) == 1
    source = source.replace(anchor2, '''                                // Evict the oldest UDP association *before* opening
                                // another SOCKS5 control/relay pair. This prevents
                                // EMFILE even when the quota's flow ceiling is higher
                                // than the process file-descriptor allowance.
                                let fd_budget = udp_fd_flow_budget();
                                while flows.len() + opening.len() >= fd_budget {
                                    if !evict_oldest_udp_flow(&mut flows, &stats) {
                                        break;
                                    }
                                }
                                if flows.len() + opening.len() >= fd_budget {
                                    stats.udp_budget_rejected();
                                    continue;
                                }
''' + anchor2)
    path.write_text(source)
