#!/usr/bin/env python3
"""Opt-in minimal CSQTTR 2.5.3 server fixes for *our* CSQTT Core 2.5.0.

Only touches: rust-server/tun_device.rs, model.rs, web_panel.rs, protocol.rs
in a separate, private, already-patched experimental source copy.

DO NOT apply to original release checkout, production NanoPi or upstream.
All changes are strict-anchored, repeat application rejected, no CLI,
global route, NAT, DNS or multi-instance swg-panel changes.
"""
import argparse
from pathlib import Path

BASE_PREFIX = "[10, 66, 67"
OURS_PREFIX = "[10, 67, 69"

def once(text: str, old: str, new: str, desc: str) -> str:
    n=text.count(old)
    if n != 1:
        raise ValueError(f"{desc}: expected 1 source anchor, got {n}")
    return text.replace(old,new,1)

def tun(text: str) -> str:
    if 'pub const TUN_SUBNET: &str = "10.67.69.0/24";' not in text:
        raise ValueError("wrong dedicated Core8 private subnet")
    if text.count(BASE_PREFIX) != 8:
        raise ValueError("unexpected legacy 10.66.67 prefix occurrences in TUN source")
    text = text.replace(BASE_PREFIX, OURS_PREFIX)
    text=once(text,
'''const SMALL_UDP_STICKY: std::time::Duration = std::time::Duration::from_secs(1);
''',
'''const SMALL_UDP_STICKY: std::time::Duration = std::time::Duration::from_secs(1);
// CSQTTR 2.5.3 selective backport: keep latency-sensitive packets on
// one stream only below its measured 250 ms per-flow traffic budget.
const PIN_WINDOW: std::time::Duration = std::time::Duration::from_millis(250);
const PIN_WINDOW_MAX_BYTES: usize = (TB_RATE_BYTES_PER_SEC as usize) * 3 / 4 / 4;
const BULK_HOLD: std::time::Duration = std::time::Duration::from_secs(2);
''',"small UDP rate-budget constants")
    text=once(text,
'''    sticky_until: Option<std::time::Instant>,
}''',
'''    sticky_until: Option<std::time::Instant>,
    window_start: std::time::Instant,
    window_bytes: usize,
    bulk_until: Option<std::time::Instant>,
}''',"small UDP traffic counters")
    text=once(text,
'''        if elapsed >= 0.0001 {
            self.tokens[idx] =
                (self.tokens[idx] + elapsed * TB_RATE_BYTES_PER_SEC).min(TB_CAPACITY_BYTES);
        }
        self.last_update[idx] = now;''',
'''        if elapsed >= 0.0001 {
            self.tokens[idx] =
                (self.tokens[idx] + elapsed * TB_RATE_BYTES_PER_SEC).min(TB_CAPACITY_BYTES);
            // Do not discard intervals shorter than 100us without credit.
            self.last_update[idx] = now;
        }''',"token refill clock advancement")
    text=once(text,
'''        if let Some(mut state) = self.flows.get(&key).copied() {
            state.last_seen = now;
            if small_udp {
                state.sticky_until = Some(now + SMALL_UDP_STICKY);
            }
            let sticky = state.sticky_until.is_some_and(|until| now < until);''',
'''        if let Some(mut state) = self.flows.get(&key).copied() {
            state.last_seen = now;
            if now.saturating_duration_since(state.window_start) >= PIN_WINDOW {
                state.window_start = now;
                state.window_bytes = 0;
            }
            state.window_bytes = state.window_bytes.saturating_add(packet.len());
            if state.window_bytes > PIN_WINDOW_MAX_BYTES {
                state.bulk_until = Some(now + BULK_HOLD);
                state.sticky_until = None;
            }
            let bulk = state.bulk_until.is_some_and(|until| now < until);
            if small_udp && !bulk {
                state.sticky_until = Some(now + SMALL_UDP_STICKY);
            }
            let sticky = state.sticky_until.is_some_and(|until| now < until);''',"small UDP bulk transitions")
    text=once(text,
'''                sticky_until: small_udp.then(|| now + SMALL_UDP_STICKY),
            },''',
'''                sticky_until: small_udp.then(|| now + SMALL_UDP_STICKY),
                window_start: now,
                window_bytes: packet.len(),
                bulk_until: None,
            },''',"small UDP new-flow initialization")
    text=once(text,
'''    #[test]
    fn route_table_supports_all_stream_scales() {''',
'''    #[test]
    fn dedicated_core8_subnet_matches_actual_tun_address() {
        assert_eq!(TUN_SUBNET, "10.67.69.0/24");
        assert_eq!(crate::net_setup::TUN_ADDR, "10.67.69.1");
        assert!(route_index([10, 67, 69, 2]).is_some());
        assert!(route_index([10, 66, 67, 2]).is_none());
    }

    #[test]
    fn refill_accumulates_intervals_under_100_microseconds() {
        let t0 = std::time::Instant::now();
        let mut route = ClientRoute {
            tokens: vec![0.0],
            last_update: vec![t0],
            ..Default::default()
        };
        for i in 1..=20u64 {
            route.refill(0, t0 + std::time::Duration::from_micros(50 * i));
        }
        let expected = 0.001 * TB_RATE_BYTES_PER_SEC;
        assert!((route.tokens[0] - expected).abs() < 1.0);
    }

    #[test]
    fn route_table_supports_all_stream_scales() {''',"subnet and refill regression tests")
    text=once(text,
'''    #[test]
    fn ipv4_and_ipv6_packet_key_resolution() {''',
'''    #[test]
    fn downlink_bulk_udp_with_small_packets_is_not_pinned() {
        let mut table = RouteTable::new();
        for w in 1..=10u16 {
            assert!(table.register(ip(2), w as _, w as _, w, w as _));
        }
        let start = std::time::Instant::now();
        let (big, small) = (udp_packet(1300, 443), udp_packet(100, 443));
        let mut per = std::collections::HashMap::new();
        for i in 0..4000u64 {
            let now = start + std::time::Duration::from_micros(i * 520);
            let pkt = if i % 385 == 0 { &small } else { &big };
            *per.entry(table.select_packet(2, now, pkt).unwrap().worker_id).or_insert(0usize) += 1;
        }
        let highest = per.values().copied().max().unwrap();
        assert!(per.len() == 10 && highest < 800, "bulk flow stuck: {per:?}");
    }

    #[test]
    fn downlink_game_udp_mixed_sizes_stay_pinned() {
        let mut table = RouteTable::new();
        for w in 1..=3u16 {
            assert!(table.register(ip(2), w as _, w as _, w, w as _));
        }
        let start = std::time::Instant::now();
        let (big, small) = (udp_packet(1200, 27015), udp_packet(120, 27015));
        let mut selected = Vec::new();
        for i in 0..320u64 {
            let now = start + std::time::Duration::from_micros(i * 15_625);
            let pkt = if i % 2 == 0 { &small } else { &big };
            selected.push(table.select_packet(2, now, pkt).unwrap().worker_id);
        }
        assert!(selected.iter().all(|&id| id == selected[0]), "mixed game flow was split");
    }

    #[test]
    fn ipv4_and_ipv6_packet_key_resolution() {''',"bulk versus game traffic tests")
    return text

