package limiter

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// KeyFunc picks the rate limit key for a request. Returning "" means the
// client could not be identified, and the middleware rejects the request.
//
// Only key by something the client cannot choose: its address, or an identity
// your authentication already verified. A raw header or cookie is not that. A
// client that invents a new value per request gets a new bucket per request.
type KeyFunc func(r *http.Request) string

// ClientIP keys requests by client IP address.
//
// With no arguments it uses the TCP peer address and ignores every header,
// because X-Forwarded-For is written by the client and can hold anything.
// Pass the address ranges of your own proxies or load balancer to have
// X-Forwarded-For read, and only for requests that arrive from them.
//
// IPv6 addresses are grouped by /64, since one user normally owns a whole /64.
func ClientIP(trustedProxies ...netip.Prefix) KeyFunc {
	trusted := func(addr netip.Addr) bool {
		for _, p := range trustedProxies {
			if p.Contains(addr) {
				return true
			}
		}
		return false
	}

	return func(r *http.Request) string {
		peer, ok := parseAddr(r.RemoteAddr)
		if !ok {
			return ""
		}
		if !trusted(peer) {
			return ipKey(peer)
		}

		client := peer
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for _, hop := range slices.Backward(hops) {
			addr, ok := parseAddr(hop)
			if !ok {
				break
			}
			client = addr
			if !trusted(addr) {
				break
			}
		}
		return ipKey(client)
	}
}

// FirstOf returns the first non-empty key, so a verified user ID can fall
// back to the client IP for anonymous traffic.
func FirstOf(fns ...KeyFunc) KeyFunc {
	return func(r *http.Request) string {
		for _, fn := range fns {
			if key := fn(r); key != "" {
				return key
			}
		}
		return ""
	}
}

func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

func ipKey(addr netip.Addr) string {
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}
