//go:build !js

package http

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// upgradeServer answers one request with a 101 and the given early bytes in
// the same write, then echoes whatever the client sends prefixed with "echo:".
func upgradeServer(t *testing.T, early string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := ReadRequest(br)
		if err != nil || !isProtocolSwitchHeader(req.Header) {
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"+early)
		buf := make([]byte, 64)
		for {
			n, err := br.Read(buf)
			if err != nil {
				return
			}
			c.Write(append([]byte("echo:"), buf[:n]...))
		}
	}()
	return l.Addr().String()
}

func TestClientProtocolSwitchBodyIsReadWriteCloser(t *testing.T) {
	addr := upgradeServer(t, "hello")
	ctx, cancel := context.WithCancel(context.Background())
	req, err := NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	tr := &Transport{ResponseHeaderTimeout: 200 * time.Millisecond}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != StatusSwitchingProtocols {
		t.Fatalf("status %d, want 101", resp.StatusCode)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatalf("101 response body %T is not an io.ReadWriteCloser", resp.Body)
	}
	defer rwc.Close()

	// The caller owns the conn now, so neither ctx nor the header timeout applies.
	cancel()
	time.Sleep(300 * time.Millisecond)

	readN := func(want string) {
		t.Helper()
		got := make([]byte, len(want))
		if _, err := io.ReadFull(rwc, got); err != nil {
			t.Fatalf("read %q: %v", want, err)
		}
		if string(got) != want {
			t.Fatalf("read %q, want %q", got, want)
		}
	}
	readN("hello")
	if _, err := io.WriteString(rwc, "ping"); err != nil {
		t.Fatal(err)
	}
	readN("echo:ping")
}
