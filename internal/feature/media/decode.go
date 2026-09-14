package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/mewkiz/flac"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// decode turns a fetched announcement body into the samples the voice path plays. Home Assistant
// converts ordinary media to WAVE with ffmpeg before serving it, so most of what lands here is
// WAVE at the pipeline's rate; but the announce TTS stream is handed over in whatever container the
// engine produces, so this reads the ones the device can actually play rather than trust that
// conversion. Everything comes out 16 kHz mono, which is what PlayVoice takes.
func decode(body []byte) ([]int16, error) {
	switch {
	case len(body) >= 12 && string(body[0:4]) == "RIFF" && string(body[8:12]) == "WAVE":
		return wavPCM(body)
	case len(body) >= 4 && string(body[0:4]) == "fLaC":
		return flacPCM(body)
	default:
		return nil, fmt.Errorf(
			"announcement is %s, which the device does not play (it decodes WAVE and FLAC); %d bytes",
			container(body), len(body),
		)
	}
}

// container names whatever the body starts with, so the error says something about the file rather
// than about the parser.
func container(body []byte) string {
	head := body
	if len(head) > 12 {
		head = head[:12]
	}
	return fmt.Sprintf("%q", string(head))
}

// wavPCM lifts 16-bit samples out of a RIFF/WAVE body, walking the chunks rather than assuming a
// 44-byte header, and brings them down to the pipeline's 16 kHz mono. Announcements converted by
// Home Assistant already are that; a raw WAVE of any rate or channel count works too.
func wavPCM(body []byte) ([]int16, error) {
	channels := uint16(1)
	rate := uint32(speaker.VoiceRate)
	format := uint16(1) // WAVE_FORMAT_PCM; 0xFFFE is the PCM extensible subtype

	var data []byte
	total := uint64(len(body))
	for off := uint64(12); off+8 <= total; {
		id := string(body[off : off+4])
		size := uint64(binary.LittleEndian.Uint32(body[off+4 : off+8]))
		off += 8

		end := off + size
		if end > total {
			end = total
		}

		switch id {
		case "fmt ":
			// A fmt chunk that peters out before its 16 bytes is a truncated download, not audio
			// metadata; the samples themselves would be short of honest too.
			if size >= 16 && off+16 <= total {
				format = binary.LittleEndian.Uint16(body[off : off+2])
				channels = binary.LittleEndian.Uint16(body[off+2 : off+4])
				rate = binary.LittleEndian.Uint32(body[off+4 : off+8])
			}
		case "data":
			data = body[off:end]
		}

		off = end
		if size%2 == 1 {
			off++
		}
	}

	if data == nil {
		return nil, fmt.Errorf("no data chunk in %d bytes", total)
	}
	if format != 1 && format != 0xFFFE {
		return nil, fmt.Errorf("unsupported WAVE format %d (the device plays uncompressed PCM)", format)
	}
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return toVoice(samples, channels, int(rate)), nil
}

// flacPCM decodes a FLAC body, which some engines hand back rather than the WAVE one, and brings it
// to the pipeline's 16 kHz mono.
func flacPCM(body []byte) ([]int16, error) {
	stream, err := flac.New(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("reading the FLAC stream: %w", err)
	}

	rate := int(stream.Info.SampleRate)
	channels := int(stream.Info.NChannels)
	shift := int(stream.Info.BitsPerSample) - speaker.Bits

	var out []int16
	for {
		frame, err := stream.ParseNext()
		if err != nil {
			if err == io.EOF {
				// The last frame read cleanly to the end of the file.
				break
			}
			return nil, fmt.Errorf("decoding the FLAC stream: %w", err)
		}
		// The header's block size is the authoritative count, but a corrupt frame may come back
		// short of it; sample what is actually there rather than trusting the header.
		n := min(int(frame.BlockSize), len(frame.Subframes[0].Samples))
		for i := range n {
			var s int64
			for c := range channels {
				s += int64(frame.Subframes[c].Samples[i])
			}
			s /= int64(channels)
			if shift > 0 {
				s >>= shift
			} else if shift < 0 {
				s <<= -shift
			}
			if s > math.MaxInt16 {
				s = math.MaxInt16
			} else if s < math.MinInt16 {
				s = math.MinInt16
			}
			out = append(out, int16(s))
		}
	}
	return speaker.ToVoiceRate(out, rate), nil
}

// toVoice downmixes interleaved PCM to mono and brings it to the pipeline's rate.
func toVoice(interleaved []int16, channels uint16, rate int) []int16 {
	var mono []int16
	if channels <= 1 {
		mono = interleaved
	} else if n := len(interleaved) / int(channels); n > 0 {
		mono = make([]int16, n)
		for i := range mono {
			var s int32
			for c := range int(channels) {
				s += int32(interleaved[i*int(channels)+c])
			}
			mono[i] = int16(s / int32(channels))
		}
	}
	return speaker.ToVoiceRate(mono, rate)
}
