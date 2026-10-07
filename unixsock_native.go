//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

var errMissingAddress = errors.New("missing address")

// TINYGO: stream Unix domain sockets on the host target, on the same
// non-blocking fd and poller the TCP code uses (netdev_native.go). Datagram and
// seqpacket networks are not supported.

func unixSockaddr(a *UnixAddr) *syscall.SockaddrUnix {
	if a == nil {
		return nil
	}
	return &syscall.SockaddrUnix{Name: a.Name}
}

// DialUnix acts like [Dial] for Unix networks.
func DialUnix(network string, laddr, raddr *UnixAddr) (*UnixConn, error) {
	if network != "unix" {
		return nil, &OpError{Op: "dial", Net: network, Err: UnknownNetworkError(network)}
	}
	if raddr == nil {
		return nil, &OpError{Op: "dial", Net: network, Err: errMissingAddress}
	}
	fd, err := netdev.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, &OpError{Op: "dial", Net: network, Addr: raddr, Err: err}
	}
	if laddr != nil && laddr.Name != "" {
		if err := syscall.Bind(fd, unixSockaddr(laddr)); err != nil {
			netdev.Close(fd)
			return nil, &OpError{Op: "dial", Net: network, Addr: raddr, Err: err}
		}
	}
	if err := unixConnect(fd, unixSockaddr(raddr)); err != nil {
		netdev.Close(fd)
		return nil, &OpError{Op: "dial", Net: network, Addr: raddr, Err: err}
	}
	return &UnixConn{fd: fd, laddr: laddr, raddr: raddr}, nil
}

func unixConnect(fd int, sa *syscall.SockaddrUnix) error {
	for {
		err := syscall.Connect(fd, sa)
		switch err {
		case nil:
			return nil
		case syscall.EINTR:
			continue
		case syscall.EINPROGRESS, syscall.EALREADY, syscall.EAGAIN:
			if werr := poller.wait(fd, true, time.Time{}); werr != nil {
				return werr
			}
			soErr, gerr := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ERROR)
			if gerr != nil {
				return gerr
			}
			if soErr != 0 {
				return syscall.Errno(soErr)
			}
			return nil
		default:
			return err
		}
	}
}

// ListenUnix acts like [Listen] for Unix networks.
func ListenUnix(network string, laddr *UnixAddr) (*UnixListener, error) {
	if network != "unix" {
		return nil, &OpError{Op: "listen", Net: network, Err: UnknownNetworkError(network)}
	}
	if laddr == nil || laddr.Name == "" {
		return nil, &OpError{Op: "listen", Net: network, Err: errMissingAddress}
	}
	fd, err := netdev.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, &OpError{Op: "listen", Net: network, Addr: laddr, Err: err}
	}
	if err := syscall.Bind(fd, unixSockaddr(laddr)); err != nil {
		netdev.Close(fd)
		return nil, &OpError{Op: "listen", Net: network, Addr: laddr, Err: err}
	}
	if err := netdev.Listen(fd, syscall.SOMAXCONN); err != nil {
		netdev.Close(fd)
		return nil, &OpError{Op: "listen", Net: network, Addr: laddr, Err: err}
	}
	// As in Go, a listener that created its socket file removes it on Close.
	return &UnixListener{fd: fd, laddr: laddr, path: laddr.Name, unlink: laddr.Name[0] != '@'}, nil
}

// AcceptUnix accepts the next incoming call and returns the new connection.
func (l *UnixListener) AcceptUnix() (*UnixConn, error) {
	fd, _, err := netdev.Accept(l.fd)
	if err != nil {
		return nil, &OpError{Op: "accept", Net: "unix", Addr: l.laddr, Err: err}
	}
	return &UnixConn{fd: fd, laddr: l.laddr, raddr: &UnixAddr{Net: "unix"}}, nil
}

