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

	// One reusable channel and timer per direction, so the common case of a
	// single waiter allocates nothing. Index 1 is the write direction.
	cache [2]pollWaiter
}

type pollWaiter struct {
	busy bool
	ch   chan error
	t    *time.Timer
}

type netPoller struct {
	once sync.Once
	err  error // set if the poller failed to initialize
	epfd int

	mu  sync.Mutex
	fds map[int]*pollDesc
	ev  syscall.EpollEvent // scratch for arm, guarded by mu
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
	p.ev = syscall.EpollEvent{Events: ev, Fd: int32(pd.fd)}
	if pd.inEpoll {
		// ENOENT means the fd was closed without close() and its number reused.
		if syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, pd.fd, &p.ev) != syscall.ENOENT {
			return
		}
		pd.inEpoll = false
	}
	if err := syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, pd.fd, &p.ev); err == nil {
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
	dir := 0
	if write {
		dir = 1
	}

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
	// Every send to a waiter channel happens under p.mu while it is listed,
	// so draining here clears a value left by a waiter that timed out.
	var w *pollWaiter
	var ch chan error
	if c := &pd.cache[dir]; !c.busy {
		w = c
		w.busy = true
		if w.ch == nil {
			w.ch = make(chan error, 1)
		}
		select {
		case <-w.ch:
		default:
		}
		ch = w.ch
	} else {
		ch = make(chan error, 1)
	}
	if write {
		pd.writers = append(pd.writers, ch)
	} else {
		pd.readers = append(pd.readers, ch)
	}
	p.arm(pd)
	p.mu.Unlock()

	err := p.park(fd, write, deadline, ch, w)
	if w != nil {
		p.mu.Lock()
		w.busy = false
		p.mu.Unlock()
	}
	return err
}

func (p *netPoller) park(fd int, write bool, deadline time.Time, ch chan error, w *pollWaiter) error {
	if deadline.IsZero() {
		return <-ch
	}
	d := time.Until(deadline)
	if d <= 0 {
		p.cancelWaiter(fd, write, ch)
		return timeoutError{}
	}
	var t *time.Timer
	if w == nil {
		t = time.NewTimer(d)
	} else if w.t == nil {
		w.t = time.NewTimer(d)
		t = w.t
	} else {
		t = w.t
		t.Reset(d)
	}
	for {
		select {
		case err := <-ch:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			return err
		case <-t.C:
			// A reused timer can deliver a fire left over from an earlier wait.
			if d := time.Until(deadline); d > 0 {
				t.Reset(d)
				continue
			}
			p.cancelWaiter(fd, write, ch)
			return timeoutError{}
		}
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
	defer p.mu.Unlock()
	if p.fds == nil {
		// The poller never started, so nothing can be parked. Any later
		// wait() captures the new deadline anyway.
		return
	}
	pd := p.fds[fd]
	if pd == nil {
		pd = &pollDesc{fd: fd}
		p.fds[fd] = pd
	}
	if write {
		if len(pd.writers) == 0 {
			pd.writeInterrupt = true
		}
		pd.writers = wake(pd.writers, errPollInterrupted)
	} else {
		if len(pd.readers) == 0 {
			pd.readInterrupt = true
		}
		pd.readers = wake(pd.readers, errPollInterrupted)
	}
	p.arm(pd)
}

// close wakes every goroutine parked on fd with errPollClosed and stops polling
// it. It must be called just before the fd is actually closed.
func (p *netPoller) close(fd int) {
	if p.err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pd := p.fds[fd]
	if pd == nil {
		return
	}
	if pd.inEpoll {
		syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_DEL, fd, nil)
	}
	delete(p.fds, fd)
	pd.readers = wake(pd.readers, errPollClosed)
	pd.writers = wake(pd.writers, errPollClosed)
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
				pd.readers = wake(pd.readers, nil)
			}
			if hup || e.Events&syscall.EPOLLOUT != 0 {
				pd.writers = wake(pd.writers, nil)
			}
			// EPOLLONESHOT disabled the fd, so re-arm for any remaining waiters.
			pd.inEpoll = true // it is still registered, just disarmed
			p.arm(pd)
		}
		p.mu.Unlock()
	}
}

// wake sends err to every waiter in s and returns s emptied, keeping its
// backing array. Must be called with p.mu held.
func wake(s []chan error, err error) []chan error {
	for i, ch := range s {
		ch <- err
		s[i] = nil
	}
	return s[:0]
}

func removeChan(s []chan error, ch chan error) []chan error {
	for i, c := range s {
		if c == ch {
			n := copy(s[i:], s[i+1:])
			s[i+n] = nil
			return s[:i+n]
		}
	}
	return s
}
