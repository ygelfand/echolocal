// Package asp applies the speaker tuning the vendor's audio signal processing applies.
//
// A 1024-tap FIR carries the tuning: relative to 500 Hz it lifts 125-400 Hz by up to 27 dB and cuts
// 2-3 kHz by about 11 dB. A four-band compressor and limiter follows, and it is not optional — a
// 27 dB shelf at 160 Hz is only survivable because the band below 115 Hz is crushed 20:1 before it
// reaches the driver.
//
// The coefficients are the vendor's and are not ours to ship, so they are read off the device.
package asp

import (
	"fmt"
	"path/filepath"
)

// VendorDir is where the tuning lives on a device.
const VendorDir = "/vendor/etc/audio-algorithms"

// Rate is the rate the tuning was designed at, which is also the only rate the playback codec takes.
const Rate = 48000

// taps is the length of the tuning filter. A file of any other length is a tuning we do not know.
const taps = 1024

// eqFile is the tuning filter to load. The vendor ships one shape in six files that differ only in
// broadband gain, which is how it gets louder: EQ_100 carries 14.5 dB more than this one. Taking the
// unity bucket leaves loudness to the volume curve we already have.
const eqFile = "EQ_50.cfg"

// mbclFile is the compressor and limiter that sits under the tuning.
const mbclFile = "MBCL.cfg"

// Tuning is a loaded tuning, shared and read-only. Chain turns it into something that can process.
type Tuning struct {
	taps []float32
	mbcl mbcl
}

// Load reads a tuning out of a directory, normally VendorDir.
func Load(dir string) (*Tuning, error) {
	h, err := readFloats(filepath.Join(dir, eqFile), taps)
	if err != nil {
		return nil, err
	}

	m, err := readMBCL(filepath.Join(dir, mbclFile))
	if err != nil {
		return nil, err
	}

	return &Tuning{taps: h, mbcl: m}, nil
}

// Chain is a tuning applied to one stream. It holds the filter history and the compressor's
// envelopes, so it belongs to whoever is playing and is not safe for concurrent use.
type Chain struct {
	fir  *fir
	comp *mbclState
}

// Chain builds the processing state for a stream of the given block size. Every call to Process must
// then be exactly that long: the FIR's transform is sized for it.
func (t *Tuning) Chain(block int) (*Chain, error) {
	if block <= 0 {
		return nil, fmt.Errorf("asp: a block is %d samples", block)
	}
	if t.mbcl.Bypass {
		return nil, fmt.Errorf("asp: %s asks to be bypassed", mbclFile)
	}

	comp, err := newMBCL(t.mbcl, Rate)
	if err != nil {
		return nil, err
	}
	return &Chain{fir: newFIR(t.taps, block), comp: comp}, nil
}

// Process applies the tuning to one block in place. Samples are full scale at ±1, which is what the
// compressor's thresholds are in dB of.
func (c *Chain) Process(x []float32) {
	c.fir.process(x)
	c.comp.process(x)
}

// Reset drops the filter's history and the compressor's envelopes, so the next block is processed as
// though it were the first. A chain that stopped being used has a history of whatever was playing
// then, and starting from silence is better than smearing that across what is playing now.
func (c *Chain) Reset() {
	c.fir.reset()
	c.comp.reset()
}
