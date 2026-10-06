//go:build !(linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm)

package net

import (
	"errors"
	"syscall"
)

func newRawConn(fd int) (syscall.RawConn, error) {
	return nil, errors.New("SyscallConn not implemented")
}
