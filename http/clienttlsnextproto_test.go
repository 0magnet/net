//go:build !js

package http

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"
)

// altH2 stands in for x/net/http2 on both sides. A request is one line
// holding the path, and the reply is the rest of the stream.
type altH2 struct {
	c *tls.Conn
}

func (a *altH2) RoundTrip(req *Request) (*Response, error) {
	if _, err := io.WriteString(a.c, req.URL.Path+"\n"); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(a.c)
	if err != nil {
		return nil, err
	}
	return &Response{StatusCode: 200, Header: Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}, nil
}

func tlsNextProtoServer(t *testing.T) string {
	t.Helper()
	cert, err := tls.X509KeyPair(localhostCert, localhostKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Handler: HandlerFunc(func(w ResponseWriter, r *Request) {
			io.WriteString(w, "h1 "+r.URL.Path)
		}),
		TLSNextProto: map[string]func(*Server, *tls.Conn, Handler){
			"h2": func(_ *Server, c *tls.Conn, _ Handler) {
				path, _ := bufio.NewReader(c).ReadString('\n')
				io.WriteString(c, "h2 "+strings.TrimSpace(path))
				c.Close()
			},
		},
	}
	go srv.Serve(tls.NewListener(l, cfg))
	t.Cleanup(func() { l.Close() })
	return l.Addr().String()
}

func h2Transport(got *string) *Transport {
	return &Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}},
		TLSNextProto: map[string]func(string, *tls.Conn) RoundTripper{
			"h2": func(authority string, c *tls.Conn) RoundTripper {
				*got = authority
				return &altH2{c}
			},
		},
	}
}

