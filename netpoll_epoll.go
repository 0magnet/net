//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: the epoll part of the host poller (netpoll_host.go).

package net

import "syscall"

type pollBackend struct {
	epfd int
	ev   syscall.EpollEvent // scratch for arm, guarded by mu
}

func (p *netPoller) open() error {
	epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return err
	}
	p.be.epfd = epfd
	return nil
}

// unregister stops polling pd. Must be called with p.mu held.
func (p *netPoller) unregister(pd *pollDesc) {
	if pd.registered {
		syscall.EpollCtl(p.be.epfd, syscall.EPOLL_CTL_DEL, pd.fd, nil)
	}
}

// events returns the epoll interest mask for pd given its current waiters.
func (pd *pollDesc) events() uint32 {
	var ev uint32
	if len(pd.readers) != 0 {
		ev |= syscall.EPOLLIN | syscall.EPOLLRDHUP
	}
	if len(pd.writers) != 0 {
		ev |= syscall.EPOLLOUT
	}
	if ev != 0 {
		ev |= syscall.EPOLLONESHOT
	}
	return ev
}

// arm (re)programs epoll for pd. Must be called with p.mu held.
func (p *netPoller) arm(pd *pollDesc) {
	ev := pd.events()
	if ev == 0 {
		// Keep pd and the registration for the next wait. EPOLLONESHOT means an
		// unwanted event fires at most once.
		return
	}
	p.be.ev = syscall.EpollEvent{Events: ev, Fd: int32(pd.fd)}
	if pd.registered {
		// ENOENT means the fd was closed without close() and its number reused.
		if syscall.EpollCtl(p.be.epfd, syscall.EPOLL_CTL_MOD, pd.fd, &p.be.ev) != syscall.ENOENT {
			return
		}
		pd.registered = false
	}
	if err := syscall.EpollCtl(p.be.epfd, syscall.EPOLL_CTL_ADD, pd.fd, &p.be.ev); err == nil {
		pd.registered = true
	}
}

// loop is the poller's background goroutine. It waits for epoll events
// and wakes the corresponding parked goroutines.
func (p *netPoller) loop() {
	events := make([]syscall.EpollEvent, 64)
	for {
		n, err := syscall.EpollWait(p.be.epfd, events, -1)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return
		}
		p.mu.Lock()
		for i := 0; i < n; i++ {
			e := events[i]
			pd := p.fds[int(e.Fd)]
			if pd == nil {
				continue
			}
			// On error/hangup, wake everyone so they observe the real result.
			hup := e.Events&(syscall.EPOLLERR|syscall.EPOLLHUP|syscall.EPOLLRDHUP) != 0
			if hup || e.Events&syscall.EPOLLIN != 0 {
				pd.readers = wake(pd.readers, nil)
			}
			if hup || e.Events&syscall.EPOLLOUT != 0 {
				pd.writers = wake(pd.writers, nil)
			}
			// EPOLLONESHOT disabled the fd, so re-arm for any remaining waiters.
			pd.registered = true // it is still registered, just disarmed
			p.arm(pd)
		}
		p.mu.Unlock()
	}
}
