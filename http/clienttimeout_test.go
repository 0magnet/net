//go:build !js

package http

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// silentServer accepts connections and never answers.
func silentServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	return l.Addr().String()
}

func checkTimeout(t *testing.T, err error, start time.Time, want string) {
	t.Helper()
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want a timeout containing %q", err, want)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("timed out after %v", d)
	}
}

func TestClientTimeout(t *testing.T) {
	addr := silentServer(t)
	start := time.Now()
	c := &Client{Timeout: 300 * time.Millisecond}
	_, err := c.Get("http://" + addr + "/")
	checkTimeout(t, err, start, "Client.Timeout exceeded")
}

func TestResponseHeaderTimeout(t *testing.T) {
	addr := silentServer(t)
	start := time.Now()
	c := &Client{Transport: &Transport{ResponseHeaderTimeout: 300 * time.Millisecond}}
	_, err := c.Get("http://" + addr + "/")
	checkTimeout(t, err, start, "timeout awaiting response headers")
}

func TestTLSHandshakeTimeout(t *testing.T) {
	addr := silentServer(t)
	start := time.Now()
	c := &Client{Transport: &Transport{TLSHandshakeTimeout: 300 * time.Millisecond}}
	_, err := c.Get("https://" + addr + "/")
	checkTimeout(t, err, start, "TLS handshake timeout")
}
