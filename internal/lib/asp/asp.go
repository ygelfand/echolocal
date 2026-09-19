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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// VendorDir is where the tuning lives on a device.
const VendorDir = "/vendor/etc/audio-algorithms"

// Rate is the rate the tuning was designed at, which is also the only rate the playback codec takes.
const Rate = 48000

// knownTaps is the set of tap counts the vendor has shipped. The first EQ file on disk decides which
// one a particular device uses; biscuit uses 1024, radar uses 2048. A file of any other length is a
// tuning we do not know how to process.
var knownTaps = knownTapsList{1024, 2048}

// eqFile parses an EQ_*.cfg filename back into its volume boundary. The vendor's naming scheme is
// consistent across devices: EQ_50 is the bucket that holds for up to 50% volume, EQ_100 for
// everything above 90%. A bucket that is not on disk falls through to the next higher one.
type eqBucket struct {
	upTo float64
	name string
}

// parseEQ pulls the boundary out of an EQ_*.cfg filename. Returns -1 if the name does not match
// the vendor's pattern.
func parseEQ(name string) (float64, bool) {
	if !strings.HasPrefix(name, "EQ_") || !strings.HasSuffix(name, ".cfg") {
		return 0, false
	}
	v, err := strconv.ParseFloat(name[3:len(name)-4], 64)
	if err != nil {
		return 0, false
	}
	return v / 100, true
}

// discoverEQ lists the EQ buckets a device ships, in ascending volume order. The vendor picks
// which ones to include per device: biscuit ships 50..100 in 10% steps; radar ships 30, 40, 50,
// 60, 70, 80 and 100, skipping 90 because its 80→100 transition is one bucket.
func discoverEQ(dir string) ([]eqBucket, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []eqBucket
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if v, ok := parseEQ(e.Name()); ok {
			out = append(out, eqBucket{upTo: v, name: e.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].upTo < out[j].upTo })
	return out, nil
}

// mbclFile is the compressor and limiter that sits under the tuning.
const mbclFile = "MBCL.cfg"

// Tuning is a loaded tuning, shared and read-only. Chains turns it into the per-bucket chains the
// playback loop picks between by current volume.
type Tuning struct {
	buckets []eqBucket
	taps    [][]float32 // one set of taps per bucket, parallel to buckets
	mbcl    mbcl
}

// Load reads a tuning out of a directory, normally VendorDir. The first EQ file's tap count sets
// the length the rest are expected to match: biscuit ships 1024, radar ships 2048. A file whose
// length matches no knownTaps entry is a tuning we do not know how to process.
//
// Each bucket carries its own FIR — on radar the vendor tunes the filter shape per volume band, not
// just the gain, so reusing one filter for every bucket distorts the EQ at any volume above the
// lowest one. The buckets on disk decide which boundaries the tuning exposes: biscuit has 50..100
// in 10% steps, radar has 30, 40, 50, 60, 70, 80 and 100.
func Load(dir string) (*Tuning, error) {
	buckets, err := discoverEQ(dir)
	if err != nil {
		return nil, err
	}
	if len(buckets) == 0 {
		return nil, fmt.Errorf("%s: no EQ_*.cfg found", dir)
	}

	t := &Tuning{buckets: buckets}
	for i, e := range buckets {
		var want int
		if i == 0 {
			want = 0
		} else {
			want = len(t.taps[0])
		}
		h, err := readFloats(filepath.Join(dir, e.name), want)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			if !knownTaps.has(len(h)) {
				return nil, fmt.Errorf("%s: %d coefficients, expected one of %v", e.name, len(h), knownTaps)
			}
		}
		t.taps = append(t.taps, h)
	}

	m, err := readMBCL(filepath.Join(dir, mbclFile))
	if err != nil {
		return nil, err
	}
	t.mbcl = m
	return t, nil
}

// BucketFor picks the EQ chain that matches a fraction of full volume (0..1). The boundary table
// is the same one Load read from disk, so a chain's index is its position in the slice Chains
// returns. A volume above the highest bucket uses the last one; below the lowest uses the first.
func (t *Tuning) BucketFor(volume float64) int {
	for i, e := range t.buckets {
		if volume <= e.upTo {
			return i
		}
	}
	return len(t.buckets) - 1
}

// Chain is a tuning applied to one stream. It holds the filter history and the compressor's
// envelopes, so it belongs to whoever is playing and is not safe for concurrent use.
type Chain struct {
	fir  *fir
	comp *mbclState
}

// Chains builds one processing chain per EQ bucket, each sized for blocks of block samples. The
// playback loop picks the right one for the current volume; the buckets share the compressor so
// calling Process on the active chain keeps the rest's history warm only as far as the underlying
// FIR keeps it, which is exactly one block each.
func (t *Tuning) Chains(block int) ([]*Chain, error) {
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
	n := len(t.taps)
	if n == 0 {
		return nil, fmt.Errorf("asp: no EQ buckets were loaded")
	}
	out := make([]*Chain, n)
	for i := 0; i < n; i++ {
		out[i] = &Chain{fir: newFIR(t.taps[i], block), comp: comp.clone()}
	}
	return out, nil
}

// knownTapsSet is the membership test the parser uses, here to keep the import set short.
type knownTapsList []int

func (k knownTapsList) has(n int) bool {
	for _, x := range k {
		if x == n {
			return true
		}
	}
	return false
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
