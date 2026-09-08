package sendspin

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

const Port = 8928

// listener accepts the servers that dial in, one at a time. The spec ranks competing servers by
// declared activity; until that is implemented the first to arrive holds the room.
type listener struct {
	out   *out
	bg    *speaker.Arbiter
	trust *trust

	// report and security say what the room is doing, for the diagnostic sensors. Called from the
	// accept goroutine and from the session, so whatever they write to has to tolerate that.
	report   func(string)
	security func(string)

	// goodbye is why the room is leaving when the listener is stopped.
	goodbye func() string

	mu      sync.Mutex
	current *session
}

func newListener(o *out, bg *speaker.Arbiter, tr *trust, report, security func(string), goodbye func() string) *listener {
	return &listener{out: o, bg: bg, trust: tr, report: report, security: security, goodbye: goodbye}
}

// serve holds the port until ctx ends.
func (l *listener) serve(ctx context.Context, name string) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(Port)))
	if err != nil {
		return err
	}

	up := websocket.Upgrader{
		// Any origin: a server dialing in is not a browser, and there is nothing here a page could
		// reach that the network cannot already.
		CheckOrigin: func(*http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("sendspin upgrade failed", "from", r.RemoteAddr, "err", err)
			return
		}
		defer conn.Close()

		s := newSession(conn, l.trust, l.out, l.bg, name, l.report, l.security, l.goodbye)
		if !l.take(s) {
			// The spec has a second server judged by what it declares once the handshake is done. Until
			// that is built, the room is simply busy, and the socket closing says so.
			slog.Info("sendspin turning away a second server", "from", r.RemoteAddr)
			return
		}
		defer l.give(s)

		slog.Info("sendspin server connected", "from", r.RemoteAddr)
		l.report(stateJoined)
		defer l.report(stateWaiting)
		defer l.security(securityWaiting)

		err = s.run(ctx)
		switch {
		case errors.Is(err, errPreamble):
			// Closed without a word, as the spec has it: a server on the old, unencrypted protocol
			// lands here too, and there is nothing to tell it that it would understand.
			slog.Warn("sendspin handshake failed", "from", r.RemoteAddr, "err", err)
		case err != nil:
			slog.Warn("sendspin session ended", "from", r.RemoteAddr, "err", err)
		default:
			slog.Info("sendspin server disconnected", "from", r.RemoteAddr)
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	// Closing the server unblocks Serve and hangs up on whatever is connected, which is what stopping
	// means here: the room is leaving the group, not pausing.
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// take admits one server and turns away the rest.
func (l *listener) take(s *session) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != nil {
		return false
	}
	l.current = s
	return true
}

func (l *listener) give(s *session) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current == s {
		l.current = nil
	}
}

// unpairedOff is the operator withdrawing unpaired access: a server that relied on it is told to pair
// and hung up on. A paired server is not relying on it, and stays.
func (l *listener) unpairedOff() {
	l.mu.Lock()
	s := l.current
	l.mu.Unlock()
	if s != nil && s.cat() == categorySentinel {
		slog.Info("sendspin unpaired access withdrawn, leaving", "server", short(s.c.serverID))
		s.kick(goodbyePairingRequired)
	}
}
