package asp

import (
	"errors"
	"fmt"
	"path/filepath"
)

const VendorDir = "/vendor/etc/audio-algorithms"

// The rate the tuning was designed at and the only rate the playback codec takes.
const Rate = 48000

const defaultTaps = 1024

type Step struct {
	UpTo float64
	Name string
}

type Files struct {
	EQ      []Step
	Limiter string
	Taps    int
}

func (f Files) Listed() bool { return len(f.EQ) > 0 && f.Limiter != "" }

func (f Files) taps() int {
	if f.Taps == 0 {
		return defaultTaps
	}
	return f.Taps
}

type Tuning struct {
	files Files
	taps  [][]float32
	mbcl  mbcl
}

func Load(dir string, files Files) (*Tuning, error) {
	if !files.Listed() {
		return nil, errors.New("asp: no tuning files are listed for this board")
	}
	if files.Taps < 0 {
		return nil, fmt.Errorf("asp: a filter of %d taps", files.Taps)
	}
	t := &Tuning{files: files}
	for _, s := range files.EQ {
		h, err := readFloats(filepath.Join(dir, s.Name), files.taps())
		if err != nil {
			return nil, err
		}
		t.taps = append(t.taps, h)
	}

	m, err := readMBCL(filepath.Join(dir, files.Limiter))
	if err != nil {
		return nil, err
	}
	t.mbcl = m
	return t, nil
}

func (t *Tuning) step(share float64) int {
	for i, s := range t.files.EQ {
		if share <= s.UpTo {
			return i
		}
	}
	return len(t.files.EQ) - 1
}

type Chain struct {
	tuning *Tuning
	firs   []*fir
	at     int
	fade   []float32
	comp   *mbclState
}

func (t *Tuning) Chain(block int) (*Chain, error) {
	if block <= 0 {
		return nil, fmt.Errorf("asp: a block is %d samples", block)
	}
	if t.mbcl.Bypass {
		return nil, fmt.Errorf("asp: %s asks to be bypassed", t.files.Limiter)
	}

	comp, err := newMBCL(t.files.Limiter, t.mbcl, Rate)
	if err != nil {
		return nil, err
	}
	c := &Chain{tuning: t, fade: make([]float32, block), comp: comp}
	for _, h := range t.taps {
		c.firs = append(c.firs, newFIR(h, block))
	}
	return c, nil
}

func (c *Chain) Process(x []float32, share float64) {
	c.filter(x, share)
	c.comp.process(x)
}

func (c *Chain) filter(x []float32, share float64) {
	next := c.tuning.step(share)
	if next == c.at {
		c.firs[c.at].process(x)
		return
	}

	from, to := c.firs[c.at], c.firs[next]
	copy(to.tail, from.tail)
	copy(c.fade, x)
	from.process(x)
	to.process(c.fade)
	n := float32(len(x))
	for i := range x {
		w := float32(i+1) / n
		x[i] = x[i]*(1-w) + c.fade[i]*w
	}
	c.at = next
}

func (c *Chain) Reset() {
	for _, f := range c.firs {
		f.reset()
	}
	c.comp.reset()
}
