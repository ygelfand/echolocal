package sendspin

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/pion/opus"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// decoder turns one chunk into interleaved samples. The returned slice is reused and valid until the
// next call: chunks arrive every 20 ms.
type decoder interface {
	decode(chunk []byte) ([]int16, error)
}

func decoderFor(codec string, sampleRate, channels, bitDepth int) (decoder, error) {
	switch strings.ToLower(codec) {
	case "opus":
		return newOpusDecoder(sampleRate, channels)
	case "pcm":
		return newPCMDecoder(bitDepth, channels)
	}
	return nil, fmt.Errorf("sendspin: no decoder for %q", codec)
}

// Opus frames are at most 120 ms: 5760 samples per channel at 48 kHz.
type opusDecoder struct {
	dec      opus.Decoder
	channels int
	out      []int16
}

func newOpusDecoder(sampleRate, channels int) (decoder, error) {
	dec, err := opus.NewDecoderWithOutput(sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("sendspin: opus decoder: %w", err)
	}
	return &opusDecoder{dec: dec, channels: channels, out: make([]int16, 5760*channels)}, nil
}

func (d *opusDecoder) decode(chunk []byte) ([]int16, error) {
	n, err := d.dec.DecodeToInt16(chunk, d.out)
	if err != nil {
		return nil, fmt.Errorf("sendspin: opus decode: %w", err)
	}
	return d.out[:n*d.channels], nil
}

// pcmDecoder unpacks what is already samples. The wire is little-endian signed, and 24-bit is packed
// three bytes to a sample rather than padded into four.
type pcmDecoder struct {
	bytes int // per sample
	shift uint
	out   []int16
}

func newPCMDecoder(bitDepth, channels int) (decoder, error) {
	switch bitDepth {
	case 16:
		return &pcmDecoder{bytes: 2}, nil
	case 24:
		// Narrowed to what the speaker takes. Nothing is lost that the DAC could have played.
		return &pcmDecoder{bytes: 3, shift: 8}, nil
	case 32:
		return &pcmDecoder{bytes: 4, shift: 16}, nil
	}
	return nil, fmt.Errorf("sendspin: no pcm decoder for %d-bit", bitDepth)
}

func (d *pcmDecoder) decode(chunk []byte) ([]int16, error) {
	if len(chunk)%d.bytes != 0 {
		return nil, fmt.Errorf("sendspin: %d bytes is not whole %d-byte samples", len(chunk), d.bytes)
	}

	n := len(chunk) / d.bytes
	if cap(d.out) < n {
		d.out = make([]int16, n)
	}
	d.out = d.out[:n]

	for i := range n {
		at := i * d.bytes
		switch d.bytes {
		case 2:
			d.out[i] = int16(binary.LittleEndian.Uint16(chunk[at:]))
		case 3:
			// Sign-extend from 24 bits, then narrow.
			v := int32(chunk[at]) | int32(chunk[at+1])<<8 | int32(chunk[at+2])<<16
			if v&0x800000 != 0 {
				v |= ^0xFFFFFF
			}
			d.out[i] = int16(v >> d.shift)
		case 4:
			d.out[i] = int16(int32(binary.LittleEndian.Uint32(chunk[at:])) >> d.shift)
		}
	}
	return d.out, nil
}

// formats is what we can play, in preference order. Opus leads because it is always 48 kHz whatever
// the source was, and the speaker is 48 kHz only.
func formats() []audioFormat {
	return []audioFormat{
		{Codec: "opus", Channels: speaker.Channels, SampleRate: speaker.Rate, BitDepth: speaker.Bits},
		{Codec: "pcm", Channels: speaker.Channels, SampleRate: speaker.Rate, BitDepth: speaker.Bits},
	}
}

// audioFormat mirrors the protocol's, so the list above reads without the import.
type audioFormat struct {
	Codec      string
	Channels   int
	SampleRate int
	BitDepth   int
}
