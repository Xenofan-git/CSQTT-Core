"""Instrument generated ARM64 test-only Rust TPROXY path for REC_UDP IDs."""
from pathlib import Path

def apply(path: Path) -> None:
    source = path.read_text()
    anchor = '    fn send_frame(flow: &UdpFlow, destination: SocketAddrV4, payload: &[u8]) -> io::Result<()> {'
    assert source.count(anchor) == 1
    helper = '''    fn trace_lab_udp(event: &str, payload: &[u8]) {
        if let Some(marker) = payload.strip_prefix(b"REC_UDP_") {
            if marker.iter().all(u8::is_ascii_digit) {
                eprintln!("UDP_PACKET_TRACE event={event} id=REC_UDP_{}", String::from_utf8_lossy(marker));
            }
        }
    }

'''
    source = source.replace(anchor, helper + anchor)
    anchor = '                                let key = SocketAddr::V4(client);'
    assert source.count(anchor) == 1
    source = source.replace(anchor, '                                trace_lab_udp("INGRESS", payload);\n' + anchor)
    anchor = '                                    if let Err(error) = send_frame(flow, destination, payload) {'
    assert source.count(anchor) == 1
    source = source.replace(anchor, '                                    trace_lab_udp("EXISTING_FLOW", payload);\n' + anchor)
    anchor = '                                    queue.push(destination, payload, &opening_buffers);'
    assert source.count(anchor) == 2
    source = source.replace(anchor, '''                                    trace_lab_udp("OPENING_QUEUED", payload);
                                    if !queue.push(destination, payload, &opening_buffers) {
                                        trace_lab_udp("OPENING_QUEUE_REJECTED", payload);
                                    }''', 1)
    anchor = '                                    if at.elapsed() < ASSOCIATION_BACKOFF {\n                                        continue;'
    assert source.count(anchor) == 1
    source = source.replace(anchor, '''                                    if at.elapsed() < ASSOCIATION_BACKOFF {
                                        trace_lab_udp("ASSOCIATION_BACKOFF_DROP", payload);
                                        continue;''')
    anchor = '                                if opening.len() >= MAX_CONCURRENT_OPENINGS {\n                                    continue;'
    assert source.count(anchor) == 1
    source = source.replace(anchor, '''                                if opening.len() >= MAX_CONCURRENT_OPENINGS {
                                    trace_lab_udp("OPENINGS_LIMIT_DROP", payload);
                                    continue;''')
    anchor = '                                    let mut queue = OpeningQueue::default();\n                                    queue.push(destination, payload, &opening_buffers);'
    assert source.count(anchor) == 1
    source = source.replace(anchor, '''                                    let mut queue = OpeningQueue::default();
                                    trace_lab_udp("NEW_OPENING", payload);
                                    if !queue.push(destination, payload, &opening_buffers) {
                                        trace_lab_udp("NEW_OPENING_QUEUE_REJECTED", payload);
                                    }''')
    anchor = '                                    if let Err(error) = send_frame(&flow, destination, payload) {'
    assert source.count(anchor) == 1
    source = source.replace(anchor, '                                    trace_lab_udp("POOLED_FLOW", payload);\n' + anchor)
    # This is a diagnostic generated test source, not production routing.
    path.write_text(source)
