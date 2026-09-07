package http

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

// redirectTransport answers the first request with a 302 pointing at `to`,
// and every later one with a 200. It keeps each request it was handed, and a
// snapshot of that request's headers taken at RoundTrip time — after the
// redirect header copier and the cookie jar have both had their say.
type redirectTransport struct {
	to        string
	setCookie string // Set-Cookie on the redirect response, if any

	reqs []*Request
	hdrs []Header
}

func (t *redirectTransport) RoundTrip(req *Request) (*Response, error) {
	t.reqs = append(t.reqs, req)
	t.hdrs = append(t.hdrs, req.Header.Clone())

	if len(t.reqs) == 1 {
		h := Header{"Location": {t.to}}
		if t.setCookie != "" {
			h.Set("Set-Cookie", t.setCookie)
		}
		return &Response{
			Status:        "302 Found",
			StatusCode:    302,
			Header:        h,
			Body:          io.NopCloser(strings.NewReader("")),
			ContentLength: 0,
			Request:       req,
		}, nil
	}
	return &Response{
		Status:        "200 OK",
		StatusCode:    200,
		Header:        Header{},
		Body:          io.NopCloser(strings.NewReader("ok")),
		ContentLength: 2,
		Request:       req,
	}, nil
}

// runThroughBothTransports runs fn once with the transport installed as
// Client.Transport and once with it installed as DefaultTransport, because
// dispatch has to reach the redirect loop identically on both paths.
func runThroughBothTransports(t *testing.T, fn func(t *testing.T, c *Client, rt *redirectTransport), mk func() *redirectTransport) {
	t.Helper()

	t.Run("explicit", func(t *testing.T) {
		rt := mk()
		fn(t, &Client{Transport: rt}, rt)
	})

	t.Run("default", func(t *testing.T) {
		previous := DefaultTransport
		defer func() { DefaultTransport = previous }()
		rt := mk()
		DefaultTransport = rt
		fn(t, &Client{}, rt)
	})
}

// A redirect must be followed through whichever RoundTripper the client
// selected, so the fake transport sees both hops rather than one.
func TestClientRedirectThroughTransport(t *testing.T) {
	runThroughBothTransports(t,
		func(t *testing.T, c *Client, rt *redirectTransport) {
			resp, err := c.Get("http://example.com/first")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if len(rt.reqs) != 2 {
				t.Fatalf("RoundTrip calls = %d, want 2", len(rt.reqs))
			}
			if got := rt.reqs[0].URL.String(); got != "http://example.com/first" {
				t.Errorf("hop 1 URL = %q", got)
			}
			if got := rt.reqs[1].URL.String(); got != "http://example.com/second" {
				t.Errorf("hop 2 URL = %q", got)
			}
			if resp.StatusCode != 200 {
				t.Errorf("final status = %d, want 200", resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "ok" {
				t.Errorf("final body = %q, want %q", body, "ok")
			}
		},
		func() *redirectTransport { return &redirectTransport{to: "http://example.com/second"} },
	)
}

// A cross-host redirect must drop the sensitive headers, and a same-host one
// must keep them. Both go through shouldCopyHeaderOnRedirect and the closure
// makeHeadersCopier returns, so this is the dispatch path exercising them.
func TestClientRedirectSensitiveHeaders(t *testing.T) {
	for _, tc := range []struct {
		name string
		to   string
		keep bool
	}{
		{"same host", "http://example.com/second", true},
		{"subdomain", "http://sub.example.com/second", true},
		{"cross host", "http://evil.example.net/second", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runThroughBothTransports(t,
				func(t *testing.T, c *Client, rt *redirectTransport) {
					req, err := NewRequest("GET", "http://example.com/first", nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer secret")
					req.Header.Set("Cookie", "sid=1")
					req.Header.Set("X-Safe", "kept")

					resp, err := c.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()

					if len(rt.hdrs) != 2 {
						t.Fatalf("RoundTrip calls = %d, want 2", len(rt.hdrs))
					}
					if got := rt.hdrs[0].Get("Authorization"); got != "Bearer secret" {
						t.Fatalf("hop 1 Authorization = %q, want it sent", got)
					}

					hop2 := rt.hdrs[1]
					if got := hop2.Get("X-Safe"); got != "kept" {
						t.Errorf("hop 2 X-Safe = %q, want %q — non-sensitive headers always copy", got, "kept")
					}
					for _, h := range []string{"Authorization", "Cookie"} {
						got := hop2.Get(h)
						if tc.keep && got == "" {
							t.Errorf("hop 2 %s was stripped, want it kept on a %s redirect", h, tc.name)
						}
						if !tc.keep && got != "" {
							t.Errorf("hop 2 %s = %q, want it stripped on a %s redirect", h, got, tc.name)
						}
					}
				},
				func() *redirectTransport { return &redirectTransport{to: tc.to} },
			)
		})
	}
}

// recordingJar is the smallest CookieJar that can show the client consulting
// it per hop: it records the URL of every call and hands back whatever
// SetCookies last stored, regardless of scope.
type recordingJar struct {
	stored []*Cookie
	setFor []string
	getFor []string
}

func (j *recordingJar) SetCookies(u *url.URL, cookies []*Cookie) {
	j.setFor = append(j.setFor, u.String())
	j.stored = append(j.stored, cookies...)
}

func (j *recordingJar) Cookies(u *url.URL) []*Cookie {
	j.getFor = append(j.getFor, u.String())
	return j.stored
}

// A cookie set by the redirect response must be stored against the hop that
// set it and replayed on the next hop. The jar is consulted inside c.send,
// which is also where the transport is selected, so this only holds if the
// two compose.
func TestClientRedirectCookieJar(t *testing.T) {
	runThroughBothTransports(t,
		func(t *testing.T, c *Client, rt *redirectTransport) {
			jar := &recordingJar{}
			c.Jar = jar

			resp, err := c.Get("http://example.com/first")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			if len(rt.hdrs) != 2 {
				t.Fatalf("RoundTrip calls = %d, want 2", len(rt.hdrs))
			}
			if got := rt.hdrs[0].Get("Cookie"); got != "" {
				t.Errorf("hop 1 Cookie = %q, want none — the jar is empty until the 302 lands", got)
			}
			if got := rt.hdrs[1].Get("Cookie"); got != "sid=abc" {
				t.Errorf("hop 2 Cookie = %q, want %q from the jar", got, "sid=abc")
			}

			wantSet := []string{"http://example.com/first"}
			if len(jar.setFor) != 1 || jar.setFor[0] != wantSet[0] {
				t.Errorf("SetCookies called for %v, want %v", jar.setFor, wantSet)
			}
			wantGet := []string{"http://example.com/first", "http://example.com/second"}
			if len(jar.getFor) != 2 || jar.getFor[0] != wantGet[0] || jar.getFor[1] != wantGet[1] {
				t.Errorf("Cookies called for %v, want %v", jar.getFor, wantGet)
			}
		},
		func() *redirectTransport {
			return &redirectTransport{to: "http://example.com/second", setCookie: "sid=abc; Path=/"}
		},
	)
}
