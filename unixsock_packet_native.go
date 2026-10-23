//go:build (linux || darwin) && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"os"
	"syscall"
)

// File returns a copy of the underlying os.File.
func (l *UnixListener) File() (*os.File, error) { return dupFile(l.fd, "unix") }

// ReadFromUnix acts like ReadFrom but returns a UnixAddr.
func (c *UnixConn) ReadFromUnix(b []byte) (int, *UnixAddr, error) {
	for {
		if expired(c.readDeadline) {
			return 0, nil, &OpError{Op: "read", Net: "unix", Addr: c.laddr, Err: timeoutError{}}
		}
		n, from, err := syscall.Recvfrom(c.fd, b, 0)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(c.fd, false, c.readDeadline); werr != nil && werr != errPollInterrupted {
				return 0, nil, &OpError{Op: "read", Net: "unix", Addr: c.laddr, Err: werr}
			}
			continue
		}
		if err != nil {
			return 0, nil, &OpError{Op: "read", Net: "unix", Addr: c.laddr, Err: err}
		}
		var addr *UnixAddr
		if sa, ok := from.(*syscall.SockaddrUnix); ok && sa.Name != "" {
			addr = &UnixAddr{Name: sa.Name, Net: "unixgram"}
		}
		return n, addr, nil
	}
}

// ReadFrom implements the PacketConn ReadFrom method.
func (c *UnixConn) ReadFrom(b []byte) (int, Addr, error) {
	n, addr, err := c.ReadFromUnix(b)
	if addr == nil {
		return n, nil, err
	}
	return n, addr, err
}

// WriteToUnix acts like WriteTo but takes a UnixAddr.
func (c *UnixConn) WriteToUnix(b []byte, addr *UnixAddr) (int, error) {
	if addr == nil {
		return 0, &OpError{Op: "write", Net: "unix", Err: errMissingAddress}
	}
	sa := &syscall.SockaddrUnix{Name: addr.Name}
	for {
		if expired(c.writeDeadline) {
			return 0, &OpError{Op: "write", Net: "unix", Addr: addr, Err: timeoutError{}}
		}
		err := syscall.Sendto(c.fd, b, 0, sa)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(c.fd, true, c.writeDeadline); werr != nil && werr != errPollInterrupted {
				return 0, &OpError{Op: "write", Net: "unix", Addr: addr, Err: werr}
			}
			continue
		}
		if err != nil {
			return 0, &OpError{Op: "write", Net: "unix", Addr: addr, Err: err}
		}
		return len(b), nil
	}
}

// WriteTo implements the PacketConn WriteTo method.
func (c *UnixConn) WriteTo(b []byte, addr Addr) (int, error) {
	a, ok := addr.(*UnixAddr)
	if !ok {
		return 0, &OpError{Op: "write", Net: "unix", Addr: addr, Err: syscall.EINVAL}
	}
	return c.WriteToUnix(b, a)
}
