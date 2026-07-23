package middleware_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/server/middleware"
)

func newTrustedProxyHandler(t *testing.T, cidrs []string) (http.Handler, *string) {
	t.Helper()
	mw, err := middleware.TrustedProxy(cidrs)
	if err != nil {
		t.Fatalf("TrustedProxy(%v) returned error: %v", cidrs, err)
	}
	var seenRemoteAddr string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRemoteAddr = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))
	return h, &seenRemoteAddr
}

func TestTrustedProxySubstitutesXFFFromTrustedPeer(t *testing.T) {
	t.Parallel()

	h, seen := newTrustedProxyHandler(t, nil) // nil -> package default CIDRs

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.12.7.7:54321" // inside default 10.0.0.0/8
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 10.12.7.7")

	h.ServeHTTP(httptest.NewRecorder(), req)

	host, _, err := splitHost(*seen)
	if err != nil {
		t.Fatalf("recorded RemoteAddr %q did not parse: %v", *seen, err)
	}
	if host != "203.0.113.42" {
		t.Errorf("RemoteAddr host = %q; want the leftmost X-Forwarded-For entry 203.0.113.42", host)
	}
}

func TestTrustedProxyIgnoresXFFFromUntrustedPeer(t *testing.T) {
	t.Parallel()

	h, seen := newTrustedProxyHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:54321" // a public IP, not in the default trusted set
	req.Header.Set("X-Forwarded-For", "198.51.100.1")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if *seen != req.RemoteAddr {
		t.Errorf("RemoteAddr = %q; want unchanged %q (untrusted peer must not have its XFF honoured)", *seen, req.RemoteAddr)
	}
}

func TestTrustedProxyLeavesRemoteAddrAloneWithoutXFF(t *testing.T) {
	t.Parallel()

	h, seen := newTrustedProxyHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	// No X-Forwarded-For header set.

	h.ServeHTTP(httptest.NewRecorder(), req)

	if *seen != req.RemoteAddr {
		t.Errorf("RemoteAddr = %q; want unchanged %q (no XFF header present)", *seen, req.RemoteAddr)
	}
}

func TestTrustedProxyIgnoresMalformedXFFEntry(t *testing.T) {
	t.Parallel()

	h, seen := newTrustedProxyHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "not-an-ip")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if *seen != req.RemoteAddr {
		t.Errorf("RemoteAddr = %q; want unchanged %q (malformed XFF entry must not be substituted in)", *seen, req.RemoteAddr)
	}
}

func TestTrustedProxyRejectsInvalidCIDR(t *testing.T) {
	t.Parallel()

	_, err := middleware.TrustedProxy([]string{"not-a-cidr"})
	if err == nil {
		t.Fatal("TrustedProxy with an invalid CIDR: want error, got nil")
	}
}

func TestTrustedProxyCustomCIDRList(t *testing.T) {
	t.Parallel()

	h, seen := newTrustedProxyHandler(t, []string{"192.168.100.0/24"})

	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "192.168.100.5:1234"
	trusted.Header.Set("X-Forwarded-For", "203.0.113.42")
	h.ServeHTTP(httptest.NewRecorder(), trusted)
	if host, _, _ := splitHost(*seen); host != "203.0.113.42" {
		t.Errorf("with custom CIDR: RemoteAddr host = %q; want 203.0.113.42", host)
	}

	// The default 10.0.0.0/8 range must NOT be trusted once a custom
	// list is supplied - it should fully replace the default, not merge.
	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "10.0.0.5:1234"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.42")
	h.ServeHTTP(httptest.NewRecorder(), untrusted)
	if *seen != untrusted.RemoteAddr {
		t.Errorf("with custom CIDR: RemoteAddr = %q; want unchanged %q (default range must not leak in)", *seen, untrusted.RemoteAddr)
	}
}

func splitHost(addr string) (string, string, error) {
	return net.SplitHostPort(addr)
}
