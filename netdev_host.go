//go:build (linux || darwin) && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: the parts of the host netdev that do not depend on the OS. The
// socket calls are in netdev_native.go.

package net

import (
	"net/netip"
	"os"
	"syscall"
	"time"
)

// Register the host netdev as the default. A network driver (e.g. on a board
// that also reports GOOS=linux, which doesn't happen today) could still replace
// it by calling useNetdev() from its own init/setup.
func init() {
	useNetdev(&hostNetdev{})
}

// hostNetdev implements netdever with the host OS sockets via the syscall
// package. The "sockfd" values it returns are OS socket handles.
//
// Sockets are non-blocking. A goroutine that would block parks in the poller
// (netpoll_*.go) until the socket is ready, its deadline passes, or it closes.
type hostNetdev struct{}

// timeoutError is returned from Send/Recv when a deadline expires. It satisfies
// the net.Error interface so callers (e.g. net/http) can detect timeouts.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// Is matches os.ErrDeadlineExceeded, as Go's timeout errors do.
func (timeoutError) Is(target error) bool { return target == os.ErrDeadlineExceeded }

// addrPortFromSockaddr converts a syscall.Sockaddr, as returned by
// Accept/Getsockname, into a netip.AddrPort.
func addrPortFromSockaddr(sa syscall.Sockaddr) netip.AddrPort {
	switch s := sa.(type) {
	case *syscall.SockaddrInet4:
		return netip.AddrPortFrom(netip.AddrFrom4(s.Addr), uint16(s.Port))
	case *syscall.SockaddrInet6:
		return netip.AddrPortFrom(netip.AddrFrom16(s.Addr), uint16(s.Port))
	}
	return netip.AddrPort{}
}

// toInt coerces the values the net package passes to SetSockOpt (int, bool, or
// float64 durations) into an int.
func toInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

// sockaddr builds a syscall.Sockaddr from an AddrPort: a SockaddrInet6 for an
// IPv6 address, otherwise a SockaddrInet4 (an invalid/zero address maps to the
// 0.0.0.0 wildcard used when binding).
func sockaddr(ip netip.AddrPort) syscall.Sockaddr {
	return sockaddrFromParts(ip.Addr(), ip.Port())
}

func sockaddrFromParts(addr netip.Addr, port uint16) syscall.Sockaddr {
	// As4/As16 panic on a wrongly-sized address, so dispatch on the family.
	if addr = addr.Unmap(); addr.Is6() {
		// Link-local zones (%zone) are not resolved to a scope id.
		return &syscall.SockaddrInet6{Port: int(port), Addr: addr.As16()}
	}
	sa := &syscall.SockaddrInet4{Port: int(port)}
	if addr.Is4() {
		sa.Addr = addr.As4()
	}
	return sa
}

// expired reports whether a non-zero deadline is already in the past.
func expired(deadline time.Time) bool {
	return !deadline.IsZero() && !deadline.After(time.Now())
}
