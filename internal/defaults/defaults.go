package defaults

import (
	"sync"

	"github.com/ygelfand/echolocal/internal/board"
)

type Set struct {
	MicGain int

	Sensitivity int

	RingTrouble string
	RingMuted   string

	MinCores int

	VisualizerLift int
}

var biscuit = Set{
	// The vendor's own gain.
	MicGain: 20,

	Sensitivity: 8,

	RingTrouble: "Alert",

	RingMuted: "",

	MinCores: 2,

	VisualizerLift: 10,
}

func show5() Set {
	s := biscuit
	s.VisualizerLift = 0
	return s
}

var sets = map[string]Set{
	board.Biscuit.Device:  biscuit,
	board.Radar.Device:    biscuit,
	board.Checkers.Device: show5(),
	board.Cronos.Device:   show5(),
	board.Crown.Device:    show5(),
}

func For(b board.Board) Set {
	if s, ok := sets[b.Device]; ok {
		return s
	}
	return biscuit
}

var (
	mu      sync.RWMutex
	current = biscuit
)

func Use(b board.Board) {
	mu.Lock()
	defer mu.Unlock()
	current = For(b)
}

func Current() Set {
	mu.RLock()
	defer mu.RUnlock()
	return current
}
