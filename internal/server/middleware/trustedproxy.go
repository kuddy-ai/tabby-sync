package middleware

import (
	"net"
	"net/http"
	"strings"
)

// defaultTrustedProxyCIDRs covers the private/loopback ranges a reverse
// proxy container sits in when it and tabby-sync share a Docker bridge
// network (RFC1918 IPv4, IPv6 unique-local, and loopback for same-host
// setups). This is deliberately a private-range allowlist rather than
// "trust everything": blindly honouring X-Forwarded-For from any peer
// lets an internet client forge their logged/rate-limited IP by simply
// sending the header themselves. Only requests whose immediate TCP peer
// (r.RemoteAddr) falls in one of these ranges get their
// X-Forwarded-For value substituted in; every other peer is logged and
// rate-limited by its real connecting address, forgeable header or not.
var defaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"fc00::/7", // IPv6 unique-local, covers caddy-net's fd00::/48 range
}

// TrustedProxy returns a middleware that replaces r.RemoteAddr with the
// client IP taken from the leftmost entry of an inbound X-Forwarded-For
// header, but ONLY when the immediate TCP peer is in cidrs (or the
// package default when cidrs is nil). This must run before any
// middleware that reads r.RemoteAddr for logging or rate-limiting -
// AccessLog and ratelimit.Middleware both do - so those consumers see
// the real client address instead of the reverse proxy's container IP.
//
// Caddy (and most reverse proxies) prepends the connecting client's IP
// to any existing X-Forwarded-For and forwards the result, so the
// leftmost entry is the original client; entries to its right, if any,
// are intermediate proxies the request already passed through before
// reaching this deployment's Caddy instance.
//
// A malformed or unparsable header, or a peer outside the trusted set,
// leaves r.RemoteAddr untouched: the request is passed through with
// whatever address the TCP layer actually reports.
func TrustedProxy(cidrs []string) (Middleware, error) {
	if cidrs == nil {
		cidrs = defaultTrustedProxyCIDRs
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		nets = append(nets, n)
	}

	isTrusted := func(addr string) bool {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return false
		}
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			xff := r.Header.Get("X-Forwarded-For")
			if xff == "" || !isTrusted(r.RemoteAddr) {
				next.ServeHTTP(w, r)
				return
			}

			client := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(client) == nil {
				// Not a parsable IP; leave RemoteAddr as-is rather than
				// forward a value that would break net.SplitHostPort
				// downstream or let a client smuggle a non-IP string
				// into logs.
				next.ServeHTTP(w, r)
				return
			}

			r2 := r.Clone(r.Context())
			r2.RemoteAddr = net.JoinHostPort(client, "0")
			next.ServeHTTP(w, r2)
		})
	}, nil
}
