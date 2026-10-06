//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"syscall"
	"time"
)

// rawConn exposes a host socket's file descriptor, as syscall.RawConn does in
// Go. Read and Write park on the poller until the callback reports done.
type rawConn struct{ fd int }

func newRawConn(fd int) (syscall.RawConn, error) { return &rawConn{fd: fd}, nil }

func (c *rawConn) Control(f func(fd uintptr)) error {
	f(uintptr(c.fd))
	return nil
}

func (c *rawConn) Read(f func(fd uintptr) bool) error {
	for !f(uintptr(c.fd)) {
		if err := poller.wait(c.fd, false, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

func (c *rawConn) Write(f func(fd uintptr) bool) error {
	for !f(uintptr(c.fd)) {
		if err := poller.wait(c.fd, true, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}
