package media

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// flacFixture is a tenth of a second of 440 Hz tone: 16-bit mono at 44.1 kHz, 4096 samples,
// generated with the mewkiz/flac encoder so the decode path walks real frames.
const flacFixture = "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"

func le16(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

func wavData(channels uint16, rate uint32, samples []int16) []byte {
	return wave(fmtChunk(channels, rate, 16), le16(samples))
}

func TestDecodePassesPipelineRateThrough(t *testing.T) {
	samples := []int16{1, -2, 3, -4, 5, -6, 7, -8, 100, -200}
	out, err := decode(wavData(1, 16000, samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(samples) {
		t.Fatalf("came back %d samples, want %d", len(out), len(samples))
	}
	for i := range samples {
		if out[i] != samples[i] {
			t.Errorf("sample %d changed: %d -> %d", i, samples[i], out[i])
		}
	}
}

func TestDecodeBringsRawWAVToThePipelineRate(t *testing.T) {
	// A working DC level is the easiest thing to spot drift in.
	in := make([]int16, 44100)
	for i := range in {
		in[i] = 1000
	}
	out, err := decode(wavData(1, 44100, in))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(out); n < 14000 || n > 19000 {
		t.Errorf("a second of 44.1 kHz came back as %d samples, want ~16000", n)
	}
	for _, s := range out[len(out)/4 : len(out)*3/4] {
		if math.Abs(float64(s)-1000) > 25 {
			t.Errorf("44.1 kHz DC came out as %d, want ~1000", s)
			break
		}
	}
}

func TestDecodeDownmixesStereo(t *testing.T) {
	// Left out of phase with right: mono must be silence, not one of the channels.
	in := make([]int16, 2*1600)
	for i := range in {
		if i%2 == 0 {
			in[i] = 4000
		} else {
			in[i] = -4000
		}
	}
	out, err := decode(wavData(2, 16000, in))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1600 {
		t.Fatalf("came back %d samples, want %d (mono)", len(out), 1600)
	}
	for _, s := range out {
		if s != 0 {
			t.Errorf("out-of-phase stereo survived as %d, want 0", s)
			break
		}
	}
}

func TestDecodeFLAC(t *testing.T) {
	body, err := base64.StdEncoding.DecodeString(flacFixture)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decode(body)
	if err != nil {
		t.Fatal(err)
	}
	// 4096 samples at 44.1 kHz is a tenth of a second: about 1600 at the pipeline rate.
	if n := len(out); n < 1000 || n > 2000 {
		t.Fatalf("FLAC came back %d samples, want ~1600", n)
	}
	var peak int32
	for _, s := range out {
		a := int32(s)
		if a < 0 {
			a = -a
		}
		if a > peak {
			peak = a
		}
	}
	if peak < 3000 {
		t.Errorf("FLAC decoded to a peak of %d, want the tone near its 8000 amplitude", peak)
	}
	if peak > 12000 {
		t.Errorf("FLAC decoded to a peak of %d, above what the tone can reach", peak)
	}
}

func TestDecodeRefusesOtherContainers(t *testing.T) {
	body := []byte("ID3\x04" + strings.Repeat("x", 200))
	_, err := decode(body)
	if err == nil {
		t.Fatal("decoded an ID3 blob")
	}
	msg := err.Error()
	for _, want := range []string{"does not play", "WAVE and FLAC", "204 bytes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q, want it to say %q", msg, want)
		}
	}
}

func TestDecodeRefusesATruncatedWAVE(t *testing.T) {
	if _, err := decode([]byte("RIFF")); err == nil {
		t.Fatal("accepted four bytes")
	}
	// RIFF header but no data chunk: the parser must say so rather than panic.
	_, err := decode([]byte("RIFF\xff\xff\xff\xffWAVE" + string(fmtChunk(1, 16000, 16))))
	if err == nil || !strings.Contains(err.Error(), "no data chunk") {
		t.Fatalf("want a missing-data error, got %v", err)
	}
}

func TestDecodeRefusesACompressedWAVE(t *testing.T) {
	// fmtChunk in media_test.go hard-codes format 1 (PCM); an IEEE float WAVE is the one a decoder
	// must not read as raw samples.
	body := wave(fmtWaveFormat(3, 1, 16000, 16), le16([]int16{1, 2, 3}))
	_, err := decode(body)
	if err == nil || !strings.Contains(err.Error(), "unsupported WAVE format 3") {
		t.Fatalf("want an unsupported-format error, got %v", err)
	}
}

func TestDecodeSurfacesATruncatedFLAC(t *testing.T) {
	body, err := base64.StdEncoding.DecodeString(flacFixture)
	if err != nil {
		t.Fatal(err)
	}
	// Cut in the middle of the first frame: the stream info parses, the frame does not.
	if _, err := decode(body[:200]); err == nil || !strings.Contains(err.Error(), "FLAC stream") {
		t.Fatalf("want a FLAC decode error, got %v", err)
	}
}

func TestDecodeRefusesBrokenFLAC(t *testing.T) {
	_, err := decode(append([]byte("fLaC"), []byte("not a flac")...))
	if err == nil || !strings.Contains(err.Error(), "FLAC stream") {
		t.Fatalf("want a FLAC read error, got %v", err)
	}
}

// fmtWaveFormat builds an fmt chunk with an explicit format code, for WAVes that are not the plain
// PCM the device plays.
func fmtWaveFormat(format uint16, channels uint16, rate uint32, bits uint16) []byte {
	var b bytes.Buffer
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, format)
	_ = binary.Write(&b, binary.LittleEndian, channels)
	_ = binary.Write(&b, binary.LittleEndian, rate)
	_ = binary.Write(&b, binary.LittleEndian, rate*uint32(channels)*uint32(bits)/8)
	_ = binary.Write(&b, binary.LittleEndian, channels*bits/8)
	_ = binary.Write(&b, binary.LittleEndian, bits)
	return b.Bytes()
}
