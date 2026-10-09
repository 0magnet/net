// TINYGO: connections for the approximate roundTrip in client.go, made as Go's
// Transport makes them: through DialContext or Dial, Proxy and TLSClientConfig.

package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
)

func (t *Transport) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	switch {
	case t.DialContext != nil:
		return t.DialContext(ctx, network, addr)
	case t.Dial != nil:
		return t.Dial(network, addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// canonicalAddr returns u's host and port, with the scheme's default port.
func canonicalAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// connect returns a connection that carries req to its server, and whether
// req must be written in the absolute form an HTTP proxy expects.
func (t *Transport) connect(req *Request) (conn net.Conn, usingProxy bool, proxyAuth string, err error) {
	ctx := req.Context()
	addr := canonicalAddr(req.URL)
	var pu *url.URL
	if t.Proxy != nil {
		if pu, err = t.Proxy(req); err != nil {
			return nil, false, "", err
		}
	}
	switch {
	case pu == nil:
		conn, err = t.dial(ctx, "tcp", addr)
	case pu.Scheme == "socks5" || pu.Scheme == "socks5h":
		if conn, err = t.dial(ctx, "tcp", canonicalAddr(pu)); err == nil {
			if err = socks5Connect(conn, addr, pu.User); err != nil {
				conn.Close()
			}
		}
	case pu.Scheme == "http" || pu.Scheme == "https":
		if conn, err = t.dial(ctx, "tcp", canonicalAddr(pu)); err == nil && pu.Scheme == "https" {
			conn, err = t.tlsClient(ctx, conn, pu.Hostname())
		}
		if err == nil {
			auth := proxyAuthorization(pu.User)
			if req.URL.Scheme == "https" {
				if err = httpConnect(conn, addr, auth); err != nil {
					conn.Close()
				}
			} else {
				usingProxy, proxyAuth = true, auth
			}
		}
	default:
		return nil, false, "", fmt.Errorf("net/http: unsupported proxy scheme %q", pu.Scheme)
	}
	if err != nil {
		return nil, false, "", err
	}
	if req.URL.Scheme == "https" {
		if conn, err = t.tlsClient(ctx, conn, req.URL.Hostname()); err != nil {
			return nil, false, "", err
		}
	}
	return conn, usingProxy, proxyAuth, nil
}

func (t *Transport) tlsClient(ctx context.Context, conn net.Conn, serverName string) (net.Conn, error) {
	cfg := &tls.Config{}
	if t.TLSClientConfig != nil {
		cfg = t.TLSClientConfig.Clone()
	}
	if cfg.ServerName == "" {
		cfg.ServerName = serverName
	}
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tc, nil
}

func proxyAuthorization(u *url.Userinfo) string {
	if u == nil {
		return ""
	}
	password, _ := u.Password()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+password))
}

// httpConnect asks an HTTP proxy on conn for a tunnel to addr.
func httpConnect(conn net.Conn, addr, auth string) error {
	hdr := make(Header)
	if auth != "" {
		hdr.Set("Proxy-Authorization", auth)
	}
	connectReq := &Request{Method: "CONNECT", URL: &url.URL{Opaque: addr}, Host: addr, Header: hdr}
	if err := connectReq.Write(conn); err != nil {
		return err
	}
	resp, err := ReadResponse(bufio.NewReader(conn), connectReq)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return errors.New("net/http: proxy refused CONNECT: " + resp.Status)
	}
	return nil
}

// socks5Connect asks a SOCKS5 proxy on conn for a stream to addr.
// See RFC 1928 section 4 and RFC 1929 for the username and password.
func socks5Connect(conn net.Conn, addr string, user *url.Userinfo) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return errors.New("socks5: bad port in " + addr)
	}
	methods := []byte{0x00}
	if user != nil {
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	var b [4]byte
	if _, err := io.ReadFull(conn, b[:2]); err != nil {
		return err
	}
	switch {
	case b[0] != 5:
		return errors.New("socks5: unexpected protocol version")
	case b[1] == 0x02 && user != nil:
		password, _ := user.Password()
		name := user.Username()
		if len(name) > 255 || len(password) > 255 {
			return errors.New("socks5: username or password too long")
		}
		msg := append([]byte{1, byte(len(name))}, name...)
		msg = append(append(msg, byte(len(password))), password...)
		if _, err := conn.Write(msg); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, b[:2]); err != nil {
			return err
		}
		if b[1] != 0 {
			return errors.New("socks5: username and password rejected")
		}
	case b[1] != 0x00:
		return errors.New("socks5: no acceptable authentication method")
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 255 {
			return errors.New("socks5: host name too long")
		}
		req = append(append(req, 3, byte(len(host))), host...)
	} else if ip4 := ip.To4(); ip4 != nil {
		req = append(append(req, 1), ip4...)
	} else {
		req = append(append(req, 4), ip.To16()...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, b[:4]); err != nil {
		return err
	}
	if b[1] != 0 {
		return fmt.Errorf("socks5: connect to %s failed with reply %d", addr, b[1])
	}
	var skip int
	switch b[3] {
	case 1:
		skip = 4 + 2
	case 4:
		skip = 16 + 2
	case 3:
		if _, err := io.ReadFull(conn, b[:1]); err != nil {
			return err
		}
		skip = int(b[0]) + 2
	default:
		return errors.New("socks5: unknown address type in reply")
	}
	_, err = io.ReadFull(conn, make([]byte, skip))
	return err
}