// Accept implements the Accept method in the [Listener] interface.
func (l *UnixListener) Accept() (Conn, error) {
	c, err := l.AcceptUnix()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Close stops listening on the Unix address. Already accepted connections are
// not closed.
func (l *UnixListener) Close() error {
	err := l.closer.close(l.fd)
	if l.unlink && l.path != "" {
		syscall.Unlink(l.path)
	}
	return err
}

func (c *UnixConn) Read(b []byte) (int, error) {
	n, err := netdev.Recv(c.fd, b, 0, c.readDeadline)
	for err == errPollInterrupted {
		n, err = netdev.Recv(c.fd, b, 0, c.readDeadline)
	}
	if n < 0 {
		n = 0
	}
	if err != nil && err != io.EOF {
		return n, &OpError{Op: "read", Net: "unix", Addr: c.raddr, Err: err}
	}
	return n, err
}

func (c *UnixConn) Write(b []byte) (int, error) {
	total := 0
	for {
		n, err := netdev.Send(c.fd, b[total:], 0, c.writeDeadline)
		if n > 0 {
			total += n
		}
		if err == errPollInterrupted {
			continue
		}
		if err != nil {
			err = &OpError{Op: "write", Net: "unix", Addr: c.raddr, Err: err}
		}
		return total, err
	}
}

func (c *UnixConn) Close() error { return c.closer.close(c.fd) }

// CloseRead shuts down the reading side of the Unix domain connection.
func (c *UnixConn) CloseRead() error { return syscall.Shutdown(c.fd, syscall.SHUT_RD) }

// CloseWrite shuts down the writing side of the Unix domain connection.
func (c *UnixConn) CloseWrite() error { return syscall.Shutdown(c.fd, syscall.SHUT_WR) }

func (c *UnixConn) SetDeadline(t time.Time) error {
	c.readDeadline = t
	c.writeDeadline = t
	pollInterrupt(c.fd, false, t)
	pollInterrupt(c.fd, true, t)
	return nil
}

func (c *UnixConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	pollInterrupt(c.fd, false, t)
	return nil
}

func (c *UnixConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	pollInterrupt(c.fd, true, t)
	return nil
}

// ReadMsgUnix reads a message from c, copying the payload into b and the
// associated out-of-band data into oob. It carries SCM_RIGHTS and the like.
func (c *UnixConn) ReadMsgUnix(b, oob []byte) (n, oobn, flags int, addr *UnixAddr, err error) {
	for {
		if expired(c.readDeadline) {
			return 0, 0, 0, nil, &OpError{Op: "read", Net: "unix", Addr: c.raddr, Err: timeoutError{}}
		}
		n, oobn, flags, _, err = syscall.Recvmsg(c.fd, b, oob, 0)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(c.fd, false, c.readDeadline); werr == errPollInterrupted {
				continue
			} else if werr != nil {
				return 0, 0, 0, nil, &OpError{Op: "read", Net: "unix", Addr: c.raddr, Err: werr}
			}
			continue
		}
		if err != nil {
			return n, oobn, flags, nil, &OpError{Op: "read", Net: "unix", Addr: c.raddr, Err: err}
		}
		if n == 0 && oobn == 0 && len(b) > 0 {
			return 0, 0, flags, nil, io.EOF
		}
		return n, oobn, flags, c.raddr, nil
	}
}

// WriteMsgUnix writes a message to addr via c, copying the payload from b and
// the associated out-of-band data from oob.
func (c *UnixConn) WriteMsgUnix(b, oob []byte, addr *UnixAddr) (n, oobn int, err error) {
	for {
		if expired(c.writeDeadline) {
			return 0, 0, &OpError{Op: "write", Net: "unix", Addr: c.raddr, Err: timeoutError{}}
		}
		n, err = syscall.SendmsgN(c.fd, b, oob, nil, 0)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			if werr := poller.wait(c.fd, true, c.writeDeadline); werr == errPollInterrupted {
				continue
			} else if werr != nil {
				return 0, 0, &OpError{Op: "write", Net: "unix", Addr: c.raddr, Err: werr}
			}
			continue
		}
		if err != nil {
			return n, 0, &OpError{Op: "write", Net: "unix", Addr: c.raddr, Err: err}
		}
		return n, len(oob), nil
	}
}

// File returns a copy of the underlying os.File.
//
// TINYGO: not implemented, as for the other socket types (file.go).
func (c *UnixConn) File() (*os.File, error) { return dupFile(c.fd, "unix") }
