// TINYGO: The following is copied and modified from Go 1.26.2 official implementation.

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import "time"

// BUG(mikio): On JS, WASIP1 and Plan 9, methods and functions related
// to UnixConn and UnixListener are not implemented.

// BUG(mikio): On Windows, methods and functions related to UnixConn
// and UnixListener don't work for "unixgram" and "unixpacket".

// BUG(paralin): On TinyGo, Unix sockets are implemented on the host target only.

// UnixAddr represents the address of a Unix domain socket end point.
type UnixAddr struct {
	Name string
	Net  string
}

// Network returns the address's network name, "unix", "unixgram" or
// "unixpacket".
func (a *UnixAddr) Network() string {
	return a.Net
}

func (a *UnixAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	return a.Name
}

func (a *UnixAddr) isWildcard() bool {
	return a == nil || a.Name == ""
}

func (a *UnixAddr) opAddr() Addr {
	if a == nil {
		return nil
	}
	return a
}

// ResolveUnixAddr returns an address of Unix domain socket end point.
//
// The network must be a Unix network name.
//
// See func [Dial] for a description of the network and address
// parameters.
func ResolveUnixAddr(network, address string) (*UnixAddr, error) {
	switch network {
	case "unix", "unixgram", "unixpacket":
		return &UnixAddr{Name: address, Net: network}, nil
	default:
		return nil, UnknownNetworkError(network)
	}
}

// UnixConn is an implementation of the Conn interface for connections to Unix
// domain sockets. On the host target it is backed by a real socket; elsewhere
// every method returns an error (unixsock_stub.go).
type UnixConn struct {
	closer        closeGuard
	fd            int
	laddr         *UnixAddr
	raddr         *UnixAddr
	readDeadline  time.Time
	writeDeadline time.Time
}

// UnixListener is a Unix domain socket listener.
type UnixListener struct {
	closer closeGuard
	fd     int
	laddr  *UnixAddr
	path   string
	unlink bool
}

func (c *UnixConn) LocalAddr() Addr  { return c.laddr }
func (c *UnixConn) RemoteAddr() Addr { return c.raddr }

// Addr returns the listener's network address.
func (l *UnixListener) Addr() Addr { return l.laddr }

// SetDeadline sets the deadline associated with the listener.
//
// TINYGO: no-op, Accept has no deadline support.
func (l *UnixListener) SetDeadline(t time.Time) error { return nil }

// SetUnlinkOnClose sets whether the underlying socket file should be removed
// from the file system when the listener is closed.
func (l *UnixListener) SetUnlinkOnClose(unlink bool) { l.unlink = unlink }
