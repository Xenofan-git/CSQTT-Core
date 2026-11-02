// SPDX-FileCopyrightText: 2026 amurcanov
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

use anyhow::{Context, Result, bail};
use std::fs::File;
use tokio_util::sync::CancellationToken;

#[cfg(unix)]
use std::os::fd::{AsRawFd, FromRawFd, RawFd};
#[cfg(unix)]
use std::os::unix::net::UnixListener;

#[cfg(unix)]
pub struct FdReceiver {
    path: String,
    listener: tokio::net::UnixListener,
}

#[cfg(unix)]
impl FdReceiver {
    pub fn bind(name: &str) -> Result<Self> {
        let _ = std::fs::remove_file(name);
        let std_listener = UnixListener::bind(name)
            .with_context(|| format!("bind TUN UDS: {name}"))?;
        std_listener.set_nonblocking(true)?;
        let listener = tokio::net::UnixListener::from_std(std_listener)?;
        crate::log_error!("[TUN] UDS listener ready: {name}");
        Ok(Self { path: name.to_owned(), listener })
    }

    pub async fn receive(&self, cancel: &CancellationToken) -> Result<File> {
        let (stream, _) = tokio::select! {
            _ = cancel.cancelled() => bail!("cancelled"),
            result = self.listener.accept() => result.context("accept TUN UDS")?,
        };
        let stream = stream.into_std().context("convert TUN UDS stream")?;
        let fd = stream.as_raw_fd();
        let tun_fd = tokio::task::spawn_blocking(move || recv_fd(fd))
            .await
            .context("TUN FD receiver task")??;
        let ack = b"TUN-ACK\n";
        unsafe {
            let rc = libc::send(fd, ack.as_ptr().cast(), ack.len(), 0);
            if rc < 0 {
                return Err(std::io::Error::last_os_error().into());
            }
        }
        crate::log_error!("[TUN] TUN FD accepted by client");
        Ok(unsafe { File::from_raw_fd(tun_fd) })
    }
}

#[cfg(unix)]
impl Drop for FdReceiver {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.path);
    }
}

#[cfg(unix)]
fn recv_fd(fd: RawFd) -> Result<RawFd> {
    let mut byte = [0u8; 32];
    let mut iov = libc::iovec {
        iov_base: byte.as_mut_ptr().cast(),
        iov_len: byte.len(),
    };
    let mut control = [0u8; 64];
    let mut msg: libc::msghdr = unsafe { std::mem::zeroed() };
    msg.msg_iov = &mut iov;
    msg.msg_iovlen = 1;
    msg.msg_control = control.as_mut_ptr().cast();
    msg.msg_controllen = control.len();

    let n = unsafe { libc::recvmsg(fd, &mut msg, 0) };
    if n < 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    if n == 0 {
        bail!("TUN FD sender closed");
    }

    unsafe {
        let mut cmsg = libc::CMSG_FIRSTHDR(&msg);
        while !cmsg.is_null() {
            if (*cmsg).cmsg_level == libc::SOL_SOCKET
                && (*cmsg).cmsg_type == libc::SCM_RIGHTS
            {
                let data = libc::CMSG_DATA(cmsg).cast::<RawFd>();
                let len = (*cmsg).cmsg_len as usize - libc::CMSG_LEN(0) as usize;
                if len >= std::mem::size_of::<RawFd>() {
                    return Ok(*data);
                }
            }
            cmsg = libc::CMSG_NXTHDR(&msg, cmsg);
        }
    }
    bail!("TUN FD not present in UDS message")
}

#[cfg(not(unix))]
pub struct FdReceiver;

#[cfg(not(unix))]
impl FdReceiver {
    pub fn bind(_name: &str) -> Result<Self> { Ok(Self) }
    pub async fn receive(&self, _cancel: &CancellationToken) -> Result<File> {
        bail!("TUN FD transport is not supported on this platform")
    }
}
