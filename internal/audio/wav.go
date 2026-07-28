// Package audio holds format helpers shared by the host and device tools.
package audio

import (
	"encoding/binary"
	"math"
	"os"
)

// WriteWAV writes 16-bit little-endian PCM as a RIFF/WAVE file.
func WriteWAV(path string, pcm []byte, rate, channels int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var h []byte
	u32 := func(v uint32) { h = binary.LittleEndian.AppendUint32(h, v) }
	u16 := func(v uint16) { h = binary.LittleEndian.AppendUint16(h, v) }

	blockAlign := channels * 2

	h = append(h, "RIFF"...)
	u32(uint32(36 + len(pcm)))
	h = append(h, "WAVEfmt "...)
	u32(16)
	u16(1) // PCM
	u16(uint16(channels))
	u32(uint32(rate))
	u32(uint32(rate * blockAlign))
	u16(uint16(blockAlign))
	u16(16)
	h = append(h, "data"...)
	u32(uint32(len(pcm)))

	if _, err := f.Write(h); err != nil {
		return err
	}
	_, err = f.Write(pcm)
	return err
}

// Tone renders a mono sine as 16-bit PCM. amplitude is relative to full scale.
func Tone(freq float64, secs float64, rate int, amplitude float64) []byte {
	n := int(secs * float64(rate))
	out := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		v := amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))
		s := int16(v * math.MaxInt16)
		out = append(out, byte(s), byte(s>>8))
	}
	return out
}
