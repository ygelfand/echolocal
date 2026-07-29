package mic

import (
	"log/slog"
	"math"
	"sync/atomic"
	"time"
)

// Leveling the mix. Home Assistant used to apply gain to what a device sent and stopped when
// ESPHome moved to assist satellites, so nothing does: speech arrives at recognition 20 dB or more
// below what the models were trained on, and no hardware gain fixes both a talker across the room
// and one leaning over the device.
//
// The shape is the vendor's own full band AGC: a target, a ceiling on how much to add, quick to come
// down and slow to go up, and it only follows audio that stands above the room rather than the room
// itself.
const (
	targetDBFS = -23.0
	maxGainDB  = 27.0

	// How far above the quietest thing recently heard a frame has to be before it is taken for
	// someone talking. Relative, because an absolute threshold cannot be right: the room is unknown
	// and the analog gain in front of this is a setting, so any fixed level is below the noise in one
	// house and above the speech in another. What is fixed is how far speech stands out from a room,
	// which is a property of speech.
	speechOverFloorDB = 12.0

	// The floor drops to the quietest audio quickly, since a pause between words is the best look at
	// the room anyone gets, and climbs slowly enough that talking without pausing cannot convince it
	// the room got louder. Slower still and a room that genuinely turns noisy would never be noticed.
	floorFall = 200 * time.Millisecond
	floorRise = 60 * time.Second

	// How close to full scale a frame is allowed to come. Speech peaks 12 to 18 dB above its own
	// average, so a target set on the average alone asks for gain that clips the peaks: the loudest
	// sample in the frame is what decides how much gain it can actually take.
	peakDBFS = -3.0

	// Where the gain starts, so the first thing said after a boot is not the one utterance that
	// climbs from nothing. The vendor's own leveller carries a fixed 20 dB and adapts around it.
	startGainDB = 20.0

	fallTime = 500 * time.Millisecond
	riseTime = 5 * time.Second
)

type leveler struct {
	gain float32

	// floor is the quietest the room has been heard to be, which is what speech is judged against.
	floor float32

	// How much of the way to the wanted value one frame moves, for the gain and for the floor.
	fall, rise          float32
	floorDown, floorUp  float32
	target, ceiling     float32
	headroom, overFloor float32

	// published is the gain, for anything outside the reader that wants to log it.
	published atomic.Uint32
	clipped    atomic.Uint64
}

// forget throws away what has been learned, so switching leveling off and on again is a way out of
// a room it has adapted badly to.
func (l *leveler) forget() {
	l.gain = float32(math.Pow(10, startGainDB/20))
	l.floor = fullScale
	l.publish()
}

func (l *leveler) publish() { l.published.Store(math.Float32bits(l.gain)) }

func newLeveler() *leveler {
	frame := float64(FrameSamples) / float64(Rate)

	return &leveler{
		gain:      float32(math.Pow(10, startGainDB/20)),
		floor:     fullScale,
		fall:      float32(1 - math.Exp(-frame/fallTime.Seconds())),
		rise:      float32(1 - math.Exp(-frame/riseTime.Seconds())),
		floorDown: float32(1 - math.Exp(-frame/floorFall.Seconds())),
		floorUp:   float32(1 - math.Exp(-frame/floorRise.Seconds())),
		target:    float32(math.Pow(10, targetDBFS/20) * fullScale),
		ceiling:   float32(math.Pow(10, maxGainDB/20)),
		headroom:  float32(math.Pow(10, peakDBFS/20) * fullScale),
		overFloor: float32(math.Pow(10, speechOverFloorDB/20)),
	}
}

const fullScale = 32768

func abs(s int16) int32 {
	if s < 0 {
		return -int32(s)
	}
	return int32(s)
}

// apply scales a frame in place, following the level it has been carrying rather than this frame
// alone: gain that jumped per frame would pump audibly and change what a wake model hears mid-word.
func (l *leveler) apply(frame []int16) {
	if len(frame) == 0 {
		return
	}

	var sum float64
	var peak float32
	for _, s := range frame {
		sum += float64(s) * float64(s)
		peak = max(peak, float32(abs(s)))
	}
	rms := float32(math.Sqrt(sum / float64(len(frame))))

	// Follow the room, quickly down and slowly up, never below one sample of quantisation.
	follow := l.floorUp
	if rms < l.floor {
		follow = l.floorDown
	}
	l.floor = max(l.floor+(rms-l.floor)*follow, 1)

	// Silence says nothing about how loud speech is, so the gain is left where speech left it.
	if rms > l.floor*l.overFloor {
		want := min(max(l.target/rms, 1), l.ceiling)

		step := l.rise
		if want < l.gain {
			step = l.fall
		}
		l.gain += (want - l.gain) * step
	}

	// Whatever the average asked for, this frame does not get gain that would push its loudest sample
	// into the ceiling. Smoothing cannot do this part: an onset arrives inside one frame.
	if peak > 0 {
		l.gain = min(l.gain, l.headroom/peak)
	}

	l.publish()

	for i, s := range frame {
		switch v := float32(s) * l.gain; {
		case v > fullScale-1:
			frame[i] = fullScale - 1
			l.clipped.Add(1)
		case v < -fullScale:
			frame[i] = -fullScale
			l.clipped.Add(1)
		default:
			frame[i] = int16(v)
		}
	}
}

// SetLeveling turns leveling on or off. It takes effect on the next frame.
func (s *Source) SetLeveling(on bool) {
	s.leveling.Store(on)
	slog.Info("microphone leveling", "on", on)
}

// Gain is what the leveler is currently applying, in dB.
func (s *Source) Gain() float64 {
	return 20 * math.Log10(float64(math.Float32frombits(s.leveler.published.Load())))
}

// Clipped is how many samples the leveler has had to hold at full scale.
func (s *Source) Clipped() uint64 { return s.leveler.clipped.Load() }
