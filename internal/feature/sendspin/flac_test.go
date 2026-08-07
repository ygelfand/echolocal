package sendspin

import (
	"bytes"
	"testing"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

const flacBlock = 1024

// flacStream encodes blocks of a known signal, and reports the samples that went in so what comes out
// can be held against them.
func flacStream(t *testing.T, blocks int) (encoded []byte, want []int16) {
	t.Helper()

	var buf bytes.Buffer
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlock,
		BlockSizeMax:  flacBlock,
		SampleRate:    speaker.Rate,
		NChannels:     speaker.Channels,
		BitsPerSample: speaker.Bits,
		NSamples:      uint64(flacBlock * blocks),
	}

	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		t.Fatalf("flac.NewEncoder: %v", err)
	}

	for b := range blocks {
		subs := make([]*frame.Subframe, speaker.Channels)
		for c := range subs {
			samples := make([]int32, flacBlock)
			for i := range samples {
				// Distinct per channel and per block, so a swap or an off-by-one block is visible.
				samples[i] = int32((b*flacBlock+i)%2000 - 1000 + c*3)
				want = append(want, 0)
			}
			subs[c] = &frame.Subframe{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   samples,
				NSamples:  flacBlock,
			}
		}

		// Interleave into want in the order the decoder must produce.
		at := b * flacBlock * speaker.Channels
		for i := range flacBlock {
			for c := range speaker.Channels {
				want[at+i*speaker.Channels+c] = int16(subs[c].Samples[i])
			}
		}

		f := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         flacBlock,
				SampleRate:        speaker.Rate,
				Channels:          frame.ChannelsLR,
				BitsPerSample:     speaker.Bits,
				Num:               uint64(b),
			},
			Subframes: subs,
		}
		if err := enc.WriteFrame(f); err != nil {
			t.Fatalf("WriteFrame %d: %v", b, err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("closing the encoder: %v", err)
	}
	return buf.Bytes(), want
}

// drain feeds the stream in pieces and gathers every frame that comes back, which is what the session
// does with chunks off the wire.
func drain(t *testing.T, d decoder, rest []byte, piece int) []int16 {
	t.Helper()

	var got []int16
	for len(rest) > 0 {
		n := min(piece, len(rest))
		pcm, err := d.decode(rest[:n])
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = append(got, pcm...)
		rest = rest[n:]
	}

	// The parser runs alongside, so the last frames can still be in flight after the final chunk.
	for range 200 {
		pcm, err := d.decode(nil)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(pcm) == 0 {
			break
		}
		got = append(got, pcm...)
	}
	return got
}

// The header and the frames arrive separately — the header once in stream/start, the frames as chunks —
// and only together are they a stream mewkiz/flac will read.
func TestFLACDecodesWhatWasEncoded(t *testing.T) {
	encoded, want := flacStream(t, 3)

	// Anywhere in the metadata will do: the decoder concatenates the header with what follows, so the
	// split does not have to land on the real boundary.
	d, err := newFLACDecoder(encoded[:4])
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	got := drain(t, d, encoded[4:], 512)

	if len(got) != len(want) {
		t.Fatalf("decoded %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d is %d, want %d", i, got[i], want[i])
		}
	}
}

// A frame split across chunks yields nothing until the chunk that completes it, which the session has
// to tell apart from silence rather than placing an empty frame.
func TestFLACSaysNothingUntilAFrameIsWhole(t *testing.T) {
	encoded, want := flacStream(t, 1)

	d, err := newFLACDecoder(encoded[:4])
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	// One byte at a time: almost every call must report no frame at all.
	got := drain(t, d, encoded[4:], 1)

	if len(got) != len(want) {
		t.Fatalf("decoded %d samples, want %d", len(got), len(want))
	}
}

func TestFLACRefusesToStartWithoutItsHeader(t *testing.T) {
	if _, err := newFLACDecoder(nil); err == nil {
		t.Error("started with no header")
	}
}
