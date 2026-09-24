package sntp

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// server answers one query the way a stratum-2 server whose clock runs ahead by skew would.
func server(t *testing.T, skew time.Duration, mangle func([]byte)) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })

	go func() {
		buf := make([]byte, 64)
		n, from, err := pc.ReadFrom(buf)
		if err != nil || n < packetLen {
			return
		}
		now := time.Now().Add(skew)
		resp := make([]byte, packetLen)
		resp[0] = 0x24 // LI 0, version 4, mode 4
		resp[1] = 2
		copy(resp[24:32], buf[40:48]) // originate = client transmit
		putTimestamp(resp[32:], now)  // receive
		putTimestamp(resp[40:], now)  // transmit
		if mangle != nil {
			mangle(resp)
		}
		_, _ = pc.WriteTo(resp, from)
	}()
	return pc.LocalAddr().String()
}

func TestQueryMeasuresTheOffset(t *testing.T) {
	addr := server(t, 2*time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r, err := Query(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if r.Offset < 1900*time.Millisecond || r.Offset > 2100*time.Millisecond {
		t.Fatalf("offset %v, want about 2s", r.Offset)
	}
	if r.Delay < 0 || r.Delay > time.Second {
		t.Fatalf("delay %v", r.Delay)
	}
	if r.Stratum != 2 || r.Server != addr {
		t.Fatalf("stratum %d server %s", r.Stratum, r.Server)
	}
}

func TestQueryRejectsBadAnswers(t *testing.T) {
	cases := map[string]func([]byte){
		"kiss of death":         func(b []byte) { b[1] = 0; copy(b[12:16], "RATE") },
		"wrong mode":            func(b []byte) { b[0] = 0x23 },
		"not our question":      func(b []byte) { b[31] ^= 0xff },
		"no transmit timestamp": func(b []byte) { binary.BigEndian.PutUint64(b[40:], 0) },
	}
	for name, mangle := range cases {
		t.Run(name, func(t *testing.T) {
			addr := server(t, 0, mangle)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := Query(ctx, addr); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestQueryTimesOut(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Query(ctx, pc.LocalAddr().String()); err == nil {
		t.Fatal("a silent server answered")
	}
}

func TestTimestampRoundTrip(t *testing.T) {
	now := time.Unix(1790090005, 123456789)
	var b [8]byte
	putTimestamp(b[:], now)
	back := timestamp(b[:])
	if d := back.Sub(now); d < -time.Nanosecond || d > time.Nanosecond {
		t.Fatalf("round trip drifted by %v", d)
	}
}
