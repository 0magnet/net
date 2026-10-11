//go:build windows && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: Native (host) netdev for the TinyGo "windows" target, on Winsock.
//
// The windows target runs goroutines on one thread, so no socket call may
// block. Sockets are non-blocking, and a goroutine that would block parks in
// the poller (netpoll_windows.go). Calls that Go's syscall package leaves out
// are loaded from ws2_32.dll.

package net

import (
	"io"
	"net/netip"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Winsock values from winsock2.h, ws2ipdef.h and winerror.h that Go's syscall
// package does not name.
const (
	wsaEWOULDBLOCK syscall.Errno = 10035
	wsaEINPROGRESS syscall.Errno = 10036
	wsaEALREADY    syscall.Errno = 10037
	wsaEISCONN     syscall.Errno = 10056

	winFIONBIO      = 0x8004667e
	winSoError      = 0x1007
	winTCPKeepidle  = 3
	winTCPKeepcnt   = 16
	winTCPKeepintvl = 17
)

var (
	modws2_32       = syscall.NewLazyDLL("ws2_32.dll")
	procAccept      = modws2_32.NewProc("accept")
	procIoctlsocket = modws2_32.NewProc("ioctlsocket")
	procSelect      = modws2_32.NewProc("select")
)

func init() {
	var d syscall.WSAData
	syscall.WSAStartup(uint32(0x202), &d)
}

func (*hostNetdev) GetHostByName(name string) (netip.Addr, error) {
	if name == "" {
		return netip.AddrFrom4([4]byte{}), nil
	}
	if addr, err := netip.ParseAddr(name); err == nil {
		return addr.Unmap(), nil
	}
	if strings.EqualFold(name, "localhost") {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	// Like the Unix lookup, return the first IPv4 address. GetAddrInfoW
	// blocks the thread, which the scheduler cannot avoid here.
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return netip.Addr{}, err
	}
	hints := syscall.AddrinfoW{Family: syscall.AF_INET, Socktype: syscall.SOCK_STREAM, Protocol: syscall.IPPROTO_TCP}
	var res *syscall.AddrinfoW
	if err := syscall.GetAddrInfoW(n, nil, &hints, &res); err != nil {
		return netip.Addr{}, &DNSError{Err: err.Error(), Name: name}
	}
	defer syscall.FreeAddrInfoW(res)
	for r := res; r != nil; r = r.Next {
		if r.Family == syscall.AF_INET && r.Addr != nil {
			sa := (*syscall.RawSockaddrInet4)(unsafe.Pointer(r.Addr))
			return netip.AddrFrom4(sa.Addr), nil
		}
	}
	return netip.Addr{}, &DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (*hostNetdev) Addr() (netip.Addr, error) {
	// Connecting a UDP socket sends nothing but picks the outbound address.
	h, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return netip.Addr{}, err
	}
	defer syscall.Closesocket(h)
	if err := syscall.Connect(h, &syscall.SockaddrInet4{Addr: [4]byte{8, 8, 8, 8}, Port: 53}); err != nil {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	sa, err := syscall.Getsockname(h)
	if err != nil {
		return netip.Addr{}, err
	}
	if sa4, ok := sa.(*syscall.SockaddrInet4); ok {
		return netip.AddrFrom4(sa4.Addr), nil
	}
	return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
}

func (*hostNetdev) Socket(domain, stype, protocol int) (int, error) {
	if protocol == _IPPROTO_TLS {
		protocol = syscall.IPPROTO_TCP
	}
	if domain == _AF_INET6 {
		domain = syscall.AF_INET6
	}
	h, err := syscall.Socket(domain, stype, protocol)
	if err != nil {
		return -1, err
	}
	if err := setNonblock(h); err != nil {
		syscall.Closesocket(h)
		return -1, err
	}
	if stype == syscall.SOCK_DGRAM {
		// Otherwise an ICMP port unreachable for an earlier datagram fails the
		// next read with WSAECONNRESET, as Go's net package also avoids.
		flag, ret := uint32(0), uint32(0)
		syscall.WSAIoctl(h, syscall.SIO_UDP_CONNRESET, (*byte)(unsafe.Pointer(&flag)), uint32(unsafe.Sizeof(flag)), nil, 0, &ret, nil, 0)
	}
	return int(h), nil
}

func setNonblock(h syscall.Handle) error {
	one := uint32(1)
	if r, _, err := procIoctlsocket.Call(uintptr(h), winFIONBIO, uintptr(unsafe.Pointer(&one))); r != 0 {
		return err
	}
	return nil
}

func (*hostNetdev) Bind(sockfd int, ip netip.AddrPort) error {
	return syscall.Bind(syscall.Handle(sockfd), sockaddr(ip))
}

func (n *hostNetdev) Connect(sockfd int, host string, ip netip.AddrPort) error {
	addr := ip.Addr()
	if !addr.IsValid() || addr.IsUnspecified() {
		resolved, err := n.GetHostByName(host)
		if err != nil {
			return err
		}
		addr = resolved
	}
	h := syscall.Handle(sockfd)
	switch err := syscall.Connect(h, sockaddrFromParts(addr, ip.Port())); err {
	case nil, wsaEISCONN:
		return nil
	case wsaEWOULDBLOCK, wsaEINPROGRESS, wsaEALREADY:
		// The connect goes on in the background. It is done when the socket
		// is writable, or failed when the poller reports an exception.
		if werr := poller.wait(sockfd, true, time.Time{}); werr != nil {
			return werr
		}
		var soErr int32
		l := int32(unsafe.Sizeof(soErr))
		if err := syscall.Getsockopt(h, syscall.SOL_SOCKET, winSoError, (*byte)(unsafe.Pointer(&soErr)), &l); err != nil {
			return err
		}
		if soErr != 0 {
			return syscall.Errno(soErr)
		}
		return nil
	default:
		return err
	}
}

func (*hostNetdev) PollDeadline(sockfd int, write bool, deadline time.Time) {
	poller.interrupt(sockfd, write, deadline)
}

func (*hostNetdev) Listen(sockfd int, backlog int) error {
	return syscall.Listen(syscall.Handle(sockfd), backlog)
}

func (*hostNetdev) Accept(sockfd int) (int, netip.AddrPort, error) {
	for {
		var rsa syscall.RawSockaddrAny
		l := int32(unsafe.Sizeof(rsa))
		r, _, err := procAccept.Call(uintptr(sockfd), uintptr(unsafe.Pointer(&rsa)), uintptr(unsafe.Pointer(&l)))
		if h := syscall.Handle(r); h != syscall.InvalidHandle {
			// An accepted socket inherits non-blocking mode from the listener.
			sa, _ := rsa.Sockaddr()
			return int(h), addrPortFromSockaddr(sa), nil
		}
		if err != wsaEWOULDBLOCK {
			return -1, netip.AddrPort{}, err
		}
		if werr := poller.wait(sockfd, false, time.Time{}); werr != nil {
			return -1, netip.AddrPort{}, werr
		}
	}
}

func (*hostNetdev) GetSockname(sockfd int) (netip.AddrPort, error) {
	sa, err := syscall.Getsockname(syscall.Handle(sockfd))
	if err != nil {
		return netip.AddrPort{}, err
	}
	return addrPortFromSockaddr(sa), nil
}

func (*hostNetdev) Send(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	total := 0
	for total < len(buf) {
		if expired(deadline) {
			return total, timeoutError{}
		}
		b := syscall.WSABuf{Len: uint32(len(buf) - total), Buf: &buf[total]}
		var sent uint32
		err := syscall.WSASend(syscall.Handle(sockfd), &b, 1, &sent, 0, nil, nil)
		if err == wsaEWOULDBLOCK {
			if werr := poller.wait(sockfd, true, deadline); werr != nil {
				return total, werr
			}
			continue
		}
		if err != nil {
			return total, err
		}
		if sent == 0 {
			break
		}
		total += int(sent)
	}
	return total, nil
}

func (*hostNetdev) Recv(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	for {
		if expired(deadline) {
			return 0, timeoutError{}
		}
		b := syscall.WSABuf{Len: uint32(len(buf)), Buf: &buf[0]}
		var n, fl uint32
		err := syscall.WSARecv(syscall.Handle(sockfd), &b, 1, &n, &fl, nil, nil)
		if err == wsaEWOULDBLOCK {
			if werr := poller.wait(sockfd, false, deadline); werr != nil {
				return 0, werr
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		return int(n), nil
	}
}

func (*hostNetdev) Close(sockfd int) error {
	poller.close(sockfd)
	return syscall.Closesocket(syscall.Handle(sockfd))
}

func (*hostNetdev) SetSockOpt(sockfd int, level int, opt int, value interface{}) error {
	h := syscall.Handle(sockfd)
	switch {
	case level == _SOL_SOCKET && opt == _SO_LINGER:
		sec := toInt(value)
		l := &syscall.Linger{}
		if sec >= 0 {
			l.Onoff = 1
			l.Linger = int32(sec)
		}
		return syscall.SetsockoptLinger(h, syscall.SOL_SOCKET, syscall.SO_LINGER, l)
	case level == _SOL_SOCKET && opt == _SO_KEEPALIVE:
		return syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, toInt(value))
	case level == _SOL_TCP && opt == _TCP_KEEPIDLE:
		return syscall.SetsockoptInt(h, syscall.IPPROTO_TCP, winTCPKeepidle, toInt(value))
	case level == _SOL_TCP && opt == _TCP_KEEPINTVL:
		return syscall.SetsockoptInt(h, syscall.IPPROTO_TCP, winTCPKeepintvl, toInt(value))
	case level == _SOL_TCP && opt == _TCP_KEEPCNT:
		return syscall.SetsockoptInt(h, syscall.IPPROTO_TCP, winTCPKeepcnt, toInt(value))
	}
	return syscall.SetsockoptInt(h, level, opt, toInt(value))
}

// ShutdownWrite half-closes sockfd for TCPConn.CloseWrite.
func (*hostNetdev) ShutdownWrite(sockfd int) error {
	return syscall.Shutdown(syscall.Handle(sockfd), syscall.SHUT_WR)
}
