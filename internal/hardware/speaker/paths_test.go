package speaker

import (
	"math"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

func TestRouteForEachModeAndPlug(t *testing.T) {
	auto, on, off := config.OutputAuto, config.OutputOn, config.OutputOff
	for _, c := range []struct {
		plugged            bool
		speaker, headphone config.OutputMode
		want               route
	}{
		{false, auto, auto, route{out: OutputSpeaker, speaker: true}},
		{true, auto, auto, route{out: OutputHeadphone, speaker: false}},
		{true, on, auto, route{out: OutputHeadphone, speaker: true}},
		{true, on, off, route{out: OutputSpeaker, speaker: true}},
		{false, off, on, route{out: OutputHeadphone, speaker: false}},
		{true, off, off, route{out: OutputSpeaker, speaker: false}},
		{false, auto, off, route{out: OutputSpeaker, speaker: true}},
	} {
		if got := routeFor(c.plugged, c.speaker, c.headphone); got != c.want {
			t.Errorf("plugged=%t speaker=%s headphone=%s: got %+v, want %+v",
				c.plugged, c.speaker, c.headphone, got, c.want)
		}
	}
}

func TestGainForFollowsTheCurveAtEachStep(t *testing.T) {
	cases := []struct {
		out  Output
		step int
		want float32
	}{
		{OutputSpeaker, 0, 0},
		{OutputSpeaker, 30, 1},
		{OutputSpeaker, 15, 0.398},
		{OutputSpeaker, 5, 0.0708},
		{OutputHeadphone, 0, 0},
		{OutputHeadphone, 30, 1},
		{OutputHeadphone, 15, 0.112},
	}

	for _, c := range cases {
		got := gainFor(c.out, float64(c.step)/VolumeSteps)
		if math.Abs(float64(got-c.want)) > 0.001 {
			t.Errorf("gainFor(%s, step %d) = %v, want %v", c.out, c.step, got, c.want)
		}
	}
}

func TestGainForMainTimesLevelMatchesEitherWay(t *testing.T) {
	a := gainFor(OutputSpeaker, 1.0*0.5)
	b := gainFor(OutputSpeaker, 0.5*1.0)
	if a != b || math.Abs(float64(a-0.398)) > 0.001 {
		t.Errorf("main 100 level 50 = %v, main 50 level 100 = %v, want both 0.398", a, b)
	}
	between := gainFor(OutputSpeaker, 15.5/VolumeSteps)
	if between <= gainFor(OutputSpeaker, 0.5) ||
		between >= gainFor(OutputSpeaker, 16.0/VolumeSteps) {
		t.Errorf("half a step = %v, want between steps 15 and 16", between)
	}
}

func TestGainForClamps(t *testing.T) {
	if got := gainFor(OutputSpeaker, -0.2); got != 0 {
		t.Errorf("gainFor(speaker, -0.2) = %v, want 0", got)
	}
	if got := gainFor(OutputSpeaker, 3); got != 1 {
		t.Errorf("gainFor(speaker, 3) = %v, want 1", got)
	}
}

func TestGainForUnknownOutput(t *testing.T) {
	if got := gainFor(Output("bluetooth"), 1); got != 1 {
		t.Errorf("gainFor(bluetooth, 1) = %v, want 1", got)
	}
}
