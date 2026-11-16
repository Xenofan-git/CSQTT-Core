"""Prevent SOCKS5 UDP relay sockets from reusing transparent TPROXY listener ports."""
from pathlib import Path

def apply(path: Path) -> None:
    source = path.read_text()
    anchor = '''        socket
            .set_reuse_address(true)
            .context("enable SO_REUSEADDR on SOCKS5 UDP relay socket")?;
'''
    assert source.count(anchor) == 1, 'SOCKS5 UDP relay reuse-address anchor changed'
    source = source.replace(anchor, '''        // SOCKS5 relay sockets must never reuse the transparent TPROXY listener
        // port. SO_REUSEADDR on both sockets lets Linux allocate that port to an
        // ephemeral relay socket and divert intercepted UDP datagrams.
        // Keep SO_REUSEADDR disabled on the ephemeral relay socket.
''')
    path.write_text(source)
