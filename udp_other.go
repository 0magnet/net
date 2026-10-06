//go:build !(linux && !baremetal && !nintendoswitch && !wasm_unknown && !tinygo.wasm)

package net

import (
	"errors"
	"net/netip"
	"time"
)

// Netdev drivers only offer connected sockets, so the peer is the connected one.

func udpRecvFrom(fd int, b []byte, deadline time.Time) (int, netip.AddrPort, error) {
	n, err := netdev.Recv(fd, b, 0, deadline)
	return n, netip.AddrPort{}, err
}

func udpSendTo(fd int, b []byte, _ netip.AddrPort, deadline time.Time) (int, error) {
	return netdev.Send(fd, b, 0, deadline)
}

func udpRecvMsg(int, []byte, []byte, time.Time) (int, int, int, netip.AddrPort, error) {
	return 0, 0, 0, netip.AddrPort{}, errors.New("ReadMsgUDP not implemented")
}

func udpSendMsg(int, []byte, []byte, netip.AddrPort, time.Time) (int, int, error) {
	return 0, 0, errors.New("WriteMsgUDP not implemented")
}
