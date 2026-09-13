package sendspin

import (
	"bytes"
	"encoding/binary"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

const (
	chunkBlock   = 960 // the reference server's block size: 20 ms at 48 kHz, one frame to a chunk
	streamBlocks = 100
)

// encodedStream mirrors sendspin-go's encode.FLACEncoder, which cannot be imported: the Opus encoder
// beside it binds libopus through cgo.
func encodedStream(t *testing.T, block, blocks int) (header []byte, chunks [][]byte, want []int16) {
	t.Helper()

	buf := &bytes.Buffer{}
	enc, err := flac.NewEncoder(buf, &meta.StreamInfo{
		BlockSizeMin:  uint16(block),
		BlockSizeMax:  uint16(block),
		SampleRate:    speaker.Rate,
		NChannels:     speaker.Channels,
		BitsPerSample: speaker.Bits,
	})
	if err != nil {
		t.Fatalf("flac.NewEncoder: %v", err)
	}
	defer enc.Close()
	enc.EnablePredictionAnalysis(true)

	header = append(header, buf.Bytes()...)
	buf.Reset()

	for b := range blocks {
		subs := make([]*frame.Subframe, speaker.Channels)
		for c := range subs {
			subs[c] = &frame.Subframe{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   make([]int32, block),
				NSamples:  block,
			}
		}
		for i := range block {
			for c := range speaker.Channels {
				// No two blocks share audio, so a frame can be traced to the block it came from.
				v := int32(((b*block+i)*37)%3001 - 1500 + c*7)
				subs[c].Samples[i] = v
				want = append(want, int16(v))
			}
		}

		if err := enc.WriteFrame(&frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(block),
				SampleRate:        speaker.Rate,
				Channels:          frame.ChannelsLR,
				BitsPerSample:     speaker.Bits,
				Num:               uint64(b),
			},
			Subframes: subs,
		}); err != nil {
			t.Fatalf("encoding block %d: %v", b, err)
		}

		chunks = append(chunks, bytes.Clone(buf.Bytes()))
		buf.Reset()
	}
	return header, chunks, want
}

func blockAt(t *testing.T, want []int16, block int) map[[2]int16]int {
	t.Helper()

	at := make(map[[2]int16]int, streamBlocks)
	for b := range streamBlocks {
		i := b * block * speaker.Channels
		key := [2]int16{want[i], want[i+1]}
		if _, seen := at[key]; seen {
			t.Fatalf("two blocks start with %v, so a frame cannot be traced to one", key)
		}
		at[key] = b
	}
	return at
}

// lagged runs the session's loop and reports how many chunks behind its own each frame came back.
func lagged(t *testing.T, block int) (lags map[int]int, empty int) {
	t.Helper()

	header, chunks, want := encodedStream(t, block, streamBlocks)
	from := blockAt(t, want, block)

	d, err := newFLACDecoder(header)
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	lags = map[int]int{}
	for i, chunk := range chunks {
		pcm, err := d.decode(chunk)
		if err != nil {
			t.Fatalf("decoding chunk %d: %v", i, err)
		}
		if len(pcm) == 0 {
			empty++
			continue
		}

		b, ok := from[[2]int16{pcm[0], pcm[1]}]
		if !ok {
			t.Fatalf("chunk %d returned a frame from no block", i)
		}
		lags[i-b]++
	}
	return lags, empty
}

// A frame is written at the timestamp of the chunk that returned it, so a lag that varies puts one
// frame where the last one's audio belongs: a hole, or an overwrite.
func TestFLACLagDoesNotVary(t *testing.T) {
	for _, block := range []int{480, 960, 1152, 2048, 4096} {
		lags, empty := lagged(t, block)
		t.Logf("block %d: %d decoded to nothing, lag %v", block, empty, lags)

		if len(lags) != 1 {
			t.Errorf("block %d: lag varies: %v, each step worth %d ms", block, lags,
				block*1000/speaker.Rate)
		}
	}
}

