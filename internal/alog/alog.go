// Package alog writes to Android's logd so echod's output shows up in logcat, which is
// ring-buffered by the system and cannot grow without bound the way a file on /dev would.
package alog

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// Socket is logd's write endpoint.
const Socket = "/dev/socket/logdw"

// Log buffer ids, as used by liblog.
const idMain = 0

// Priorities, matching android_LogPriority.
const (
	Verbose byte = 2
	Debug   byte = 3
	Info    byte = 4
	Warn    byte = 5
	Error   byte = 6
)

// Logger writes tagged entries to the main log buffer.
type Logger struct {
	conn net.Conn
	tag  string
}

// New connects to logd. It fails when logd is not up or the domain may not write to it, so
// callers that must not die for want of logging should fall back to stderr.
func New(tag string) (*Logger, error) {
	c, err := net.Dial("unixgram", Socket)
	if err != nil {
		return nil, fmt.Errorf("alog: dial %s: %w", Socket, err)
	}
	return &Logger{conn: c, tag: tag}, nil
}

func (l *Logger) Close() error { return l.conn.Close() }

// Write sends one entry. The wire format is a packed header — buffer id, thread id, realtime —
// followed by priority, NUL-terminated tag and NUL-terminated message.
func (l *Logger) Write(prio byte, msg string) error {
	now := time.Now()

	buf := make([]byte, 0, 11+1+len(l.tag)+1+len(msg)+1)
	buf = append(buf, idMain)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(os.Getpid()))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(now.Unix()))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(now.Nanosecond()))
	buf = append(buf, prio)
	buf = append(buf, l.tag...)
	buf = append(buf, 0)
	buf = append(buf, msg...)
	buf = append(buf, 0)

	_, err := l.conn.Write(buf)
	return err
}

func (l *Logger) Infof(format string, args ...any) error {
	return l.Write(Info, fmt.Sprintf(format, args...))
}

// Writer adapts the logger for packages that write lines, such as slog.
func (l *Logger) Writer(prio byte) io.Writer { return &writer{l: l, prio: prio} }

type writer struct {
	l    *Logger
	prio byte
}

func (w *writer) Write(p []byte) (int, error) {
	if err := w.l.Write(w.prio, strings.TrimRight(string(p), "\n")); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (l *Logger) Errorf(format string, args ...any) error {
	return l.Write(Error, fmt.Sprintf(format, args...))
}
