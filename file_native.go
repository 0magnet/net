//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"errors"
	"net/netip"
	"os"
	"syscall"
)

// The host netdev uses real descriptors, so a socket from another source can be
// adopted. Each call works on a duplicate, as in Go.

func adoptFD(f *os.File) (fd, sotype int, lsa syscall.Sockaddr, err error) {
	fd, err = syscall.Dup(int(f.Fd()))
	if err != nil {
		return -1, 0, nil, os.NewSyscallError("dup", err)
	}
	syscall.CloseOnExec(fd)
	if err = syscall.SetNonblock(fd, true); err == nil {
		if sotype, err = syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE); err == nil {
			lsa, err = syscall.Getsockname(fd)
		}
	}
	if err != nil {
		syscall.Close(fd)
		return -1, 0, nil, err
	}
	return fd, sotype, lsa, nil
}

func unixAddrFromSockaddr(sa syscall.Sockaddr) *UnixAddr {
	if u, ok := sa.(*syscall.SockaddrUnix); ok {
		return &UnixAddr{Name: u.Name, Net: "unix"}
	}
	return &UnixAddr{Net: "unix"}
}

var errFileType = errors.New("net: file is not a supported socket")

// FileConn returns a copy of the network connection corresponding to the open
// file f.
func FileConn(f *os.File) (Conn, error) {
	fd, sotype, lsa, err := adoptFD(f)
	if err != nil {
		return nil, err
	}
	rsa, _ := syscall.Getpeername(fd)
	switch lsa.(type) {
	case *syscall.SockaddrInet4, *syscall.SockaddrInet6:
		la := addrPortFromSockaddr(lsa)
		var ra netip.AddrPort
		if rsa != nil {
			ra = addrPortFromSockaddr(rsa)
		}
		switch sotype {
		case syscall.SOCK_STREAM:
			c := &TCPConn{fd: fd, net: "tcp", laddr: TCPAddrFromAddrPort(la)}
			if ra.IsValid() {
				c.raddr = TCPAddrFromAddrPort(ra)
			}
			return c, nil
		case syscall.SOCK_DGRAM:
			c := &UDPConn{fd: fd, net: "udp", laddr: UDPAddrFromAddrPort(la)}
			if ra.IsValid() {
				c.raddr = UDPAddrFromAddrPort(ra)
			}
			return c, nil
		}
	case *syscall.SockaddrUnix:
		if sotype == syscall.SOCK_STREAM {
			c := &UnixConn{fd: fd, laddr: unixAddrFromSockaddr(lsa), raddr: &UnixAddr{Net: "unix"}}
			if rsa != nil {
				c.raddr = unixAddrFromSockaddr(rsa)
			}
			return c, nil
		}
	}
	syscall.Close(fd)
	return nil, errFileType
}

// FileListener returns a copy of the network listener corresponding to the open
// file f.
func FileListener(f *os.File) (Listener, error) {
	fd, sotype, lsa, err := adoptFD(f)
	if err != nil {
		return nil, err
	}
	if acc, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ACCEPTCONN); err != nil || acc == 0 || sotype != syscall.SOCK_STREAM {
		syscall.Close(fd)
		return nil, errFileType
	}
	switch lsa.(type) {
	case *syscall.SockaddrInet4, *syscall.SockaddrInet6:
		return &TCPListener{listener{fd: fd, laddr: TCPAddrFromAddrPort(addrPortFromSockaddr(lsa))}}, nil
	case *syscall.SockaddrUnix:
		return &UnixListener{fd: fd, laddr: unixAddrFromSockaddr(lsa)}, nil
	}
	syscall.Close(fd)
	return nil, errFileType
}

// FilePacketConn returns a copy of the packet network connection
// corresponding to the open file f.
func FilePacketConn(f *os.File) (PacketConn, error) {
	c, err := FileConn(f)
	if err != nil {
		return nil, err
	}
	if pc, ok := c.(PacketConn); ok {
		return pc, nil
	}
	c.Close()
	return nil, errFileType
}

func dupFile(fd int, name string) (*os.File, error) {
	nfd, err := syscall.Dup(fd)
	if err != nil {
		return nil, os.NewSyscallError("dup", err)
	}
	syscall.CloseOnExec(nfd)
	return os.NewFile(uintptr(nfd), name), nil
}

// File returns a copy of the underlying os.File.
func (l *TCPListener) File() (*os.File, error) { return dupFile(l.fd, "tcp") }
