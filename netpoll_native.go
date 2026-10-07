//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: a small epoll poller for the host netdev. A goroutine that would
// block parks here until the fd is ready, its deadline passes, or it closes.

package net

import (
	"errors"
	"sync"
	"syscall"
	"time"
)

// errPollClosed is returned to goroutines parked on an fd when that fd is closed.
var errPollClosed = errors.New("net: use of closed network connection")

// pollDesc holds the goroutines currently waiting on one fd, split by direction.
type pollDesc struct {
	fd      int
	readers []chan error
	writers []chan error
	inEpoll bool

	// Records an interrupt that arrived with no waiter parked, so the next
	// wait in that direction returns at once instead of using a stale deadline.
	readInterrupt  bool
	writeInterrupt bool
}

type netPoller struct {
	once sync.Once
	err  error // set if the poller failed to initialize
	epfd int

	mu  sync.Mutex
	fds map[int]*pollDesc
}

var poller netPoller

func (p *netPoller) init() {
	p.once.Do(func() {
		epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
		if err != nil {
			p.err = err
			return
		}
		p.epfd = epfd
		p.fds = make(map[int]*pollDesc)
		go p.loop()
	})
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
	event := &syscall.EpollEvent{Events: ev, Fd: int32(pd.fd)}
	if pd.inEpoll {
		// ENOENT means the fd was closed without close() and its number reused.
		if syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, pd.fd, event) != syscall.ENOENT {
			return
		}
		pd.inEpoll = false
	}
	if err := syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, pd.fd, event); err == nil {
		pd.inEpoll = true
	}
}

// wait blocks until fd is ready in the requested direction (write=EPOLLOUT,
// otherwise EPOLLIN), the deadline expires, or the fd is closed.
func (p *netPoller) wait(fd int, write bool, deadline time.Time) error {
	p.init()
	if p.err != nil {
		return p.err
	}

	ch := make(chan error, 1)

	p.mu.Lock()
	pd := p.fds[fd]
	if pd == nil {
		pd = &pollDesc{fd: fd}
		p.fds[fd] = pd
	}
	if (write && pd.writeInterrupt) || (!write && pd.readInterrupt) {
		// An interrupt arrived before we parked. Consume it and let the caller
		// re-evaluate its deadline.
		if write {
			pd.writeInterrupt = false
		} else {
			pd.readInterrupt = false
		}
		p.arm(pd)
		p.mu.Unlock()
		return errPollInterrupted
	}
	if write {
		pd.writers = append(pd.writers, ch)
	} else {
		pd.readers = append(pd.readers, ch)
	}
	p.arm(pd)
	p.mu.Unlock()

	var timeout <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			p.cancelWaiter(fd, write, ch)
			return timeoutError{}
		}
		t := time.NewTimer(d)
		defer t.Stop()
		timeout = t.C
	}

	select {
	case err := <-ch:
		return err
	case <-timeout:
		p.cancelWaiter(fd, write, ch)
		return timeoutError{}
	}
}

// cancelWaiter removes a single waiter channel that gave up (deadline expired)
// before the poller signalled it.
func (p *netPoller) cancelWaiter(fd int, write bool, ch chan error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pd := p.fds[fd]
	if pd == nil {
		return
	}
	if write {
		pd.writers = removeChan(pd.writers, ch)
	} else {
		pd.readers = removeChan(pd.readers, ch)
	}
	p.arm(pd)
}

// interrupt wakes every goroutine parked on fd in the given direction so it
// re-evaluates its deadline. net/http's abortPendingRead relies on this.
func (p *netPoller) interrupt(fd int, write bool) {
	if p.err != nil {
		return
	}
	p.mu.Lock()
	if p.fds == nil {
		// The poller never started, so nothing can be parked. Any later
		// wait() captures the new deadline anyway.
		p.mu.Unlock()
		return
	}
	pd := p.fds[fd]
	if pd == nil {
		pd = &pollDesc{fd: fd}
		p.fds[fd] = pd
	}
	var chs []chan error
	if write {
		chs, pd.writers = pd.writers, nil
		if len(chs) == 0 {
			pd.writeInterrupt = true
		}
	} else {
		chs, pd.readers = pd.readers, nil
		if len(chs) == 0 {
			pd.readInterrupt = true
		}
	}
	p.arm(pd)
	p.mu.Unlock()

	for _, ch := range chs {
		ch <- errPollInterrupted
	}
}

// close wakes every goroutine parked on fd with errPollClosed and stops polling
// it. It must be called just before the fd is actually closed.
func (p *netPoller) close(fd int) {
	if p.err != nil {
		return
	}
	p.mu.Lock()
	pd := p.fds[fd]
	if pd == nil {
		p.mu.Unlock()
		return
	}
	if pd.inEpoll {
		syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_DEL, fd, nil)
	}
	delete(p.fds, fd)
	readers, writers := pd.readers, pd.writers
	pd.readers, pd.writers = nil, nil
	p.mu.Unlock()

	for _, ch := range readers {
		ch <- errPollClosed
	}
	for _, ch := range writers {
		ch <- errPollClosed
	}
}

// loop is the poller's background goroutine. It waits for epoll events
// and wakes the corresponding parked goroutines.
func (p *netPoller) loop() {
	events := make([]syscall.EpollEvent, 64)
	for {
		n, err := syscall.EpollWait(p.epfd, events, -1)
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
				for _, ch := range pd.readers {
					ch <- nil
				}
				pd.readers = nil
			}
			if hup || e.Events&syscall.EPOLLOUT != 0 {
				for _, ch := range pd.writers {
					ch <- nil
				}
				pd.writers = nil
			}
			// EPOLLONESHOT disabled the fd, so re-arm for any remaining waiters.
			pd.inEpoll = true // it is still registered, just disarmed
			p.arm(pd)
		}
		p.mu.Unlock()
	}
}

func removeChan(s []chan error, ch chan error) []chan error {
	for i, c := range s {
		if c == ch {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
