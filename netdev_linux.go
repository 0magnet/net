//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

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

// maxListenerBacklog reads the system limit, as Go does.
func maxListenerBacklog() int {
	b, err := os.ReadFile("/proc/sys/net/core/somaxconn")
	if err != nil {
		return syscall.SOMAXCONN
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 0 {
		return syscall.SOMAXCONN
	}
	return n
}
