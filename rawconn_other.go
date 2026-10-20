//go:build !((linux || darwin) && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm)

package net

import (
	"errors"
	"syscall"
	"time"
)

func newRawConn(fd int, rd, wd *time.Time) (syscall.RawConn, error) {
	return nil, errors.New("SyscallConn not implemented")
}
