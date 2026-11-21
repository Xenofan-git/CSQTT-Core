#!/usr/bin/env python3
"""Fixture ONLY: isolated ARM64 synthetic TURN creds, original WIRE-3 data plane.
Never ship this derived client ELF. Unchanged release ELF is tested separately.
"""
from pathlib import Path
import sys
base=Path(sys.argv[1])
auth=base/'rust-client/auth.rs'
s=auth.read_text()
needle='''    pub async fn get_credentials(&self, link: &str, stream_id: usize) -> Result<TurnCredentials> {
'''
assert s.count(needle)==1
new=needle+'''
        #[cfg(feature = "isolated_turn_fixture")]
        if let Ok(port) = std::env::var("CSQTT_TEST_TURN_RELAY_PORT") {
            let p: u16 = port.parse().context("invalid isolated test-only TURN port")?;
            if p == 0 { bail!("invalid zero synthetic TURN port"); }
            crate::log_error!("[TEST ONLY] Synthetic local TURN credentials loaded; production VK credentials NOT used");
            return Ok(TurnCredentials {
                username: Arc::from("user"),
                password: Arc::from("pass"),
                server_addresses: vec![Arc::from(format!("turn:127.0.0.1:{p}?transport=udp"))].into(),
            });
        }
'''
auth.write_text(s.replace(needle,new))
cargo=base/'rust-client/Cargo.toml'
s=cargo.read_text()
needle='''[dependencies]
'''
assert s.count(needle)==1
cargo.write_text(s.replace(needle,'''[features]
isolated_turn_fixture = []

'''+needle))
main=base/'rust-client/main.rs'
s=main.read_text()
needle='arguments.workers.clamp(3, MAX_WORKERS)'
assert s.count(needle)==1, 'original 2.5 exact worker count changed upstream'
s=s.replace(needle,'''{
            #[cfg(feature = "isolated_turn_fixture")]
            {
                if std::env::var_os("CSQTT_TEST_TURN_RELAY_PORT").is_some() {
                    1
                } else {
                    arguments.workers.clamp(3, MAX_WORKERS)
                }
            }
            #[cfg(not(feature = "isolated_turn_fixture"))]
            {
                arguments.workers.clamp(3, MAX_WORKERS)
            }
        }''',1)
main.write_text(s)
print('SYNTHETIC_TURN_SINGLE_WORKER_CLAMP_ONLY_IN_TEST_ELF')
tests=base/'rust-client/turn_integration_tests.rs'
s=tests.read_text()
addon=r'''
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "isolated external synthetic TURN relay fixture only"]
async fn csqtt_core_fullchain_synthetic_turn_fixture() {
    use std::path::Path;
    let peer: SocketAddr = std::env::var("CSQTT_E2E_PEER")
        .expect("CSQTT_E2E_PEER required").parse().unwrap();
    let port_file = std::env::var("CSQTT_E2E_RELAY_PORT_FILE")
        .expect("CSQTT_E2E_RELAY_PORT_FILE required");
    let stop_file = std::env::var("CSQTT_E2E_RELAY_STOP_FILE")
        .expect("CSQTT_E2E_RELAY_STOP_FILE required");
    assert!(!Path::new(&port_file).exists());
    // Listen on all guest-local interfaces so replies to the separate
    // server namespace arrive over veth; client still contacts 127.0.0.1.
    let relay = Arc::new(UdpSocket::bind("0.0.0.0:0").await.unwrap());
    let port = relay.local_addr().unwrap().port();
    std::fs::write(&port_file,port.to_string()).unwrap();
    println!("ISOLATED_TURN_FIXTURE_READY_PORT_{port}");
    let stop = CancellationToken::new();
    let task = tokio::spawn(relay_bound_turn_channel_to_peer(relay, peer, stop.clone()));
    for _ in 0..1500 {
        if Path::new(&stop_file).exists() { break; }
        if task.is_finished() {
            task.await.unwrap();
            panic!("local TURN relay exited before test finished");
        }
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    stop.cancel();
    task.await.unwrap();
    println!("ISOLATED_TURN_FIXTURE_CLEAN_EXIT");
}
'''
assert 'csqtt_core_fullchain_synthetic_turn_fixture' not in s
tests.write_text(s+addon)
print('RUST_SOURCE_ONLY_ISOLATED_SYNTHETIC_TURN_FIXTURE_PREPARED')
