//go:build windows && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"errors"
	"net/netip"
	"syscall"
	"time"
	"unsafe"
)

// Unconnected UDP on Windows: the peer address travels with each datagram.

func udpRecvFrom(fd int, b []byte, deadline time.Time) (int, netip.AddrPort, error) {
	var p *byte
	if len(b) > 0 {
		p = &b[0]
	}
	for {
		if expired(deadline) {
			return 0, netip.AddrPort{}, timeoutError{}
		}
		buf := syscall.WSABuf{Len: uint32(len(b)), Buf: p}
		var n, flags uint32
		var rsa syscall.RawSockaddrAny
		l := int32(unsafe.Sizeof(rsa))
		err := syscall.WSARecvFrom(syscall.Handle(fd), &buf, 1, &n, &flags, &rsa, &l, nil, nil)
		if err == wsaEWOULDBLOCK {
			if werr := poller.wait(fd, false, deadline); werr != nil {
				return 0, netip.AddrPort{}, werr
			}
			continue
		}
		if err != nil {
			return 0, netip.AddrPort{}, err
		}
		sa, _ := rsa.Sockaddr()
		return int(n), addrPortFromSockaddr(sa), nil
	}
}

func udpSendTo(fd int, b []byte, to netip.AddrPort, deadline time.Time) (int, error) {
	sa := sockaddr(netip.AddrPortFrom(to.Addr().Unmap(), to.Port()))
	var p *byte
	if len(b) > 0 {
		p = &b[0]
	}
	for {
		if expired(deadline) {
			return 0, timeoutError{}
		}
		buf := syscall.WSABuf{Len: uint32(len(b)), Buf: p}
		var n uint32
		err := syscall.WSASendto(syscall.Handle(fd), &buf, 1, &n, 0, sa, nil, nil)
		if err == wsaEWOULDBLOCK {
			if werr := poller.wait(fd, true, deadline); werr != nil {
				return 0, werr
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		return int(n), nil
	}
}

func udpRecvMsg(int, []byte, []byte, time.Time) (int, int, int, netip.AddrPort, error) {
	return 0, 0, 0, netip.AddrPort{}, errors.New("ReadMsgUDP not implemented")
}

func udpSendMsg(int, []byte, []byte, netip.AddrPort, time.Time) (int, int, error) {
	return 0, 0, errors.New("WriteMsgUDP not implemented")
}
