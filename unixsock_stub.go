//go:build !(linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm) && !(js && wasm)

package net

import (
	"errors"
	"time"
)

// TINYGO: Unix sockets exist on the host target only (unixsock_native.go).
// Elsewhere these keep type assertions and references compiling.

var errUnixNotImplemented = errors.New("net: Unix sockets not implemented")

func (c *UnixConn) Read(b []byte) (int, error)         { return 0, errUnixNotImplemented }
func (c *UnixConn) Write(b []byte) (int, error)        { return 0, errUnixNotImplemented }
func (c *UnixConn) Close() error                       { return errUnixNotImplemented }
func (c *UnixConn) CloseRead() error                   { return errUnixNotImplemented }
func (c *UnixConn) CloseWrite() error                  { return errUnixNotImplemented }
func (c *UnixConn) SetDeadline(t time.Time) error      { return errUnixNotImplemented }
func (c *UnixConn) SetReadDeadline(t time.Time) error  { return errUnixNotImplemented }
func (c *UnixConn) SetWriteDeadline(t time.Time) error { return errUnixNotImplemented }

// ReadMsgUnix is not implemented on this target.
func (c *UnixConn) ReadMsgUnix(b, oob []byte) (n, oobn, flags int, addr *UnixAddr, err error) {
	return 0, 0, 0, nil, errUnixNotImplemented
}

// WriteMsgUnix is not implemented on this target.
func (c *UnixConn) WriteMsgUnix(b, oob []byte, addr *UnixAddr) (n, oobn int, err error) {
	return 0, 0, errUnixNotImplemented
}

// DialUnix is not implemented on this target.
func DialUnix(network string, laddr, raddr *UnixAddr) (*UnixConn, error) {
	return nil, errUnixNotImplemented
}

// ListenUnix is not implemented on this target.
func ListenUnix(network string, laddr *UnixAddr) (*UnixListener, error) {
	return nil, errUnixNotImplemented
}

func (l *UnixListener) Accept() (Conn, error)          { return nil, errUnixNotImplemented }
func (l *UnixListener) AcceptUnix() (*UnixConn, error) { return nil, errUnixNotImplemented }
func (l *UnixListener) Close() error                   { return errUnixNotImplemented }
