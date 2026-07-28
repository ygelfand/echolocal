package alog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

// Handler writes slog records to logd, and to a fallback writer so a run from an adb shell shows
// output too. Lines carry the uptime, because what matters about echod's log is when something
// happened relative to boot.
type Handler struct {
	l        *Logger
	fallback io.Writer
	attrs    []slog.Attr
	group    string

	mu *sync.Mutex
}

// NewHandler connects to logd. It never fails: without logd the handler writes to fallback only,
// since echod must not die for want of logging.
func NewHandler(tag string, fallback io.Writer) *Handler {
	h := &Handler{fallback: fallback, mu: &sync.Mutex{}}

	l, err := New(tag)
	if err != nil {
		fmt.Fprintf(fallback, "logcat unavailable: %v\n", err)
		return h
	}
	h.l = l
	return h
}

func (h *Handler) Close() error {
	if h.l == nil {
		return nil
	}
	return h.l.Close()
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
	fmt.Fprintf(&b, "[%8.2f] %s", Uptime(), r.Message)

	for _, a := range h.attrs {
		appendAttr(&b, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&b, h.group, a)
		return true
	})

	line := b.String()

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.fallback != nil {
		fmt.Fprintf(h.fallback, "%s %s\n", level(r.Level), line)
	}
	if h.l == nil {
		return nil
	}
	return h.l.Write(priority(r.Level), line)
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

// priority maps slog levels onto Android's, so errors show as E in logcat rather than I.
func priority(l slog.Level) byte {
	switch {
	case l >= slog.LevelError:
		return Error
	case l >= slog.LevelWarn:
		return Warn
	case l >= slog.LevelInfo:
		return Info
	default:
		return Debug
	}
}

func level(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "E"
	case l >= slog.LevelWarn:
		return "W"
	case l >= slog.LevelInfo:
		return "I"
	default:
		return "D"
	}
}

// Safely runs f, turning a panic into a log line rather than a dead process. Every goroutine needs
// its own: a panic in one cannot be recovered anywhere else, and init discards our stderr, so an
// uncaught one looks like a silent restart.
func Safely(what string, f func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from a panic", "in", what, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	f()
}

// Uptime is seconds since boot.
func Uptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(string(b), " ")
	v, _ := strconv.ParseFloat(first, 64)
	return v
}
