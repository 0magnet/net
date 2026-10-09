//go:build js && wasm

package net

import (
	"errors"
	"io"
	"time"
)

// TINYGO: stream Unix domain sockets on js, held by the in-process netdev
// (netdev_js.go). Datagram and seqpacket networks are not supported.

var errMissingAddress = errors.New("missing address")

var errUnixMsgNotImplemented = errors.New("net: ReadMsgUnix and WriteMsgUnix not implemented on js")

func loopDev() *loopNetdev { return netdev.(*loopNetdev) }

// DialUnix acts like [Dial] for Unix networks.
func DialUnix(network string, laddr, raddr *UnixAddr) (*UnixConn, error) {
	if network != "unix" {
		return nil, &OpError{Op: "dial", Net: network, Err: UnknownNetworkError(network)}
	}
	if raddr == nil {
		return nil, &OpError{Op: "dial", Net: network, Err: errMissingAddress}
	}
	fd, err := netdev.Socket(_AF_UNIX, _SOCK_STREAM, 0)
	if err != nil {
		return nil, &OpError{Op: "dial", Net: network, Addr: raddr, Err: err}
	}
	if laddr != nil && laddr.Name != "" {
		if err := loopDev().bindUnix(fd, laddr.Name); err != nil {
			netdev.Close(fd)
			return nil, &OpError{Op: "dial", Net: network, Addr: raddr, Err: err}
		}
	}
	if err := loopDev().connectUnix(fd, raddr.Name); err != nil {
		netdev.Close(fd)
		return nil, &OpError{Op: "dial", Net: network, Addr: raddr, Err: err}
	}
	return &UnixConn{fd: fd, laddr: laddr, raddr: raddr}, nil
}

// ListenUnix acts like [Listen] for Unix networks.
func ListenUnix(network string, laddr *UnixAddr) (*UnixListener, error) {
	if network != "unix" {
		return nil, &OpError{Op: "listen", Net: network, Err: UnknownNetworkError(network)}
	}
	if laddr == nil || laddr.Name == "" {
		return nil, &OpError{Op: "listen", Net: network, Err: errMissingAddress}
	}
	fd, err := netdev.Socket(_AF_UNIX, _SOCK_STREAM, 0)
	if err != nil {
		return nil, &OpError{Op: "listen", Net: network, Addr: laddr, Err: err}
	}
	if err := loopDev().bindUnix(fd, laddr.Name); err != nil {
		netdev.Close(fd)
		return nil, &OpError{Op: "listen", Net: network, Addr: laddr, Err: err}
	}
	if err := netdev.Listen(fd, loopBacklogMax); err != nil {
		netdev.Close(fd)
		return nil, &OpError{Op: "listen", Net: network, Addr: laddr, Err: err}
	}
	return &UnixListener{fd: fd, laddr: laddr, path: laddr.Name}, nil
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
func (l *UnixListener) Close() error { return l.closer.close(l.fd) }

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
func (c *UnixConn) CloseRead() error { return nil }

// CloseWrite shuts down the writing side of the Unix domain connection.
func (c *UnixConn) CloseWrite() error { return loopDev().shutdownWrite(c.fd) }

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

// ReadMsgUnix is not implemented on js.
func (c *UnixConn) ReadMsgUnix(b, oob []byte) (n, oobn, flags int, addr *UnixAddr, err error) {
	return 0, 0, 0, nil, errUnixMsgNotImplemented
}

// WriteMsgUnix is not implemented on js.
func (c *UnixConn) WriteMsgUnix(b, oob []byte, addr *UnixAddr) (n, oobn int, err error) {
	return 0, 0, errUnixMsgNotImplemented
}
