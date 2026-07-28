package audio

import (
	"fmt"
	"io"
	"math"
)

// FullScale is the peak magnitude of a 24-bit sample.
const FullScale = 1 << 23

// DecodeS24LE3 sign-extends one packed 24-bit little-endian sample.
func DecodeS24LE3(b []byte) int32 {
	v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
	if v&0x800000 != 0 {
		v |= ^0xFFFFFF
	}
	return v
}

// Stats accumulates per-channel level measurements over a capture.
//
// TruncErr tracks what a 24→16-bit truncation would discard, which is the only way to tell
// whether the low bits carry signal or only converter noise.
type Stats struct {
	N        int64
	Peak     int32
	sumSq    float64
	sumSqErr float64
	nonzero  int64
}

func (s *Stats) Add(v int32) {
	s.N++
	if a := v; a < 0 {
		if -a > s.Peak {
			s.Peak = -a
		}
	} else if a > s.Peak {
		s.Peak = a
	}
	s.sumSq += float64(v) * float64(v)

	resid := v - (v>>8)<<8
	s.sumSqErr += float64(resid) * float64(resid)
	if resid != 0 {
		s.nonzero++
	}
}

func (s *Stats) RMS() float64 {
	if s.N == 0 {
		return 0
	}
	return math.Sqrt(s.sumSq / float64(s.N))
}

func (s *Stats) TruncErrRMS() float64 {
	if s.N == 0 {
		return 0
	}
	return math.Sqrt(s.sumSqErr / float64(s.N))
}

// LowBitsUsed is the fraction of samples with any of the low 8 bits set. Acoustic capture
// sits near 100%; a digital loopback shifted into the top 16 bits reads exactly 0.
func (s *Stats) LowBitsUsed() float64 {
	if s.N == 0 {
		return 0
	}
	return float64(s.nonzero) / float64(s.N)
}

// DBFS converts a linear magnitude to dB relative to 24-bit full scale.
func DBFS(v float64) float64 {
	if v <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(v/FullScale)
}

// WriteReport prints a per-channel table.
func WriteReport(w io.Writer, st []Stats) {
	fmt.Fprintf(w, "\n%3s  %10s  %10s  %12s  %10s  %s\n",
		"ch", "peak dBFS", "rms dBFS", "trunc-err dB", "trunc SNR", "low-8-bits")
	for c := range st {
		s := &st[c]
		if s.N == 0 {
			continue
		}
		rms, errRMS := s.RMS(), s.TruncErrRMS()

		snr := math.Inf(1)
		if errRMS > 0 {
			snr = 20 * math.Log10(rms/errRMS)
		}

		note := ""
		switch {
		case s.Peak == 0:
			note = "  <- silent"
		case s.LowBitsUsed() == 0:
			note = "  <- digital loopback, not a mic"
		}

		fmt.Fprintf(w, "%3d  %10.1f  %10.1f  %12.1f  %8.1f dB  %9.1f%%%s\n",
			c, DBFS(float64(s.Peak)), DBFS(rms), DBFS(errRMS), snr,
			100*s.LowBitsUsed(), note)
	}
}