// decode waits for the parser to finish with the chunk, so a chunk hands back its own frame and the
// audio is placed at that chunk's timestamp. Nothing is held back at either end of the stream.
func TestFLACPlaysTheFrameTheChunkCarried(t *testing.T) {
	header, chunks, want := encodedStream(t, chunkBlock, streamBlocks)

	d, err := newFLACDecoder(header)
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	o := anchored(t)
	for i, chunk := range chunks {
		pcm, err := d.decode(chunk)
		if err != nil {
			t.Fatalf("decoding chunk %d: %v", i, err)
		}
		if len(pcm) > 0 {
			o.write(microsFor(int64(i*chunkBlock)), pcm)
		}
	}

	// The first chunk's frame came back on that chunk, so audio starts at the anchor.
	if o.base != 1000 {
		t.Fatalf("audio starts at frame %d, want %d", o.base, 1000)
	}

	played := streamBlocks * chunkBlock
	got := rendered(o, 1000, played)

	if at := firstWrong(got, want[:played*speaker.Channels]); at >= 0 {
		t.Errorf("output frame %d is %d, want %d", at/speaker.Channels, got[at], want[at])
	}
}

// A track's worth of chunks. The write into the decoder's pipe is on the session goroutine, the one
// that also reads the socket, so a parse that never returns takes the room with it.
func TestFLACSurvivesALongStream(t *testing.T) {
	const total = 20000 // ~6.5 minutes at 20 ms a chunk

	// FLAC frames are self-contained, so replaying them is a longer stream to the parser and spares
	// the test encoding six minutes of audio.
	header, chunks, _ := encodedStream(t, chunkBlock, streamBlocks)

	d, err := newFLACDecoder(header)
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	var fed, back atomic.Int64
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := range total {
			pcm, err := d.decode(chunks[i%len(chunks)])
			fed.Add(1)
			if err != nil {
				t.Errorf("decoding chunk %d: %v", i, err)
				return
			}
			if len(pcm) > 0 {
				back.Add(1)
			}
		}
	}()

	// Stalled, not slow: the decoder is only failed when it stops taking chunks altogether.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	last, still := int64(-1), 0
	for {
		select {
		case <-done:
			if held := fed.Load() - back.Load(); held > 1 {
				t.Errorf("%d frames held after %d chunks, want the steady one", held, total)
			}
			return

		case <-tick.C:
			at := fed.Load()
			if at != last {
				last, still = at, 0
				continue
			}
			if still++; still < 15 {
				continue
			}

			stacks := make([]byte, 1<<16)
			stacks = stacks[:runtime.Stack(stacks, true)]
			t.Fatalf("the decoder took no chunk for %ds, stopped at %d of %d holding %d frames\n\n%s",
				still, at, total, at-back.Load(), stacks)
		}
	}
}

// PCM decodes on the call that carried it, so it cannot be placed at another chunk's timestamp.
func TestPCMRendersWhatWasEncoded(t *testing.T) {
	_, _, want := encodedStream(t, chunkBlock, streamBlocks)

	d, err := newPCMDecoder(speaker.Bits, speaker.Channels)
	if err != nil {
		t.Fatalf("newPCMDecoder: %v", err)
	}

	o := anchored(t)
	for b := range streamBlocks {
		chunk := make([]byte, chunkBlock*speaker.Channels*2)
		for i := range chunkBlock * speaker.Channels {
			binary.LittleEndian.PutUint16(chunk[i*2:], uint16(want[b*chunkBlock*speaker.Channels+i]))
		}

		pcm, err := d.decode(chunk)
		if err != nil {
			t.Fatalf("decoding chunk %d: %v", b, err)
		}
		o.write(microsFor(int64(b*chunkBlock)), pcm)
	}

	got := rendered(o, 1000, streamBlocks*chunkBlock)
	if at := firstWrong(got, want); at >= 0 {
		t.Errorf("output frame %d is %d, want %d", at/speaker.Channels, got[at], want[at])
	}
}

func firstWrong(got, want []int16) int {
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			return i
		}
	}
	return -1
}
