//go:build linux && !baremetal && !tinygo.wasm

package net

import (
	"testing"
	"time"
)

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
