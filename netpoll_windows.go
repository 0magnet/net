//go:build windows && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm

// TINYGO: the Windows part of the host poller (netpoll_host.go).
//
// Goroutines share one thread on windows, so the poller cannot block in the
// OS. It polls the waited sockets with select and a zero timeout, and sleeps
// between polls while nothing is ready. select reports a failed connect as an
// exception, which WSAPoll on many Windows versions never does.

package net

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const pollKeepClosed = false

const (
	pollMinIdle = time.Millisecond
	pollMaxIdle = 10 * time.Millisecond
)

type pollBackend struct {
	kick chan struct{} // a new waiter, so poll now
}

var procTimeBeginPeriod = syscall.NewLazyDLL("winmm.dll").NewProc("timeBeginPeriod")

func (p *netPoller) open() error {
	p.be.kick = make(chan struct{}, 1)
	// Sleep rounds up to the 15.6 ms system tick unless the timer resolution is
	// raised, which would add that much latency to every wait.
	procTimeBeginPeriod.Call(1)
	return nil
}

func (p *netPoller) unregister(pd *pollDesc) {}

// arm wakes the loop to poll pd. Must be called with p.mu held.
func (p *netPoller) arm(pd *pollDesc) {
	if len(pd.readers) == 0 && len(pd.writers) == 0 {
		return
	}
	select {
	case p.be.kick <- struct{}{}:
	default:
	}
}

// fdSet is a Winsock fd_set: a count, then the sockets. The count is a
// u_int padded to the size of a SOCKET, so element 0 holds it.
type fdSet []uintptr

func (s fdSet) ready() []uintptr { return s[1 : 1+int(uint32(s[0]))] }

func (p *netPoller) loop() {
	rd, wr, ex := fdSet{0}, fdSet{0}, fdSet{0}
	idle := pollMinIdle
	t := time.NewTimer(pollMaxIdle)
	t.Stop()
	for {
		p.mu.Lock()
		rd, wr, ex = rd[:1], wr[:1], ex[:1]
		for fd, pd := range p.fds {
			if len(pd.readers) != 0 {
				rd = append(rd, uintptr(fd))
			}
			if len(pd.writers) != 0 {
				wr = append(wr, uintptr(fd))
				ex = append(ex, uintptr(fd))
			}
		}
		p.mu.Unlock()

		n := 0
		if len(rd) > 1 || len(wr) > 1 {
			rd[0], wr[0], ex[0] = uintptr(len(rd)-1), uintptr(len(wr)-1), uintptr(len(ex)-1)
			var tv syscall.Timeval
			r, _, _ := procSelect.Call(0, uintptr(unsafe.Pointer(&rd[0])), uintptr(unsafe.Pointer(&wr[0])), uintptr(unsafe.Pointer(&ex[0])), uintptr(unsafe.Pointer(&tv)))
			n = int(int32(r))
		}
		if n > 0 {
			p.mu.Lock()
			for _, fd := range rd.ready() {
				if pd := p.fds[int(fd)]; pd != nil {
					pd.readers = wake(pd.readers, nil)
				}
			}
			for _, fd := range wr.ready() {
				if pd := p.fds[int(fd)]; pd != nil {
					pd.writers = wake(pd.writers, nil)
				}
			}
			for _, fd := range ex.ready() {
				if pd := p.fds[int(fd)]; pd != nil {
					pd.readers = wake(pd.readers, nil)
					pd.writers = wake(pd.writers, nil)
				}
			}
			p.mu.Unlock()
			idle = pollMinIdle
			// Let the woken goroutines run before polling again.
			runtime.Gosched()
			continue
		}

		t.Reset(idle)
		select {
		case <-p.be.kick:
			t.Stop()
			select {
			case <-t.C:
			default:
			}
			idle = pollMinIdle
		case <-t.C:
			idle = min(2*idle, pollMaxIdle)
		}
	}
}
