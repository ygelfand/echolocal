// Package logd writes echod's log to Android's log daemon, which is where `adb logcat` reads it.
//
// Lines are stamped with the uptime rather than the wall clock, because the clock is wrong until
// something sets it and a device that has just booted may never have been told.
package logd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ygelfand/echolocal/internal/hardware/metrics"
)

// streamDepth is how far a reader of Lines may fall behind before it starts losing the oldest. The
// daemon still has every one of them.
const streamDepth = 256

// Handler writes slog records to the daemon, and to a fallback writer so running echod by hand from
// an adb shell still prints something.
type Handler struct {
	conn     *conn
	fallback io.Writer
	attrs    []slog.Attr
	group    string

	mu *sync.Mutex
}

type Line struct {
	Level slog.Level
	Text  string

	// Dropped is how many lines were lost before this one, so a gap can be reported rather than read
	// as nothing having happened.
	Dropped uint64
}

type stream struct {
	lines   chan Line
	dropped atomic.Uint64
}

// One process has one log, the same reason slog has a default. Nothing waits on it: logging happens
// on whatever goroutine had something to say, including the one feeding audio.
var out = stream{lines: make(chan Line, streamDepth)}

// Lines is the log from now on, for anything that wants to carry it somewhere else — the API sends
// it to Home Assistant. With nobody reading it costs one buffer and then nothing.
func Lines() <-chan Line { return out.lines }

// publish makes room by throwing away the oldest line rather than the newest: a live log is worth
// more than its history.
func (s *stream) publish(l Line) {
	l.Dropped = s.dropped.Swap(0)

	select {
	case s.lines <- l:
		return
	default:
	}

	select {
	case <-s.lines:
		s.dropped.Add(1)
	default:
	}

	select {
	case s.lines <- l:
	default:
		// Someone else took the room in between. Count this line and carry its tally forward.
		s.dropped.Add(1 + l.Dropped)
	}
}

// NewHandler never fails: with no daemon it writes to fallback only, since echod must not die for
// want of logging.
func NewHandler(tag string, fallback io.Writer) *Handler {
	h := &Handler{fallback: fallback, mu: &sync.Mutex{}}

	c, err := dial(tag)
	if err != nil {
		fmt.Fprintf(fallback, "logcat unavailable: %v\n", err)
		return h
	}
	h.conn = c
	return h
}

func (h *Handler) Close() error {
	if h.conn == nil {
		return nil
	}
	return h.conn.Close()
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := *h
	out.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &out
}

func (h *Handler) WithGroup(name string) slog.Handler {
	out := *h
	out.group = name
	return &out
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[%8.2f] %s", metrics.Uptime(), r.Message)

	for _, a := range h.attrs {
		appendAttr(&b, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&b, h.group, a)
		return true
	})

	line := b.String()

	out.publish(Line{Level: r.Level, Text: line})

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.fallback != nil {
		fmt.Fprintf(h.fallback, "%s %s\n", Letter(r.Level), line)
	}
	if h.conn == nil {
		return nil
	}
	return h.conn.write(Priority(r.Level), line)
}

func appendAttr(b *strings.Builder, group string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	b.WriteByte(' ')
	if group != "" {
		b.WriteString(group)
		b.WriteByte('.')
	}
	b.WriteString(a.Key)
	b.WriteByte('=')
	b.WriteString(value(a.Value))
}

// value keeps numbers and short strings bare, and quotes anything with a space in it so a
// message with an embedded error stays one readable field.
func value(v slog.Value) string {
	s := v.String()
	if strings.ContainsAny(s, " \t=\"") {
		return strconv.Quote(s)
	}
	return s
}
