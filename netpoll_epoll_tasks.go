//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm && (scheduler.tasks || scheduler.cores)

// TINYGO: goroutines share one thread, so the poller goroutine parks in the
// runtime poller until its epoll fd is readable, then collects the events.

package net

import "syscall"

func epollWait(epfd int, events []syscall.EpollEvent) (int, error) {
	runtime_waitFD(int32(epfd), false, -1)
	return syscall.EpollWait(epfd, events, 0)
}

func runtime_waitFD(fd int32, write bool, timeout int64) bool
