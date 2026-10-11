//go:build linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: a small poller for the host netdev. A goroutine that would block
// parks here until the fd is ready, its deadline passes, or it closes. The OS
// part is in netpoll_epoll.go, which provides open, arm, unregister and loop.

package net

import (
	"errors"
	"sync"
	"time"
)

// errPollClosed is returned to goroutines parked on an fd when that fd is closed.
var errPollClosed = errors.New("net: use of closed network connection")

// pollDesc holds the goroutines currently waiting on one fd, split by direction.
type pollDesc struct {
	fd         int
	readers    []chan error
	writers    []chan error
	registered bool // the backend is polling fd

	// Records an interrupt that arrived with no waiter parked, so the next
	// wait in that direction returns at once instead of using a stale deadline.
	readInterrupt  bool
	writeInterrupt bool

	// The latest deadline per direction, set by interrupt. It wins over the
	// caller's copy, which may predate a concurrent deadline change.
	deadline    [2]time.Time
	hasDeadline [2]bool

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
	be   pollBackend

	mu  sync.Mutex
	fds map[int]*pollDesc
}

var poller netPoller

func (p *netPoller) init() {
	p.once.Do(func() {
		if err := p.open(); err != nil {
			p.err = err
			return
		}
		p.fds = make(map[int]*pollDesc)
		go p.loop()
	})
}

// wait blocks until fd is ready in the requested direction (writable when
// write, otherwise readable), the deadline expires, or the fd is closed.
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
	if pd.hasDeadline[dir] {
		deadline = pd.deadline[dir]
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
func (p *netPoller) interrupt(fd int, write bool, deadline time.Time) {
	p.init()
	if p.err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pd := p.fds[fd]
	if pd == nil {
		pd = &pollDesc{fd: fd}
		p.fds[fd] = pd
	}
	dir := 0
	if write {
		dir = 1
	}
	pd.deadline[dir], pd.hasDeadline[dir] = deadline, true
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
	p.unregister(pd)
	delete(p.fds, fd)
	pd.readers = wake(pd.readers, errPollClosed)
	pd.writers = wake(pd.writers, errPollClosed)
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
