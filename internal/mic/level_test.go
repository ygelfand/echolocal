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

// The published level is about the room, not about the gain in front of it: the same voice over the
// same room noise has to read the same whether the room is quiet or loud, or a ring set to react to
// it would be pinned at nothing in one house and at everything in another.
func TestLevelIsRelativeToTheRoom(t *testing.T) {
	quiet, loud := newLeveler(), newLeveler()

	// Both rooms hear a voice 18 dB above their own noise.
	settle(quiet, -60, 200)
	settle(loud, -35, 200)

	quietVoice := level(quiet, -42, 40)
	loudVoice := level(loud, -17, 40)

	if math.Abs(quietVoice-loudVoice) > 0.1 {
		t.Errorf("the same voice reads %.2f over a quiet room and %.2f over a loud one", quietVoice, loudVoice)
	}

	// 18 dB over the room is a quiet voice, not a loud one: captures of someone talking in a room put
	// speech at 23 dB over the floor at the 90th percentile of frames and 30 dB at its loudest, in the
	// band the level measures. So this has to show, without being anywhere near the top.
	if quietVoice < 0.2 || quietVoice > 0.6 {
		t.Errorf("a voice 18 dB over the room reads %.2f, want it visible and short of the top", quietVoice)
	}

	// And ordinary speech reaches most of the way up, or nothing ever fills the ring.
	if talking := level(newRoom(t), -32, 40); talking < 0.6 {
		t.Errorf("speech 25 dB over the room reads %.2f, want most of the way up", talking)
	}
}

// newRoom is a leveler with a settled quiet room behind it, at 25 dB below the speech the caller is
// about to feed it.
func newRoom(t *testing.T) *leveler {
	t.Helper()

	l := newLeveler()
	settle(l, -57, 200)
	return l
}

// Silence has to read zero, and it has to come back down on its own: a level that stuck where the
// last word left it would leave the ring lit at whatever was last said.
func TestLevelRestsAtZeroAndFallsBack(t *testing.T) {
	l := newLeveler()
	settle(l, -55, 200)

	if quiet := level(l, -55, 40); quiet > 0.05 {
		t.Errorf("a quiet room reads %.2f, want nothing", quiet)
	}

	spoke := level(l, -30, 20)
	if spoke < 0.5 {
		t.Fatalf("speech reads %.2f, want most of the way up", spoke)
	}

	// Half a second of quiet has it most of the way down but not out — the release is deliberately
	// slow enough to see, or the ring would snap dark between words.
	if after := level(l, -55, 25); after > 0.3 {
		t.Errorf("half a second after speech the level is %.2f, want it mostly fallen", after)
	}

	// A second and a half is silence as far as anything watching is concerned.
	if after := level(l, -55, 50); after > 0.05 {
		t.Errorf("a second and a half after speech the level is %.2f, want nothing", after)
	}
}

// The level rises faster than it falls. A meter that followed the decay of every syllable would
// flicker, and one slow to rise would miss the start of the word that is the point of watching.
func TestLevelRisesFasterThanItFalls(t *testing.T) {
	l := newLeveler()
	settle(l, -55, 200)

	up := level(l, -30, 3)
	settle(l, -30, 40)
	down := level(l, -55, 3)

	if up < 0.2 {
		t.Errorf("three frames of speech reached %.2f, want it well up already", up)
	}
	if down < 0.5 {
		t.Errorf("three frames of quiet fell to %.2f, want it still most of the way up", down)
	}
}

// The LED ring's whine lands above a kilohertz — measured at 25 dB over a dark room at 3 to 5 kHz,
// and nothing at all below 1 kHz. The level has to ignore it, or lighting the ring reads as a busy
// room and anything driving the ring from the level chases itself.
func TestLevelIgnoresTheBandTheRingWhinesIn(t *testing.T) {
	quiet := speech(-55)

	for _, hz := range []float64{3000, 4500} {
		l := newLeveler()
		for range 200 {
			l.observe(quiet)
		}

		// The same level as speech that reads as loud, but up where the ring lives.
		whine := tone(hz, -30)
		var loudest float64
		for range 60 {
			l.observe(whine)
			loudest = math.Max(loudest, float64(math.Float32frombits(l.level.Load())))
		}
		if loudest > 0.1 {
			t.Errorf("%.0f Hz at -30 dBFS drove the level to %.2f, want it ignored", hz, loudest)
		}
	}

	// And the same level in the speech band still reads, or the filter has thrown out the signal
	// along with the whine.
	l := newLeveler()
	for range 200 {
		l.observe(quiet)
	}
	if got := level(l, -30, 60); got < 0.5 {
		t.Errorf("speech at -30 dBFS reads %.2f, want most of the way up", got)
	}
}

// A room's own steady noise sits at the bottom of the spectrum and never stops. It has to read as
// nothing, or a ring following the room follows the fridge.
func TestLevelIgnoresSteadyRoomNoise(t *testing.T) {
	for _, hz := range []float64{60, 120} {
		l := newLeveler()

		// Loud steady hum, then a few dB louder, which is what a compressor cycling does.
		hum := tone(hz, -45)
		for range 300 {
			l.observe(hum)
		}

		var loudest float64
		for range 100 {
			l.observe(tone(hz, -40))
			loudest = math.Max(loudest, float64(math.Float32frombits(l.level.Load())))
		}
		if loudest > 0.1 {
			t.Errorf("%.0f Hz hum rising 5 dB drove the level to %.2f, want it ignored", hz, loudest)
		}
	}
}

// tone is a frame at one frequency and level, for asking what the level does with a band.
func tone(hz, dbfs float64) []int16 {
	frame := make([]int16, FrameSamples)
	amp := math.Pow(10, dbfs/20) * fullScale * math.Sqrt2

	for i := range frame {
		frame[i] = int16(amp * math.Sin(2*math.Pi*hz*float64(i)/Rate))
	}
	return frame
}

// settle feeds a steady level without reading anything back, for getting a room's floor established.
func settle(l *leveler, dbfs float64, frames int) {
	for range frames {
		l.observe(speech(dbfs))
	}
}

// level feeds a steady level and reports what was published at the end of it.
func level(l *leveler, dbfs float64, frames int) float64 {
	settle(l, dbfs, frames)
	return float64(math.Float32frombits(l.level.Load()))
}
