package logd

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"
)

// socket is the daemon's write endpoint. logcat reads the other one.
const socket = "/dev/socket/logdw"

// idMain is the buffer applications log to, as used by liblog.
const idMain = 0

// severity is one rung of the ladder: what slog calls it, what Android's android_LogPriority calls
// it, and the letter logcat prints.
type severity struct {
	Level    slog.Level
	Priority byte
	Letter   string
}

// severities is the ladder, most severe first. Everything that has to say how bad a line is reads
// this rather than writing the thresholds out again.
var severities = []severity{
	{slog.LevelError, 6, "E"},
	{slog.LevelWarn, 5, "W"},
	{slog.LevelInfo, 4, "I"},
	{slog.LevelDebug, 3, "D"},
}

func rung(l slog.Level) severity {
	for _, s := range severities {
		if l >= s.Level {
			return s
		}
	}
	return severities[len(severities)-1]
}

// Priority is what Android calls a slog level, so an error shows as E in logcat rather than I.
func Priority(l slog.Level) byte { return rung(l).Priority }

// Letter is the one-character level the fallback writer prints.
func Letter(l slog.Level) string { return rung(l).Letter }

// Level is the reverse, for reading back entries something else logged. Anything above Error is
// Android's fatal, which is still an error.
func Level(prio byte) slog.Level {
	for _, s := range severities {
		if prio >= s.Priority {
			return s.Level
		}
	}
	return slog.LevelDebug
}

type conn struct {
	sock net.Conn
	tag  string
}

// dial connects to the daemon. It fails when logd is not up or the domain may not write to it, which
// is why nothing here is required: echod must not die for want of logging.
func dial(tag string) (*conn, error) {
	c, err := net.Dial("unixgram", socket)
	if err != nil {
		return nil, fmt.Errorf("logd: dial %s: %w", socket, err)
	}
	return &conn{sock: c, tag: tag}, nil
}

func (c *conn) Close() error { return c.sock.Close() }

// write sends one entry. The wire format is a packed header — buffer id, thread id, realtime —
// followed by priority, NUL-terminated tag and NUL-terminated message.
func (c *conn) write(prio byte, msg string) error {
	now := time.Now()

	buf := make([]byte, 0, 11+1+len(c.tag)+1+len(msg)+1)
	buf = append(buf, idMain)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(os.Getpid()))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(now.Unix()))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(now.Nanosecond()))
	buf = append(buf, prio)
	buf = append(buf, c.tag...)
	buf = append(buf, 0)
	buf = append(buf, msg...)
	buf = append(buf, 0)

	_, err := c.sock.Write(buf)
	return err
}
