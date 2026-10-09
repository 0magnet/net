//go:build js && wasm

package net

import (
	"errors"
	"io"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"
)

// TINYGO: a browser offers no sockets, so on js the netdev connects listeners
// and dialers inside this program, as Go's net_fake.go does for GOOS=js.

func init() {
	useNetdev(newLoopNetdev())
}

const (
	loopBufMax       = 1 << 20
	loopBacklogMax   = 128
	loopFirstEphPort = 32768
	_AF_UNIX         = 0x1
)

var (
	errLoopRefused = os.NewSyscallError("connect", syscall.ECONNREFUSED)
	errLoopInUse   = os.NewSyscallError("bind", syscall.EADDRINUSE)
	errLoopNoHost  = errors.New("no such host")
	errLoopBadFD   = os.NewSyscallError("socket", syscall.EBADF)
)

type loopSock struct {
	stype   int
	unix    bool
	key     string
	local   netip.AddrPort
	listen  bool
	backlog []*loopSock
	peer    *loopSock
	peerKey string
	eof     bool
	closed  bool
	wrShut  bool
	buf     []byte
	msgs    [][]byte
	ev      chan struct{}
	rdGen   int
	wrGen   int
}

type loopNetdev struct {
	mu      sync.Mutex
	fds     map[int]*loopSock
	bound   map[string]*loopSock
	nextFD  int
	nextEph int
}

func newLoopNetdev() *loopNetdev {
	return &loopNetdev{
		fds:     map[int]*loopSock{},
		bound:   map[string]*loopSock{},
		nextFD:  3,
		nextEph: loopFirstEphPort,
	}
}

func newLoopSock(stype int) *loopSock {
	return &loopSock{stype: stype, ev: make(chan struct{})}
}

// notify wakes every goroutine waiting on s. Callers hold n.mu.
func (s *loopSock) notify() {
	close(s.ev)
	s.ev = make(chan struct{})
}

// wait releases n.mu until s changes or deadline passes, then retakes it.
func (n *loopNetdev) wait(s *loopSock, deadline time.Time) error {
	ev := s.ev
	if deadline.IsZero() {
		n.mu.Unlock()
		<-ev
		n.mu.Lock()
		return nil
	}
	d := time.Until(deadline)
	if d <= 0 {
		return os.ErrDeadlineExceeded
	}
	t := time.NewTimer(d)
	n.mu.Unlock()
	select {
	case <-ev:
		t.Stop()
		n.mu.Lock()
		return nil
	case <-t.C:
		n.mu.Lock()
		return os.ErrDeadlineExceeded
	}
}

func (n *loopNetdev) sock(fd int) (*loopSock, error) {
	s := n.fds[fd]
	if s == nil {
		return nil, errLoopBadFD
	}
	return s, nil
}

func (n *loopNetdev) newFD(s *loopSock) int {
	fd := n.nextFD
	n.nextFD++
	n.fds[fd] = s
	return fd
}

func loopProto(stype int) string {
	if stype == _SOCK_DGRAM {
		return "udp:"
	}
	return "tcp:"
}

func loopPortKey(stype int, port uint16) string {
	return loopProto(stype) + netItoa(int(port))
}

// ephemeral picks a free port for stype. Callers hold n.mu.
func (n *loopNetdev) ephemeral(stype int) uint16 {
	for i := 0; i < 65536-loopFirstEphPort; i++ {
		p := n.nextEph
		n.nextEph++
		if n.nextEph > 65535 {
			n.nextEph = loopFirstEphPort
		}
		if n.bound[loopPortKey(stype, uint16(p))] == nil {
			return uint16(p)
		}
	}
	return 0
}

func loopLocalAddr(a netip.Addr) netip.Addr {
	if !a.IsValid() || a.IsUnspecified() {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1})
	}
	return a
}

func (n *loopNetdev) GetHostByName(name string) (netip.Addr, error) {
	if name == "" || name == "localhost" {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	if a, err := netip.ParseAddr(name); err == nil {
		return a, nil
	}
	return netip.Addr{}, errLoopNoHost
}

func (n *loopNetdev) Addr() (netip.Addr, error) {
	return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
}

func (n *loopNetdev) Socket(domain int, stype int, protocol int) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := newLoopSock(stype)
	s.unix = domain == _AF_UNIX
	return n.newFD(s), nil
}

func (n *loopNetdev) Bind(fd int, ap netip.AddrPort) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	port := ap.Port()
	if port == 0 {
		if port = n.ephemeral(s.stype); port == 0 {
			return errLoopInUse
		}
	}
	key := loopPortKey(s.stype, port)
	if n.bound[key] != nil {
		return errLoopInUse
	}
	n.bound[key] = s
	s.key = key
	s.local = netip.AddrPortFrom(loopLocalAddr(ap.Addr()), port)
	return nil
}

// bindUnix binds fd to the socket path name.
func (n *loopNetdev) bindUnix(fd int, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	key := "unix:" + name
	if n.bound[key] != nil {
		return errLoopInUse
	}
	n.bound[key] = s
	s.key = key
	return nil
}

// autoBind gives an unbound socket an ephemeral port. Callers hold n.mu.
func (n *loopNetdev) autoBind(s *loopSock) error {
	if s.key != "" || s.unix {
		return nil
	}
	port := n.ephemeral(s.stype)
	if port == 0 {
		return errLoopInUse
	}
	s.key = loopPortKey(s.stype, port)
	n.bound[s.key] = s
	s.local = netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
	return nil
}

func (n *loopNetdev) Connect(fd int, host string, ap netip.AddrPort) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	if a := ap.Addr(); a.IsValid() && !a.IsLoopback() && !a.IsUnspecified() {
		return errLoopRefused
	}
	if err := n.autoBind(s); err != nil {
		return err
	}
	key := loopPortKey(s.stype, ap.Port())
	if s.stype == _SOCK_DGRAM {
		s.peerKey = key
		return nil
	}
	return n.connectStream(s, key)
}