func getBody(t *testing.T, rt RoundTripper, req *Request) string {
	t.Helper()
	resp, err := (&Client{Transport: rt}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTransportTLSNextProto(t *testing.T) {
	addr := tlsNextProtoServer(t)
	var authority string
	req, _ := NewRequest("GET", "https://"+addr+"/a", nil)
	if got := getBody(t, h2Transport(&authority), req); got != "h2 /a" {
		t.Fatalf("body = %q, want the h2 handler", got)
	}
	if authority != addr {
		t.Fatalf("TLSNextProto authority = %q, want %q", authority, addr)
	}
}

func TestTransportWithoutTLSNextProto(t *testing.T) {
	addr := tlsNextProtoServer(t)
	tr := &Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	req, _ := NewRequest("GET", "https://"+addr+"/b", nil)
	if got := getBody(t, tr, req); got != "h1 /b" {
		t.Fatalf("body = %q, want the HTTP/1 handler", got)
	}
}

// A websocket upgrade needs HTTP/1, so h2 is not offered for it.
func TestTransportTLSNextProtoWebsocket(t *testing.T) {
	addr := tlsNextProtoServer(t)
	var authority string
	req, _ := NewRequest("GET", "https://"+addr+"/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	if got := getBody(t, h2Transport(&authority), req); got != "h1 /ws" {
		t.Fatalf("body = %q, want the HTTP/1 handler", got)
	}
}

type skipAlt struct{ calls int }

func (s *skipAlt) RoundTrip(*Request) (*Response, error) {
	s.calls++
	return nil, ErrSkipAltProtocol
}

func TestTransportRegisterProtocol(t *testing.T) {
	addr := tlsNextProtoServer(t)
	tr := &Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	skip := &skipAlt{}
	tr.RegisterProtocol("https", skip)
	foo := &recordingTransport{}
	tr.RegisterProtocol("foo", foo)

	req, _ := NewRequest("GET", "https://"+addr+"/c", nil)
	if got := getBody(t, tr, req); got != "h1 /c" || skip.calls != 1 {
		t.Fatalf("body = %q after %d alt calls, want h1 /c after 1", got, skip.calls)
	}
	req, _ = NewRequest("GET", "foo://x/y", nil)
	if got := getBody(t, tr, req); got != "ok" || foo.calls != 1 {
		t.Fatalf("foo body = %q after %d calls", got, foo.calls)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("second RegisterProtocol of foo did not panic")
		}
	}()
	tr.RegisterProtocol("foo", foo)
}

// localhostCert and localhostKey are copied from Go net/http/internal/testcert.
var localhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIIDSDCCAjCgAwIBAgIQEP/md970HysdBTpuzDOf0DANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMCAXDTcwMDEwMTAwMDAwMFoYDzIwODQwMTI5MTYw
MDAwWjASMRAwDgYDVQQKEwdBY21lIENvMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEAxcl69ROJdxjN+MJZnbFrYxyQooADCsJ6VDkuMyNQIix/Hk15Nk/u
FyBX1Me++aEpGmY3RIY4fUvELqT/srvAHsTXwVVSttMcY8pcAFmXSqo3x4MuUTG/
jCX3Vftj0r3EM5M8ImY1rzA/jqTTLJg00rD+DmuDABcqQvoXw/RV8w1yTRi5BPoH
DFD/AWTt/YgMvk1l2Yq/xI8VbMUIpjBoGXxWsSevQ5i2s1mk9/yZzu0Ysp1tTlzD
qOPa4ysFjBitdXiwfxjxtv5nXqOCP5rheKO0sWLk0fetMp1OV5JSJMAJw6c2ZMkl
U2WMqAEpRjdE/vHfIuNg+yGaRRqI07NZRQIDAQABo4GXMIGUMA4GA1UdDwEB/wQE
AwICpDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MB0GA1Ud
DgQWBBQR5QIzmacmw78ZI1C4MXw7Q0wJ1jA9BgNVHREENjA0ggtleGFtcGxlLmNv
bYINKi5leGFtcGxlLmNvbYcEfwAAAYcQAAAAAAAAAAAAAAAAAAAAATANBgkqhkiG
9w0BAQsFAAOCAQEACrRNgiioUDzxQftd0fwOa6iRRcPampZRDtuaF68yNHoNWbOu
LUwc05eOWxRq3iABGSk2xg+FXM3DDeW4HhAhCFptq7jbVZ+4Jj6HeJG9mYRatAxR
Y/dEpa0D0EHhDxxVg6UzKOXB355n0IetGE/aWvyTV9SiDs6QsaC57Q9qq1/mitx5
2GFBoapol9L5FxCc77bztzK8CpLujkBi25Vk6GAFbl27opLfpyxkM+rX/T6MXCPO
6/YBacNZ7ff1/57Etg4i5mNA6ubCpuc4Gi9oYqCNNohftr2lkJr7REdDR6OW0lsL
rF7r4gUnKeC7mYIH1zypY7laskopiLFAfe96Kg==
-----END CERTIFICATE-----`)

var localhostKey = []byte(strings.ReplaceAll(`-----BEGIN RSA TESTING KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDFyXr1E4l3GM34
wlmdsWtjHJCigAMKwnpUOS4zI1AiLH8eTXk2T+4XIFfUx775oSkaZjdEhjh9S8Qu
pP+yu8AexNfBVVK20xxjylwAWZdKqjfHgy5RMb+MJfdV+2PSvcQzkzwiZjWvMD+O
pNMsmDTSsP4Oa4MAFypC+hfD9FXzDXJNGLkE+gcMUP8BZO39iAy+TWXZir/EjxVs
xQimMGgZfFaxJ69DmLazWaT3/JnO7RiynW1OXMOo49rjKwWMGK11eLB/GPG2/mde
o4I/muF4o7SxYuTR960ynU5XklIkwAnDpzZkySVTZYyoASlGN0T+8d8i42D7IZpF
GojTs1lFAgMBAAECggEAIYthUi1lFBDd5gG4Rzlu+BlBIn5JhcqkCqLEBiJIFfOr
/4yuMRrvS3bNzqWt6xJ9MSAC4ZlN/VobRLnxL/QNymoiGYUKCT3Ww8nvPpPzR9OE
sE68TUL9tJw/zZJcRMKwgvrGqSLimfq53MxxkE+kLdOc0v9C8YH8Re26mB5ZcWYa
7YFyZQpKsQYnsmu/05cMbpOQrQWhtmIqRoyn8mG/par2s3NzjtpSE9NINyz26uFc
k/3ovFJQIHkUmTS7KHD3BgY5vuCqP98HramYnOysJ0WoYgvSDNCWw3037s5CCwJT
gCKuM+Ow6liFrj83RrdKBpm5QUGjfNpYP31o+QNP4QKBgQDSrUQ2XdgtAnibAV7u
7kbxOxro0EhIKso0Y/6LbDQgcXgxLqltkmeqZgG8nC3Z793lhlSasz2snhzzooV5
5fTy1y8ikXqjhG0nNkInFyOhsI0auE28CFoDowaQd+5cmCatpN4Grqo5PNRXxm1w
HktfPEgoP11NNCFHvvN5fEKbbQKBgQDwVlOaV20IvW3IPq7cXZyiyabouFF9eTRo
VJka1Uv+JtyvL2P0NKkjYHOdN8gRblWqxQtJoTNk020rVA4UP1heiXALy50gvj/p
hMcybPTLYSPOhAGx838KIcvGR5oskP1aUCmFbFQzGELxhJ9diVVjxUtbG2DuwPKd
tD9TLxT2OQKBgQCcdlHSjp+dzdgERmBa0ludjGfPv9/uuNizUBAbO6D690psPFtY
JQMYaemgSd1DngEOFVWADt4e9M5Lose+YCoqr+UxpxmNlyv5kzJOFcFAs/4XeglB
PHKdgNW/NVKxMc6H54l9LPr+x05sYdGlEtqnP/3W5jhEvhJ5Vjc8YiyVgQKBgQCl
zwjyrGo+42GACy7cPYE5FeIfIDqoVByB9guC5bD98JXEDu/opQQjsgFRcBCJZhOY
M0UsURiB8ROaFu13rpQq9KrmmF0ZH+g8FSzQbzcbsTLg4VXCDXmR5esOKowFPypr
Sm667BfTAGP++D5ya7MLmCv6+RKQ5XD8uEQQAaV2kQKBgAD8qeJuWIXZT0VKkQrn
nIhgtzGERF/6sZdQGW2LxTbUDWG74AfFkkEbeBfwEkCZXY/xmnYqYABhvlSex8jU
supU6Eea21esIxIub2zv/Np0ojUb6rlqTPS4Ox1E27D787EJ3VOXpriSD10vyNnZ
jel6uj2FOP9g54s+GzlSVg/T
-----END RSA TESTING KEY-----`, "TESTING KEY", "PRIVATE KEY"))
