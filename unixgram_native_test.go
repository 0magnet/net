//go:build linux && !baremetal && !tinygo.wasm

package net

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// sdNotify sends payload the way Caddy's notify package does.
func sdNotify(path, payload string) error {
	addr := &UnixAddr{Name: path, Net: "unixgram"}
	conn, err := DialUnix(addr.Net, nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(payload))
	return err
}

func testUnixgramNotify(t *testing.T, path string) {
	pc, err := ListenPacket("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	pc.SetReadDeadline(time.Now().Add(5 * time.Second))

	buf := make([]byte, 64)
	for _, msg := range []string{"READY=1", "STOPPING=1"} {
		if err := sdNotify(path, msg); err != nil {
			t.Fatalf("notify %s: %v", msg, err)
		}
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(buf[:n]); got != msg {
			t.Fatalf("got %q, want %q", got, msg)
		}
	}

	c, err := Dial("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("WATCHDOG=1")); err != nil {
		t.Fatal(err)
	}
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "WATCHDOG=1" {
		t.Fatalf("got %q, want WATCHDOG=1", got)
	}
}

func TestUnixgramNotifyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.sock")
	testUnixgramNotify(t, path)
	os.Remove(path)
}

func TestUnixgramNotifyAbstract(t *testing.T) {
	name := "@tinygo-net-notify-" + strconv.Itoa(os.Getpid())
	testUnixgramNotify(t, name)
	if _, err := os.Stat(name); err == nil {
		os.Remove(name)
		t.Fatal("abstract name was bound as a file")
	}
}

func TestUnixgramReplyTo(t *testing.T) {
	dir := t.TempDir()
	srv, err := ListenUnixgram("unixgram", &UnixAddr{Name: filepath.Join(dir, "srv"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cli, err := ListenUnixgram("unixgram", &UnixAddr{Name: filepath.Join(dir, "cli"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv.SetDeadline(time.Now().Add(5 * time.Second))
	cli.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := cli.WriteTo([]byte("ping"), srv.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	n, from, err := srv.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ping" || from == nil || from.Name != filepath.Join(dir, "cli") {
		t.Fatalf("got %q from %v", buf[:n], from)
	}
	if _, err := srv.WriteToUnix([]byte("pong"), from); err != nil {
		t.Fatal(err)
	}
	n, err = cli.Read(buf)
	if err != nil || string(buf[:n]) != "pong" {
		t.Fatalf("got %q, %v", buf[:n], err)
	}
}

func TestListenUnixgramRejectsStream(t *testing.T) {
	if _, err := ListenUnix("unixgram", &UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unixgram"}); err == nil {
		t.Fatal("ListenUnix accepted unixgram")
	}
}
