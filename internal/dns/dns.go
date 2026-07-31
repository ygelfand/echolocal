// Package dns teaches Go's resolver where this device keeps its nameservers.
//
// Android has no /etc/resolv.conf: bionic reads the nameservers from system properties, and echod is
// built without cgo so it gets Go's own resolver, which only knows about the file. Every lookup
// therefore goes to [::1]:53 and is refused. Nothing noticed for a long time because Home Assistant is
// reached by address and the wake word models come from Home Assistant — the first name anything had to
// resolve was a release manifest.
package dns

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/prop"
)

var props = []string{"net.dns1", "net.dns2", "net.dns3", "net.dns4"}

const (
	// timeout bounds one lookup, short enough that a nameserver which is not answering falls through to
	// the next rather than holding up whatever asked.
	timeout = 5 * time.Second

	// fresh is how long the nameservers are trusted before being read again. They come from DHCP and
	// change when the device joins another network, so reading them once would leave a moved device
	// resolving against a nameserver that is no longer there. Reading them every lookup is worse: each
	// one is a getprop, and whatever starts resolving in a loop later would be spawning processes to do
	// it. A minute is short against how often a device changes network and long against anything that
	// resolves in earnest.
	fresh = time.Minute
)

// Use makes every lookup in this process go to the nameservers the platform reports, by replacing the
// resolver the standard library shares. Nothing else has to be told: http.DefaultClient included.
func Use() {
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dial}
	slog.Info("resolver configured", "nameservers", nameservers())
}

// dial ignores the address Go derived from the resolv.conf it could not find and uses what the platform
// says, trying each in turn.
func dial(ctx context.Context, network, _ string) (net.Conn, error) {
	servers := nameservers()
	if len(servers) == 0 {
		return nil, fmt.Errorf("dns: the device reports no nameservers")
	}

	var err error
	for _, server := range servers {
		d := net.Dialer{Timeout: timeout}

		var conn net.Conn
		if conn, err = d.DialContext(ctx, network, net.JoinHostPort(server, "53")); err == nil {
			return conn, nil
		}
	}
	return nil, err
}

var (
	mu     sync.Mutex
	cached []string
	read   time.Time
)

func nameservers() []string {
	mu.Lock()
	defer mu.Unlock()

	if cached != nil && time.Since(read) < fresh {
		return cached
	}

	var out []string
	for _, name := range props {
		if v, err := prop.Get(name); err == nil && v != "" {
			out = append(out, v)
		}
	}

	cached, read = out, time.Now()
	return out
}
