//go:build board_athena

package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/ygelfand/libcountertop/pkg/audio/alsa"
	"github.com/ygelfand/libcountertop/pkg/audio/resample"
)

const (
	micDevice   = 17
	micChannels = 4
	refDevice   = 9
	refChannels = 2

	micRate   = 48000
	micPeriod = micRate / 100
	refRate   = 96000
	refPeriod = refRate / 100

	// Device 17's DMA block is 48 KB: a 64 KB ring faulted the kernel 48 KB in.
	micPeriods = 6
	refPeriods = 4

	// AFE.cfg MicBoostdB.
	micBoostDB = 5.89
)

type paired struct {
	mics, ref    *alsa.Capture
	micRS, refRS *resample.Rational

	micRaw, refRaw []byte
	micIn, refIn   []int16
	micOut, refOut []int16
}

func openCapture() (stream, error) {
	setupConverter()

	p := &paired{
		micRS:  resample.NewRational(micRate, Rate, micChannels),
		refRS:  resample.NewRational(refRate, Rate, refChannels),
		micRaw: make([]byte, micPeriod*micChannels*4),
		refRaw: make([]byte, refPeriod*refChannels*2),
		micIn:  make([]int16, micPeriod*micChannels),
		refIn:  make([]int16, refPeriod*refChannels),
	}
	p.micRS.Headroom(float32(math.Pow(10, micBoostDB/20)))

	if err := p.start(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *paired) start() error {
	ref, err := alsa.Open(Card, refDevice, alsa.Config{
		Channels:   refChannels,
		Rate:       refRate,
		Format:     alsa.FormatS16LE,
		Bits:       16,
		PeriodSize: refPeriod,
		Periods:    refPeriods,
	})
	if err != nil {
		return fmt.Errorf("reference: %w", err)
	}

	mics, err := alsa.Open(Card, micDevice, alsa.Config{
		Channels:   micChannels,
		Rate:       micRate,
		Format:     alsa.FormatS32LE,
		Bits:       32,
		PeriodSize: micPeriod,
		Periods:    micPeriods,
	})
	if err != nil {
		_ = ref.Close()
		return fmt.Errorf("microphones: %w", err)
	}

	p.ref, p.mics = ref, mics
	return nil
}

func (p *paired) restart() error {
	p.stop()
	p.micRS.Reset()
	p.refRS.Reset()
	p.micOut, p.refOut = p.micOut[:0], p.refOut[:0]
	return p.start()
}

func (p *paired) Read(buf []byte) (int, error) {
	const frameBytes = Channels * Bits / 8

	frames := len(buf) / frameBytes
	for len(p.micOut) < frames*micChannels || len(p.refOut) < frames*refChannels {
		if err := p.fill(); err != nil {
			return 0, err
		}
	}

	for f := range frames {
		out := buf[f*frameBytes:]
		for c := range micChannels {
			putS24(out[3*c:], p.micOut[f*micChannels+c])
		}
		for c := range refChannels {
			putS24(out[3*(Mics+c):], p.refOut[f*refChannels+c])
		}
	}

	p.micOut = append(p.micOut[:0], p.micOut[frames*micChannels:]...)
	p.refOut = append(p.refOut[:0], p.refOut[frames*refChannels:]...)
	return frames * frameBytes, nil
}

func (p *paired) fill() error {
	if err := p.read(p.ref, p.refRaw); err != nil {
		return err
	}
	if err := p.read(p.mics, p.micRaw); err != nil {
		return err
	}

	for i := range p.refIn {
		p.refIn[i] = int16(binary.LittleEndian.Uint16(p.refRaw[2*i:]))
	}
	// Measured on athena: 24-bit samples in the low bits of each 32-bit slot.
	for i := range p.micIn {
		p.micIn[i] = int16(int32(binary.LittleEndian.Uint32(p.micRaw[4*i:])) >> 8)
	}

	p.micOut = append(p.micOut, p.micRS.Run(p.micIn)...)
	p.refOut = append(p.refOut, p.refRS.Run(p.refIn)...)
	return nil
}

func (p *paired) read(pcm *alsa.Capture, buf []byte) error {
	for got := 0; got < len(buf); {
		n, err := pcm.Read(buf[got:])
		if errors.Is(err, alsa.ErrOverrun) {
			if rerr := p.restart(); rerr != nil {
				return fmt.Errorf("mic: restarting the paired capture: %w", rerr)
			}
			return alsa.ErrOverrun
		}
		if err != nil {
			return err
		}
		got += n
	}
	return nil
}

func (p *paired) stop() {
	if p.ref != nil {
		_ = p.ref.Close()
	}
	if p.mics != nil {
		_ = p.mics.Close()
	}
	p.ref, p.mics = nil, nil
}

func (p *paired) Close() error {
	p.stop()
	return nil
}

func putS24(b []byte, v int16) {
	s := int32(v) << 8
	b[0], b[1], b[2] = byte(s), byte(s>>8), byte(s>>16)
}
