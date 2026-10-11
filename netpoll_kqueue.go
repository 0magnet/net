//go:build darwin && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: the kqueue part of the host poller (netpoll_host.go).

package net

import "syscall"

type pollBackend struct {
	kq      int
	changes [2]syscall.Kevent_t // scratch for arm, guarded by mu
}

func (p *netPoller) open() error {
	kq, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	syscall.CloseOnExec(kq)
	p.be.kq = kq
	return nil
}

// unregister has nothing to do, because closing an fd removes its events.
func (p *netPoller) unregister(pd *pollDesc) {}

// arm adds a one-shot filter for each direction pd has waiters in. Must be
// called with p.mu held.
func (p *netPoller) arm(pd *pollDesc) {
	n := 0
	if len(pd.readers) != 0 {
		syscall.SetKevent(&p.be.changes[n], pd.fd, syscall.EVFILT_READ, syscall.EV_ADD|syscall.EV_ONESHOT)
		n++
	}
	if len(pd.writers) != 0 {
		syscall.SetKevent(&p.be.changes[n], pd.fd, syscall.EVFILT_WRITE, syscall.EV_ADD|syscall.EV_ONESHOT)
		n++
	}
	if n == 0 {
		return
	}
	if _, err := syscall.Kevent(p.be.kq, p.be.changes[:n], nil, nil); err == nil {
		pd.registered = true
	}
}

// loop is the poller's background goroutine. It waits for kqueue events
// and wakes the corresponding parked goroutines.
func (p *netPoller) loop() {
	events := make([]syscall.Kevent_t, 64)
	for {
		n, err := syscall.Kevent(p.be.kq, nil, events, nil)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return
		}
		p.mu.Lock()
		for i := 0; i < n; i++ {
			e := events[i]
			pd := p.fds[int(e.Ident)]
			if pd == nil {
				continue
			}
			// On error or EOF, wake everyone so they observe the real result.
			hup := e.Flags&(syscall.EV_EOF|syscall.EV_ERROR) != 0
			if hup || e.Filter == syscall.EVFILT_READ {
				pd.readers = wake(pd.readers, nil)
			}
			if hup || e.Filter == syscall.EVFILT_WRITE {
				pd.writers = wake(pd.writers, nil)
			}
			// The filter was one-shot, so re-arm for any remaining waiters.
			p.arm(pd)
		}
		p.mu.Unlock()
	}
}
