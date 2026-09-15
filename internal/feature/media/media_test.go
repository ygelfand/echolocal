package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// wave builds a RIFF stream: the header, the chunks given, then samples.
func wave(chunks []byte, samples []byte) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(0xFFFFFFFF))
	b.WriteString("WAVE")
	b.Write(chunks)
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(0xFFFFFFFF))
	b.Write(samples)
	return b.Bytes()
}

func fmtChunk(channels uint16, rate uint32, bits uint16) []byte {
	var b bytes.Buffer
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, channels)
	_ = binary.Write(&b, binary.LittleEndian, rate)
	_ = binary.Write(&b, binary.LittleEndian, rate*uint32(channels)*uint32(bits)/8)
	_ = binary.Write(&b, binary.LittleEndian, channels*bits/8)
	_ = binary.Write(&b, binary.LittleEndian, bits)
	return b.Bytes()
}

// chunk builds one of any size, which is how anything but fmt and data is skipped.
func namedChunk(id string, size int) []byte {
	var b bytes.Buffer
	b.WriteString(id)
	_ = binary.Write(&b, binary.LittleEndian, uint32(size))
	b.Write(bytes.Repeat([]byte{0xAB}, size+size%2))
	return b.Bytes()
}

func TestHeaderLeavesTheReaderOnTheSamples(t *testing.T) {
	good := fmtChunk(speaker.Channels, speaker.Rate, 16)
	samples := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	for _, tc := range []struct {
		name   string
		chunks []byte
	}{
		{"fmt only", good},
		{"chunk before fmt", append(namedChunk("LIST", 10), good...)},
		{"chunk after fmt", append(good, namedChunk("fact", 4)...)},
		{"odd sized chunk", append(namedChunk("LIST", 7), good...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bufio.NewReader(bytes.NewReader(wave(tc.chunks, samples)))
			if err := header(r); err != nil {
				t.Fatalf("header: %v", err)
			}

			rest, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("reading the samples: %v", err)
			}
			if !bytes.Equal(rest, samples) {
				t.Errorf("left the reader on %v, want %v", rest, samples)
			}
		})
	}
}

func TestHeaderRefusesWhatCannotBePlayed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  []byte
		wants string
	}{
		{"wrong rate", wave(fmtChunk(speaker.Channels, 44100, 16), nil), "44100 Hz"},
		{"wrong channels", wave(fmtChunk(1, speaker.Rate, 16), nil), "1 channel"},
		{"wrong depth", wave(fmtChunk(speaker.Channels, speaker.Rate, 24), nil), "24 bit"},
		{"not a wave", []byte("MP3 and then some more bytes"), "not a WAVE"},
		{"truncated", []byte("RIFF"), "WAVE header"},
		{"no data chunk", append([]byte("RIFF\xff\xff\xff\xffWAVE"), fmtChunk(2, 48000, 16)...), "WAVE chunk"},
		{"absurd chunk", append([]byte("RIFF\xff\xff\xff\xffWAVE"), namedChunk("LIST", 4)[:8]...), "LIST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := header(bufio.NewReader(bytes.NewReader(tc.body)))
			if err == nil {
				t.Fatal("played it anyway")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("said %q, want something about %q", err, tc.wants)
			}
		})
	}
}

// A chunk whose size is a lie is what a truncated download looks like, and the size is what the
// header allocates from.
func TestHeaderRefusesAnOversizedChunk(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("RIFF\xff\xff\xff\xffWAVE")
	b.WriteString("LIST")
	_ = binary.Write(&b, binary.LittleEndian, uint32(1<<30))

	err := header(bufio.NewReader(bytes.NewReader(b.Bytes())))
	if err == nil {
		t.Fatal("accepted a chunk of a gigabyte")
	}
}

// A claim stands the stream down; if it is stopped before the claim ends and nothing restarts until
// after, the stale suspend count must not linger to gate the next track. This is what stopping the
// noise during an announcement followed by a new layer selection does, and it used to leave the
// player with a count that no resume would ever answer.
func TestStoppingDuringAHoldDoesNotWedgeTheNextStart(t *testing.T) {
	spk := speaker.New()
	d := speaker.NewDriver(spk)
	s := NewStream(d, spk, func() {})

	s.start(&track{item: "noise"})
	s.bg.Suspend()
	if s.holds != 1 {
		t.Fatalf("the claim did not stand the stream down: holds %d", s.holds)
	}

	// The sound is stopped before the claim ends.
	s.Stop()
	if s.holds != 0 {
		t.Errorf("stopping under the claim left a count of %d behind", s.holds)
	}
	s.bg.Resume()

	// A new sound after the claim starts clean, not gated by the stale remainder.
	s.start(&track{item: "again"})
	if s.holds != 0 {
		t.Errorf("the new track started into a gate nobody opens: holds %d", s.holds)
	}
	s.Stop()
}

// Stopping under a claim and starting again before it ends has to stand the new track down once, or
// it would play over the claim. The stop releases everything and takes the stream out of the stack,
// so the retake is held afresh for the still-running claim and released when it ends.
func TestARetakeAfterStoppingDuringAHoldIsStoodDownAgain(t *testing.T) {
	spk := speaker.New()
	d := speaker.NewDriver(spk)
	s := NewStream(d, spk, func() {})

	s.start(&track{item: "noise"})
	s.bg.Suspend()
	if s.holds != 1 {
		t.Fatalf("the claim did not stand the stream down: holds %d", s.holds)
	}

	// Stop, then a fresh sound before the claim ends.
	s.Stop()
	s.start(&track{item: "again"})
	if s.holds != 1 {
		t.Errorf("the retake was not stood down for the claim: holds %d", s.holds)
	}

	s.bg.Resume()
	if s.holds != 0 {
		t.Errorf("the claim never released it: holds %d", s.holds)
	}
	s.Stop()
}

// A turn in OnTurnPause mode stands the stream down once, and a track change under the same turn
// must not stand it down a second time: Duck is redelivered on the retake, but one resume at the
// turn's end is all there is to answer it with. It used to leave the count behind, silent until the
// next stop.
func TestATrackChangeUnderAPausingTurnIsNotPausedTwice(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Media().OnTurn(config.OnTurnPause); err != nil {
		t.Fatal(err)
	}

	spk := speaker.New()
	d := speaker.NewDriver(spk)
	s := NewStream(d, spk, func() {})

	s.start(&track{item: "noise"})
	s.bg.Duck(true)
	if s.holds != 1 {
		t.Fatalf("the pausing turn did not stand the stream down once: holds %d", s.holds)
	}

	// A track change under the same turn.
	s.start(&track{item: "again"})
	if s.holds != 1 {
		t.Errorf("the track change stood the stream down a second time: holds %d", s.holds)
	}

	s.bg.Duck(false)
	if s.holds != 0 {
		t.Errorf("the turn never released it: holds %d", s.holds)
	}
	s.Stop()
}