def model(text: str) -> str:
    if 'pub const OWNER_TUNNEL_IP: &str = "10.67.69.2";' not in text:
        raise ValueError("wrong Core8 owner address")
    text=once(text,
'''            if entry.ip.is_empty()
                && let Some(next_ip) = get_next_ip(self)
            {
                entry.ip = next_ip;
                repairs.push(format!("password tag={tag} ip assigned"));
            }''',
'''            let outside_pool = !entry.ip.is_empty() && !ip_in_pool(&entry.ip);
            let duplicate_ip = self.passwords.values().any(|other| other.ip == entry.ip);
            if entry.ip.is_empty() || outside_pool || duplicate_ip {
                entry.ip = get_next_ip(self).unwrap_or_default();
                repairs.push(if outside_pool {
                    format!("password tag={tag} out-of-pool ip reassigned")
                } else if duplicate_ip {
                    format!("password tag={tag} duplicate ip reassigned")
                } else {
                    format!("password tag={tag} ip assigned")
                });
            }''',"stale/duplicate participant address healing")
    text=once(text,
'''pub fn get_next_ip(db: &Database) -> Option<String> {''',
'''/// Only participant addresses within this Core8 instance's TUN pool.
pub fn ip_in_pool(ip: &str) -> bool {
    let Some(octets) = crate::tun_device::parse_ipv4(ip) else {
        return false;
    };
    crate::tun_device::route_index(octets).is_some() && (3..=250).contains(&octets[3])
}

/// Preserve imported participant IP only when it belongs here and is free.
pub fn import_ip(db: &Database, ip: &str) -> String {
    if ip_in_pool(ip) && !db.passwords.values().any(|entry| entry.ip == ip) {
        return ip.to_owned();
    }
    get_next_ip(db).unwrap_or_default()
}

pub fn get_next_ip(db: &Database) -> Option<String> {''',"import pool and collision guard")
    text=once(text,
'''    db.passwords.get(session_password).and_then(|entry| {
        if !entry.ip.is_empty() {
            Some(entry.ip.clone())
        } else {
            None
        }
    })''',
'''    db.passwords.get(session_password).and_then(|entry| {
        if ip_in_pool(&entry.ip) {
            Some(entry.ip.clone())
        } else {
            None
        }
    })''',"reject invalid client IP before session")
    text=once(text,
'''    #[test]
    fn repair_migrates_passwords_and_assigns_ip() {''',
'''    #[test]
    fn select_only_valid_free_imported_tun_addresses() {
        let mut db = Database::default();
        let entry = |ip: &str| PasswordEntry {
            ip: ip.to_owned(),
            ..Default::default()
        };
        db.passwords.insert("current".to_owned(), entry("10.67.69.3"));
        assert!(super::ip_in_pool("10.67.69.3"));
        assert!(super::ip_in_pool("10.67.69.250"));
        for invalid in ["", "10.67.69.1", "10.67.69.2", "10.67.69.251",
                        "10.66.67.3", "10.67.68.3", "hello", "fd00::3"] {
            assert!(!super::ip_in_pool(invalid), "{invalid}");
        }
        assert_eq!(super::import_ip(&db, "10.67.69.40"), "10.67.69.40");
        assert_eq!(super::import_ip(&db, "10.67.69.3"), "10.67.69.4");
        assert_eq!(super::import_ip(&db, "10.66.67.44"), "10.67.69.4");
        db.passwords.insert("imported".into(), entry("10.67.69.4"));
        assert_eq!(super::import_ip(&db, "10.67.69.4"), "10.67.69.5");
    }

    #[test]
    fn repair_migrates_passwords_and_assigns_ip() {''',"imported IP conflict tests")
    return text

