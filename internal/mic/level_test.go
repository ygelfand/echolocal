package mic

import (
	"math"
	"testing"
)

// speech is a frame at a given level, as a tone: the leveler only looks at how loud a frame is.
func speech(dbfs float64) []int16 {
	frame := make([]int16, FrameSamples)
	amp := math.Pow(10, dbfs/20) * fullScale * math.Sqrt2

	for i := range frame {
		frame[i] = int16(amp * math.Sin(2*math.Pi*300*float64(i)/Rate))
	}
	return frame
}

func levelOf(frame []int16) float64 {
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(len(frame)))/fullScale)
}

// talk feeds someone speaking in a room: sentences with pauses between them, which is what keeps the
// floor on the room rather than on the voice. Returns the last spoken frame after leveling.
func talk(l *leveler, room, voice float64, seconds float64) []int16 {
	var last []int16

	for range int(seconds * Rate / FrameSamples / 20) {
		for range 15 {
			last = speech(voice)
			l.apply(last)
		}
		for range 5 {
			l.apply(speech(room))
		}
	}
	return last
}

// Quiet speech has to come up to the target, and then stay there.
func TestLevelerReachesTheTarget(t *testing.T) {
	l := newLeveler()

	got := levelOf(talk(l, -65, -45, 30))
	if math.Abs(got-targetDBFS) > 1.5 {
		t.Errorf("settled at %.1f dBFS, want %.1f", got, targetDBFS)
	}
}

// The ceiling has to hold, or a distant talker in a quiet room turns the room up instead.
func TestLevelerStopsAtTheCeiling(t *testing.T) {
	l := newLeveler()

	for range 500 {
		l.apply(speech(-70))
	}

	if gain := 20 * math.Log10(float64(l.gain)); gain > maxGainDB+0.5 {
		t.Errorf("gain reached %.1f dB, ceiling is %.1f", gain, maxGainDB)
	}
}

// Loud speech is left alone rather than turned down: the level is already where models want it, and
// anything hotter is the talker's business.
func TestLevelerLeavesLoudSpeechAlone(t *testing.T) {
	l := newLeveler()

	want := levelOf(speech(-12))
	got := levelOf(talk(l, -60, -12, 30))

	if math.Abs(got-want) > 1 {
		t.Errorf("level moved from %.1f to %.1f dBFS", want, got)
	}
}

// Silence must not wind the gain up, or the room comes up to speaking level between sentences.
func TestLevelerIgnoresSilence(t *testing.T) {
	l := newLeveler()

	for range 200 {
		l.apply(speech(-45))
	}
	spoken := l.gain

	for range 500 {
		l.apply(make([]int16, FrameSamples))
	}

	if l.gain != spoken {
		t.Errorf("gain moved from %.2f to %.2f over silence", spoken, l.gain)
	}
}

// peaky is speech-shaped in the way that matters here: its loudest sample sits well above its
// average, so gain chosen from the average alone drives it into the ceiling.
func peaky(rmsDBFS, crestDB float64) []int16 {
	frame := speech(rmsDBFS)
	frame[len(frame)/2] = int16(min(math.Pow(10, (rmsDBFS+crestDB)/20)*fullScale, fullScale-1))
	return frame
}

func peakOf(frame []int16) float64 {
	var peak int32
	for _, s := range frame {
		peak = max(peak, int32(abs(s)))
	}
	return 20 * math.Log10(float64(peak)/fullScale)
}

func TestLevelerDoesNotClipPeakySpeech(t *testing.T) {
	l := newLeveler()

	var worst float64 = -100
	for range 300 {
		frame := peaky(-35, 16)
		l.apply(frame)
		worst = max(worst, peakOf(frame))
	}

	if n := l.clipped.Load(); n != 0 {
		t.Errorf("clipped %d samples", n)
	}
	if worst > peakDBFS+0.5 {
		t.Errorf("peaked at %.1f dBFS, ceiling is %.1f", worst, peakDBFS)
	}
}

// Steady room noise must not be mistaken for speech, whatever level the room happens to sit at: gain
// that follows the room presents the room at speaking level, and the wake models then hear it.
func TestLevelerDoesNotFollowTheRoom(t *testing.T) {
	for _, room := range []float64{-70, -55, -40} {
		l := newLeveler()
		start := l.gain

		for range 1000 {
			l.apply(speech(room))
		}

		if l.gain != start {
			t.Errorf("a room at %.0f dBFS moved the gain from %.2f to %.2f", room, start, l.gain)
		}
	}
}

// Speech standing above that same room does move it, again at any level.
func TestLevelerFollowsWhatStandsAboveTheRoom(t *testing.T) {
	for _, room := range []float64{-70, -55, -40} {
		l := newLeveler()

		for range 200 {
			l.apply(speech(room))
		}
		quiet := l.gain

		for range 200 {
			l.apply(speech(room + speechOverFloorDB + 6))
		}

		if l.gain == quiet {
			t.Errorf("speech %.0f dB above a room at %.0f dBFS did not move the gain",
				speechOverFloorDB+6, room)
		}
	}
}

// Switching leveling off has to clear what it learned, so it is a way out of a bad adaptation.
func TestForgetClearsTheLearning(t *testing.T) {
	l := newLeveler()
	fresh := l.gain

	talk(l, -65, -45, 10)
	if l.gain == fresh {
		t.Fatal("gain never moved, so there is nothing to forget")
	}

	l.forget()
	if l.gain != fresh {
		t.Errorf("gain is %.2f after forgetting, want %.2f", l.gain, fresh)
	}
	if l.floor != fullScale {
		t.Errorf("floor is %.0f after forgetting, want it reset", l.floor)
	}
}

// Coming down from a loud frame has to be quick, and going up slow: the other way round pumps.
func TestLevelerFallsFasterThanItRises(t *testing.T) {
	l := newLeveler()
	if l.fall <= l.rise {
		t.Errorf("fall %.4f is not quicker than rise %.4f", l.fall, l.rise)
	}
}
