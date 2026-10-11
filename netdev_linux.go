//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import "syscall"

// msgNoSignal makes a send to a closed peer return EPIPE instead of raising
// SIGPIPE.
const msgNoSignal = syscall.MSG_NOSIGNAL

func sysSocket(domain, stype, protocol int) (int, error) {
	return syscall.Socket(domain, stype|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, protocol)
}

func sysAccept(fd int) (int, syscall.Sockaddr, error) {
	return syscall.Accept4(fd, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)
}

// The netdev constants in netdev.go are the Linux values.
func hostDomain(domain int) int { return domain }

func hostSockopt(level, opt int) (int, int) { return level, opt }
