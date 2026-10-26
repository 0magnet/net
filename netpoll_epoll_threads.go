//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm && !scheduler.tasks && !scheduler.cores

// TINYGO: the poller goroutine has its own thread and blocks in epoll_wait.

package net

import "syscall"

func epollWait(epfd int, events []syscall.EpollEvent) (int, error) {
	return syscall.EpollWait(epfd, events, -1)
}
