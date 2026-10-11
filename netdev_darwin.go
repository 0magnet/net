//go:build darwin && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

package net

import "syscall"

// Darwin has no MSG_NOSIGNAL. Each socket sets SO_NOSIGPIPE instead.
const msgNoSignal = 0

// Darwin TCP options from <netinet/tcp.h>.
const (
	darwinTCPKeepalive = 0x10
	darwinTCPKeepintvl = 0x101
	darwinTCPKeepcnt   = 0x102
)

func sysSocket(domain, stype, protocol int) (int, error) {
	fd, err := syscall.Socket(domain, stype, protocol)
	if err != nil {
		return -1, err
	}
	if err := setupSocket(fd); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func sysAccept(fd int) (int, syscall.Sockaddr, error) {
	nfd, sa, err := syscall.Accept(fd)
	if err != nil {
		return -1, nil, err
	}
	if err := setupSocket(nfd); err != nil {
		syscall.Close(nfd)
		return -1, nil, err
	}
	return nfd, sa, nil
}

// setupSocket gives fd what Linux sets with flags at creation.
func setupSocket(fd int) error {
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	return syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_NOSIGPIPE, 1)
}

// hostDomain and hostSockopt map the Linux values in netdev.go to darwin's.
func hostDomain(domain int) int {
	if domain == _AF_INET6 {
		return syscall.AF_INET6
	}
	return domain
}

func hostSockopt(level, opt int) (int, int) {
	switch {
	case level == _SOL_SOCKET && opt == _SO_KEEPALIVE:
		return syscall.SOL_SOCKET, syscall.SO_KEEPALIVE
	case level == _SOL_SOCKET && opt == _SO_LINGER:
		return syscall.SOL_SOCKET, syscall.SO_LINGER
	case level == _SOL_TCP && opt == _TCP_KEEPIDLE:
		return syscall.IPPROTO_TCP, darwinTCPKeepalive
	case level == _SOL_TCP && opt == _TCP_KEEPINTVL:
		return syscall.IPPROTO_TCP, darwinTCPKeepintvl
	case level == _SOL_TCP && opt == _TCP_KEEPCNT:
		return syscall.IPPROTO_TCP, darwinTCPKeepcnt
	}
	return level, opt
}
