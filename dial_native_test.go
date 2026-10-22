//go:build linux && !baremetal && !tinygo.wasm

package net

import (
	"syscall"
	"testing"
)

func tcpNoDelay(t *testing.T, c Conn) int {
	t.Helper()
	rc, err := c.(*TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	v := -1
	rc.Control(func(fd uintptr) {
		v, err = syscall.GetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_NODELAY)
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTCPNoDelay(t *testing.T) {
	client, server := ephemeralTestPair(t)
	defer client.Close()
	defer server.Close()
	if tcpNoDelay(t, client) == 0 || tcpNoDelay(t, server) == 0 {
		t.Fatal("TCP_NODELAY is not set by default")
	}
	if err := client.(*TCPConn).SetNoDelay(false); err != nil {
		t.Fatal(err)
	}
	if tcpNoDelay(t, client) != 0 {
		t.Fatal("SetNoDelay(false) did not clear TCP_NODELAY")
	}
}
