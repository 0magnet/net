//go:build (linux || darwin) && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"syscall"
	"time"
)

// rawConn exposes a host socket's file descriptor, as syscall.RawConn does in
// Go. Read and Write park on the poller until the callback reports done.
type rawConn struct {
	fd int
	// rd and wd point at the owning conn's deadlines, which Read and Write
	// honour as Go's do. They are nil for a listener's Control hook.
	rd, wd *time.Time
}

func newRawConn(fd int, rd, wd *time.Time) (syscall.RawConn, error) {
	return &rawConn{fd: fd, rd: rd, wd: wd}, nil
}

func (c *rawConn) Control(f func(fd uintptr)) error {
	f(uintptr(c.fd))
	return nil
}

func (c *rawConn) Read(f func(fd uintptr) bool) error {
	return c.wait(f, false, c.rd)
}

func (c *rawConn) Write(f func(fd uintptr) bool) error {
	return c.wait(f, true, c.wd)
}

// wait calls f until it reports done. A deadline change only wakes the wait,
// as in Go, so it retries under the new deadline instead of failing.
func (c *rawConn) wait(f func(fd uintptr) bool, write bool, deadline *time.Time) error {
	for {
		var d time.Time
		if deadline != nil {
			d = *deadline
		}
		if !d.IsZero() && !time.Now().Before(d) {
			return timeoutError{}
		}
		if f(uintptr(c.fd)) {
			return nil
		}
		if err := poller.wait(c.fd, write, d); err != nil && err != errPollInterrupted {
			return err
		}
	}
}
