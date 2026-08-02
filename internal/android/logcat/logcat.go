// Package logcat reads what the rest of Android logged, which is the half of the story echod cannot
// tell: the kernel, mediaserver taking the audio device away, the vendor's own tags.
//
// logd hands it over on /dev/socket/logdr, the same way logcat gets it. The socket is SOCK_SEQPACKET,
// which net cannot dial, so it is opened directly. Each packet is one entry: a header whose size is
// in the header, then priority, tag and message.
package logcat

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"

	"github.com/ygelfand/echolocal/internal/android/logd"
)

const socket = "/dev/socket/logdr"

// The buffers worth having: main is where applications log and system is where the framework does.
// The rest carry radio traffic and binary events, which read as noise here.
const (
	lidMain   = 0
	lidSystem = 3
)

// hdrSize is where the payload starts in the oldest layout, and the shortest packet worth reading.
// Every version since says its own header size, which is what keeps this working across them.
const hdrSize = 20

// Entry is one line Android logged.
type Entry struct {
	Level slog.Level
	Tag   string
	Text  string
}

// Recent returns the last of what logd is holding, oldest first. logd is asked to dump and hang up
// rather than to follow, so this returns instead of waiting for more.
func Recent(lines int) ([]Entry, error) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		return nil, fmt.Errorf("logcat: socket: %w", err)
	}

	f := os.NewFile(uintptr(fd), socket)
	defer f.Close()

	if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: socket}); err != nil {
		return nil, fmt.Errorf("logcat: connect %s: %w", socket, err)
	}

	req := fmt.Sprintf("dumpAndClose lids=%d,%d tail=%d", lidMain, lidSystem, lines)
	if _, err := f.Write([]byte(req)); err != nil {
		return nil, fmt.Errorf("logcat: %q: %w", req, err)
	}

	var out []Entry
	buf := make([]byte, 5*1024)
	for {
		n, err := f.Read(buf)
		if n == 0 || err != nil {
			return out, nil
		}
		if e, ok := parse(buf[:n]); ok {
			out = append(out, e)
		}
	}
}

// parse reads one packet. A short or malformed one is skipped rather than failing the dump: this is
// somebody else's log, and we take what can be read of it.
func parse(pkt []byte) (Entry, bool) {
	if len(pkt) < hdrSize {
		return Entry{}, false
	}

	start := int(binary.LittleEndian.Uint16(pkt[2:]))
	if start < hdrSize || start > len(pkt) {
		start = hdrSize
	}

	body := pkt[start:]
	if len(body) < 3 {
		return Entry{}, false
	}

	// Priority, then the tag and the message, each ending at a zero byte.
	parts := strings.SplitN(strings.TrimRight(string(body[1:]), "\x00"), "\x00", 2)
	if len(parts) < 2 {
		return Entry{}, false
	}

	return Entry{Level: logd.Level(body[0]), Tag: parts[0], Text: parts[1]}, true
}
