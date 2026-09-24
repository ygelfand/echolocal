// Package sntp asks a time server what time it is.
//
// It is the client half of RFC 4330: one 48-byte packet out, one back, and four timestamps that say
// how far this clock is from the server's and how long the question spent in the network. A device
// that only needs to be right to a few milliseconds needs nothing more than that.
package sntp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// Result is what one exchange said.
type Result struct {
	// Offset is how far this clock is behind the server's: add it to local time to agree.
	Offset time.Duration
	// Delay is the round trip less the server's own handling, the error bar on Offset.
	Delay   time.Duration
	Stratum int
	Server  string
}

const (
	packetLen = 48

	// The first byte: leap indicator 0, version 4, mode 3 (client). A server answers with mode 4.
	clientHeader  = 0x23
	modeMask      = 0x07
	modeServer    = 4
	modeBroadcast = 5

	// NTP counts seconds from 1900; Unix from 1970.
	epochOffset = 2208988800

	// Port is where time servers listen.
	Port = "123"
)

// Query asks the server at addr, which is a host or host:port, and waits for the answer or ctx.
func Query(ctx context.Context, addr string) (Result, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, Port)
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	req := make([]byte, packetLen)
	req[0] = clientHeader
	t1 := time.Now()
	putTimestamp(req[40:], t1)
	if _, err := conn.Write(req); err != nil {
		return Result{}, err
	}

	resp := make([]byte, packetLen)
	n, err := conn.Read(resp)
	t4 := time.Now()
	if err != nil {
		return Result{}, err
	}
	return parse(resp[:n], t1, t4, addr)
}

// parse checks the answer and works out the offset from the four timestamps.
func parse(resp []byte, t1, t4 time.Time, server string) (Result, error) {
	if len(resp) < packetLen {
		return Result{}, fmt.Errorf("sntp: %d byte reply from %s", len(resp), server)
	}
	if mode := resp[0] & modeMask; mode != modeServer && mode != modeBroadcast {
		return Result{}, fmt.Errorf("sntp: %s answered in mode %d", server, mode)
	}
	stratum := int(resp[1])
	if stratum == 0 {
		// A kiss-of-death: the server is telling us to go away, and the reference field says why.
		return Result{}, fmt.Errorf("sntp: %s refused: %q", server, string(resp[12:16]))
	}
	// The originate timestamp is our own transmit time echoed back, which is what ties the answer to
	// the question. A stale or forged packet fails here.
	if !sameTimestamp(resp[24:], t1) {
		return Result{}, fmt.Errorf("sntp: %s answered another question", server)
	}
	t2 := timestamp(resp[32:])
	t3 := timestamp(resp[40:])
	if t3.IsZero() || t3.Unix() == -epochOffset {
		return Result{}, fmt.Errorf("sntp: %s sent no transmit time", server)
	}

	offset := (t2.Sub(t1) + t3.Sub(t4)) / 2
	delay := t4.Sub(t1) - t3.Sub(t2)
	return Result{Offset: offset, Delay: delay, Stratum: stratum, Server: server}, nil
}

// putTimestamp writes t as a 64-bit NTP timestamp: seconds since 1900, then a 32-bit fraction.
func putTimestamp(b []byte, t time.Time) {
	secs := uint64(t.Unix()) + epochOffset
	frac := uint64(t.Nanosecond()) << 32 / 1e9
	binary.BigEndian.PutUint64(b, secs<<32|frac)
}

// timestamp reads one. The zero timestamp reads as the NTP epoch, which callers test for.
func timestamp(b []byte) time.Time {
	v := binary.BigEndian.Uint64(b)
	secs := int64(v>>32) - epochOffset
	nanos := int64((v & 0xffffffff) * 1e9 >> 32)
	return time.Unix(secs, nanos)
}

// sameTimestamp says whether the wire bytes are what putTimestamp would have written for t: the
// fraction loses precision below a quarter of a nanosecond, so the comparison is on the encoding.
func sameTimestamp(b []byte, t time.Time) bool {
	var ours [8]byte
	putTimestamp(ours[:], t)
	return string(ours[:]) == string(b[:8])
}

// ErrNoServers is Sync's answer when it was given nothing to ask.
var ErrNoServers = errors.New("sntp: no servers")
