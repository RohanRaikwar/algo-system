package smartconnect

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/gorilla/websocket"
)

// lookupHost resolves a hostname; swapped in tests.
var lookupHost = net.DefaultResolver.LookupHost

// NewPinnedDialer returns a websocket dialer that prefers the index-th address
// (in sorted order) the feed hostname resolves to, then tries the others.
// Parallel feed sockets built with different indexes land on different
// server IPs, so a loss burst or server-side stall on one path does not hit
// them all. The URL hostname is unchanged, so TLS SNI and certificate checks
// are unaffected.
func NewPinnedDialer(index int) *websocket.Dialer {
	// Short per-address timeout: a blackholed IP (SYNs dropped) must not hold
	// the redial for long when other Angel IPs are up. RTT is ~100ms.
	nd := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := lookupHost(ctx, host)
			if err != nil || len(ips) == 0 {
				return nd.DialContext(ctx, network, addr)
			}
			var errs []error
			for _, ip := range pinnedOrder(ips, index) {
				conn, derr := nd.DialContext(ctx, network, net.JoinHostPort(ip, port))
				if derr == nil {
					return conn, nil
				}
				errs = append(errs, derr)
			}
			return nil, errors.Join(errs...)
		},
	}
}

// pinnedOrder sorts ips and rotates them so the index-th one comes first.
func pinnedOrder(ips []string, index int) []string {
	out := append([]string(nil), ips...)
	sort.Strings(out)
	if index < 0 {
		index = -index
	}
	k := index % len(out)
	return append(out[k:], out[:k]...)
}
