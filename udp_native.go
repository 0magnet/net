//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"net/netip"
	"syscall"
	"time"
)

// Unconnected UDP on the host: the peer address travels with each datagram.

func udpRecvFrom(fd int, b []byte, deadline time.Time) (int, netip.AddrPort, error) {
	for {
		if expired(deadline) {
			return 0, netip.AddrPort{}, timeoutError{}
		}
		n, sa, err := syscall.Recvfrom(fd, b, 0)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(fd, false, deadline); werr != nil {
				return 0, netip.AddrPort{}, werr
			}
			continue
		}
		if err != nil {
			return 0, netip.AddrPort{}, err
		}
		return n, addrPortFromSockaddr(sa), nil
	}
}

func udpSendTo(fd int, b []byte, to netip.AddrPort, deadline time.Time) (int, error) {
	sa := sockaddr(netip.AddrPortFrom(to.Addr().Unmap(), to.Port()))
	for {
		if expired(deadline) {
			return 0, timeoutError{}
		}
		err := syscall.Sendto(fd, b, 0, sa)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(fd, true, deadline); werr != nil {
				return 0, werr
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		return len(b), nil
	}
}

func udpRecvMsg(fd int, b, oob []byte, deadline time.Time) (n, oobn, flags int, from netip.AddrPort, err error) {
	for {
		if expired(deadline) {
			return 0, 0, 0, netip.AddrPort{}, timeoutError{}
		}
		var sa syscall.Sockaddr
		n, oobn, flags, sa, err = syscall.Recvmsg(fd, b, oob, 0)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(fd, false, deadline); werr != nil {
				return 0, 0, 0, netip.AddrPort{}, werr
			}
			continue
		}
		if err != nil {
			return 0, 0, 0, netip.AddrPort{}, err
		}
		if sa != nil {
			from = addrPortFromSockaddr(sa)
		}
		return n, oobn, flags, from, nil
	}
}

func udpSendMsg(fd int, b, oob []byte, to netip.AddrPort, deadline time.Time) (int, int, error) {
	var sa syscall.Sockaddr
	if to.IsValid() {
		sa = sockaddr(netip.AddrPortFrom(to.Addr().Unmap(), to.Port()))
	}
	for {
		if expired(deadline) {
			return 0, 0, timeoutError{}
		}
		n, err := syscall.SendmsgN(fd, b, oob, sa, 0)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(fd, true, deadline); werr != nil {
				return 0, 0, werr
			}
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		return n, len(oob), nil
	}
}
