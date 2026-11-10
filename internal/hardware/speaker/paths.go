package speaker

import (
	"math"
	"os"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/config"
)

type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
)

// The mixer sequences below come direct from the device.
type kctl struct {
	name  string
	value string
	level int32
	blob  []byte
}

type sequences struct {
	init    []kctl
	on, off []kctl
	path    map[Output][]kctl
	leave   map[Output][]kctl
	close   []kctl
	gain    string

	// idleOff turns the amplifier off through silence, which it otherwise fills with hiss. radar only.
	idleOff bool
}

// Long enough to ride out the gaps inside a stream, and past the ALSA buffer and the tuning's tail,
// so what was last written has played before the amplifier goes off.
const idleAfter = 250 * time.Millisecond

type ampChange int

const (
	ampKeep ampChange = iota
	ampOn
	ampOff
)

// ampIdle decides when the write loop switches the amplifier for silence. Only that loop uses it.
type ampIdle struct {
	since time.Time // when the silence began; zero while something plays
}

// Turning on in the period the audio is written is early enough: the buffer still holds the
// zeros written ahead of it.
func (g *ampIdle) next(playing, on bool, now time.Time) ampChange {
	if playing {
		g.since = time.Time{}
		if !on {
			return ampOn
		}
		return ampKeep
	}
	if g.since.IsZero() {
		g.since = now
	}
	if on && now.Sub(g.since) >= idleAfter {
		return ampOff
	}
	return ampKeep
}

// The kernel's headphone jack switch: 1 while something is plugged in.
const jackState = "/sys/class/switch/h2w/state"

const jackPoll = 500 * time.Millisecond

func pluggedIn() bool {
	b, err := os.ReadFile(jackState)
	return err == nil && strings.TrimSpace(string(b)) != "0"
}

type route struct {
	out     Output
	speaker bool
}

func routeFor(plugged bool, speaker, headphone config.OutputMode) route {
	r := route{out: OutputSpeaker, speaker: speaker.Plays(!plugged)}
	if headphone.Plays(plugged) {
		r.out = OutputHeadphone
	}
	return r
}

const VolumeSteps = config.VolumeSteps

// The vendor's volume curves, direct from the device: index is the volume step, value is attenuation in dB.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -33, -30, -26, -25, -23, -21, -19, -17, -16,
		-14, -13, -12, -10, -9, -8, -7, -5, -5, -4,
		-4, -4, -4, -3, -3, -3, -3, -3, -2, -1, 0,
	},
	OutputHeadphone: {
		-100, -39, -38, -36, -34, -33, -31, -29, -27, -26,
		-25, -24, -23, -21, -20, -19, -18, -17, -15, -14,
		-13, -12, -11, -9, -8, -7, -6, -4, -3, -2, 0,
	},
}

const mute = -90

func gainFor(out Output, volume float64) float32 {
	curve, ok := volumeCurves[out]
	if !ok {
		curve = volumeCurves[OutputSpeaker]
	}
	if volume <= 0 {
		return 0
	}
	at := min(volume, 1) * VolumeSteps
	lo := min(int(at), VolumeSteps-1)
	db := curve[lo] + (curve[lo+1]-curve[lo])*(at-float64(lo))
	if db <= mute {
		return 0
	}
	return float32(math.Pow(10, db/20))
}
