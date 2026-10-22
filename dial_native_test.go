//go:build linux && !baremetal && !tinygo.wasm

package net

import (
	"context"
	"errors"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// fullBacklogAddr returns the address of a listener whose accept queue is
// full, so Linux drops further SYNs and a connect to it never completes.
func fullBacklogAddr(t *testing.T) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(sa.(*syscall.SockaddrInet4).Port)
	for i := 0; i < 8; i++ {
		c, err := DialTimeout("tcp4", addr, 200*time.Millisecond)
		if err != nil {
			return addr
		}
		t.Cleanup(func() { c.Close() })
	}
	t.Skip("accept queue never filled")
	return ""
}

func TestDialTimeoutFullBacklog(t *testing.T) {
	addr := fullBacklogAddr(t)
	start := time.Now()
	_, err := DialTimeout("tcp4", addr, 300*time.Millisecond)
	var ne Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("got %v, want a timeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timed out after %v, want about 300ms", d)
	}
}

func TestDialContextCancel(t *testing.T) {
	addr := fullBacklogAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	var d Dialer
	start := time.Now()
	_, err := d.DialContext(ctx, "tcp4", addr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("canceled after %v, want about 200ms", d)
	}
}

func TestListenBacklog(t *testing.T) {
	l, err := Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// Nothing accepts, so every dial must fit in the listen backlog.
	for i := 0; i < 64; i++ {
		c, err := DialTimeout("tcp4", l.Addr().String(), 2*time.Second)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer c.Close()
	}
}

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

func TestDialLocalAddr(t *testing.T) {
	client, server := ephemeralTestPair(t)
	defer client.Close()
	defer server.Close()
	la, _ := client.LocalAddr().(*TCPAddr)
	if la == nil || la.Port == 0 || la.String() != server.RemoteAddr().String() {
		t.Fatalf("dialed LocalAddr %v, peer sees %v", client.LocalAddr(), server.RemoteAddr())
	}
}
