package auth

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/patent-dev/bulk-file-loader/config"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func newRequest(remoteAddr, xfp string, tlsConn bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xfp != "" {
		r.Header.Set("X-Forwarded-Proto", xfp)
	}
	if tlsConn {
		r.TLS = &tls.ConnectionState{}
	}
	return r
}

func TestCookieSecure(t *testing.T) {
	trusted := []*net.IPNet{mustCIDR(t, "127.0.0.1/32")}

	cases := []struct {
		name string
		cfg  *config.Config
		req  *http.Request
		want bool
	}{
		{"insecure-cookie override beats everything",
			&config.Config{InsecureCookie: true}, newRequest("1.2.3.4:80", "", true), false},
		{"direct TLS",
			&config.Config{}, newRequest("1.2.3.4:80", "", true), true},
		{"untrusted proxy claiming https is ignored",
			&config.Config{}, newRequest("1.2.3.4:80", "https", false), true},
		{"trusted proxy + https",
			&config.Config{TrustedProxies: trusted}, newRequest("127.0.0.1:9999", "https", false), true},
		{"trusted proxy + http",
			&config.Config{TrustedProxies: trusted}, newRequest("127.0.0.1:9999", "http", false), false},
		{"trusted proxy + chain takes leftmost",
			&config.Config{TrustedProxies: trusted}, newRequest("127.0.0.1:9999", "https, http", false), true},
		{"trusted proxy without XFP falls through to default",
			&config.Config{TrustedProxies: trusted}, newRequest("127.0.0.1:9999", "", false), true},
		{"trusted proxy + no XFP + dev mode",
			&config.Config{TrustedProxies: trusted, DevMode: true}, newRequest("127.0.0.1:9999", "", false), false},
		{"dev mode plain http",
			&config.Config{DevMode: true}, newRequest("1.2.3.4:80", "", false), false},
		{"default plain http stays secure",
			&config.Config{}, newRequest("1.2.3.4:80", "", false), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{cfg: tc.cfg}
			if got := s.cookieSecure(tc.req); got != tc.want {
				t.Errorf("cookieSecure = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestForwardedProto(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"https", "https"},
		{"HTTPS", "https"},
		{"  https  ", "https"},
		{"https, http", "https"},
		{" http , https ", "http"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.in != "" {
			r.Header.Set("X-Forwarded-Proto", c.in)
		}
		if got := forwardedProto(r); got != c.want {
			t.Errorf("forwardedProto(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