// connectUnix connects fd to the listener bound at the socket path name.
func (n *loopNetdev) connectUnix(fd int, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	return n.connectStream(s, "unix:"+name)
}

// connectStream queues the server end of a new pair on the listener bound at
// key. Callers hold n.mu.
func (n *loopNetdev) connectStream(s *loopSock, key string) error {
	l := n.bound[key]
	if l == nil || !l.listen || l.closed || len(l.backlog) >= loopBacklogMax {
		if s.unix && (l == nil || !l.listen) {
			return os.NewSyscallError("connect", syscall.ENOENT)
		}
		return errLoopRefused
	}
	srv := newLoopSock(_SOCK_STREAM)
	srv.unix = s.unix
	srv.local = l.local
	srv.peer = s
	s.peer = srv
	l.backlog = append(l.backlog, srv)
	l.notify()
	return nil
}

func (n *loopNetdev) Listen(fd int, backlog int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	if s.key == "" {
		if err := n.autoBind(s); err != nil {
			return err
		}
	}
	s.listen = true
	return nil
}

func (n *loopNetdev) Accept(fd int) (int, netip.AddrPort, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return -1, netip.AddrPort{}, err
	}
	for len(s.backlog) == 0 {
		if s.closed {
			return -1, netip.AddrPort{}, ErrClosed
		}
		n.wait(s, time.Time{})
	}
	srv := s.backlog[0]
	s.backlog = s.backlog[1:]
	var raddr netip.AddrPort
	if srv.peer != nil {
		raddr = srv.peer.local
	}
	return n.newFD(srv), raddr, nil
}

func (n *loopNetdev) Send(fd int, buf []byte, flags int, deadline time.Time) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return -1, err
	}
	if s.stype == _SOCK_DGRAM {
		return n.sendDgram(s, buf)
	}
	gen := s.wrGen
	total := 0
	for total < len(buf) {
		if s.closed {
			return total, ErrClosed
		}
		p := s.peer
		if p == nil || p.closed || s.wrShut {
			return total, os.NewSyscallError("write", syscall.EPIPE)
		}
		if room := loopBufMax - len(p.buf); room > 0 {
			k := len(buf) - total
			if k > room {
				k = room
			}
			p.buf = append(p.buf, buf[total:total+k]...)
			total += k
			p.notify()
			continue
		}
		if err := n.wait(p, deadline); err != nil {
			return total, err
		}
		if s.wrGen != gen {
			return total, errPollInterrupted
		}
	}
	return total, nil
}

// sendDgram delivers one datagram to the connected peer, or drops it as UDP
// does when nothing is bound there. Callers hold n.mu.
func (n *loopNetdev) sendDgram(s *loopSock, buf []byte) (int, error) {
	if s.closed {
		return -1, ErrClosed
	}
	if p := n.bound[s.peerKey]; p != nil && !p.closed && len(p.msgs) < loopBacklogMax {
		p.msgs = append(p.msgs, append([]byte(nil), buf...))
		p.notify()
	}
	return len(buf), nil
}

func (n *loopNetdev) Recv(fd int, buf []byte, flags int, deadline time.Time) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return -1, err
	}
	gen := s.rdGen
	for {
		if s.closed {
			return -1, ErrClosed
		}
		if s.stype == _SOCK_DGRAM {
			if len(s.msgs) > 0 {
				m := s.msgs[0]
				s.msgs = s.msgs[1:]
				return copy(buf, m), nil
			}
		} else {
			if len(s.buf) > 0 {
				k := copy(buf, s.buf)
				s.buf = s.buf[k:]
				if len(s.buf) == 0 {
					s.buf = nil
				}
				s.notify()
				return k, nil
			}
			if s.eof || s.peer == nil {
				return 0, io.EOF
			}
		}
		if err := n.wait(s, deadline); err != nil {
			return -1, err
		}
		if s.rdGen != gen {
			return -1, errPollInterrupted
		}
	}
}

func (n *loopNetdev) Close(fd int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	delete(n.fds, fd)
	n.closeSock(s)
	return nil
}

// closeSock closes s and ends the pairs still waiting in its backlog.
// Callers hold n.mu.
func (n *loopNetdev) closeSock(s *loopSock) {
	s.closed = true
	if s.key != "" && n.bound[s.key] == s {
		delete(n.bound, s.key)
	}
	if p := s.peer; p != nil {
		p.eof = true
		p.notify()
	}
	for _, srv := range s.backlog {
		n.closeSock(srv)
	}
	s.backlog = nil
	s.notify()
}

func (n *loopNetdev) SetSockOpt(fd int, level int, opt int, value interface{}) error {
	return nil
}

// GetSockname reports the port picked for a socket bound to port 0.
func (n *loopNetdev) GetSockname(fd int) (netip.AddrPort, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return s.local, nil
}

// PollDeadline wakes a blocked Recv or Send on fd so it takes the new deadline.
func (n *loopNetdev) PollDeadline(fd int, write bool, deadline time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.fds[fd]
	if s == nil {
		return
	}
	if write {
		s.wrGen++
		if s.peer != nil {
			s.peer.notify()
		}
	} else {
		s.rdGen++
	}
	s.notify()
}

// shutdownWrite ends fd's output, so its peer reads io.EOF once the data
// already sent is drained.
func (n *loopNetdev) shutdownWrite(fd int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, err := n.sock(fd)
	if err != nil {
		return err
	}
	s.wrShut = true
	if p := s.peer; p != nil {
		p.eof = true
		p.notify()
	}
	return nil
}