def web(text: str) -> str:
    return once(text,
'''                    let ip = if c.ip.is_empty() {
                        model::get_next_ip(&db).unwrap_or_default()
                    } else {
                        c.ip
                    };''',
'''                    let ip = model::import_ip(&db, &c.ip);''',"safe imported IP assignment")

def protocol(text: str) -> str:
    return once(text,
'''db.passwords.get(slot_key).is_some_and(|e| e.ip.is_empty())''',
'''db.passwords.get(slot_key).is_some_and(|e| !crate::model::ip_in_pool(&e.ip))''',
"validate participant pool before TUNCONF")

def main():
    p=argparse.ArgumentParser()
    p.add_argument("--source-root",required=True,type=Path)
    p.add_argument("--check-only",action="store_true")
    args=p.parse_args()
    base=args.source_root.resolve()
    files={
        "tun_device.rs":tun,
        "model.rs":model,
        "web_panel.rs":web,
        "protocol.rs":protocol,
    }
    original={}
    modified={}
    for name,fn in files.items():
        path=base/name
        original[name]=path.read_text(encoding="utf8")
        modified[name]=fn(original[name])
        assert modified[name] != original[name]
    if not args.check_only:
        for name,updated in modified.items():
            (base/name).write_text(updated,encoding="utf8")
    print("CSQTTR_253_SELECTIVE_CORE8_"+("ANCHORS_PASS" if args.check_only else "PATCH_APPLIED"))
    for name in files:
        print("PATCH_FILE="+name+" old_lines="+str(len(original[name].splitlines()))+
              " new_lines="+str(len(modified[name].splitlines())))
if __name__=="__main__":
    main()
