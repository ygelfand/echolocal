package led

import (
	"math"
	"slices"
	"testing"
	"time"
)

// frames is ten seconds of an effect at the rate the driver runs it, which is long enough for the
// slowest thing in the catalogue to come round more than once.
func frames(f Frame) [][]Color {
	out := make([][]Color, 0, 250)
	for step := range cap(out) {
		out = append(out, f(time.Duration(step)*FrameInterval))
	}
	return out
}

// built is an effect as the driver would run it, on a ring set to base, through the same constructor
// the driver uses so that palette defaulting and the room are wired the way they really are.
//
// An effect that reacts to the room is handed a room that rises and falls, a step per frame, because
// that is where its animation comes from: hand one a silent room and it is right to hold still.
func built(t *testing.T, e Effect, base Color) Frame {
	t.Helper()

	var heard int
	frame, err := effect(e.Name, base, func() float64 {
		heard++
		return math.Abs(math.Sin(float64(heard) / 40))
	})
	if err != nil {
		t.Fatalf("building it failed: %v", err)
	}
	return frame
}

// Whatever else an effect does, it has to be twelve colours, it has to change, and it has to light
// something. A frame function that returns the wrong length fails the write for as long as the
// effect is up, and one that never changes or never lights anything is a broken effect that looks
// exactly like a ring nobody has asked for anything.
func TestEveryEffectAnimatesAndLightsTheRing(t *testing.T) {
	for _, e := range effects {
		t.Run(e.Name, func(t *testing.T) {
			var lit, moved bool
			all := frames(built(t, e, HomeAssistant))
			for i, f := range all {
				if len(f) != Segments {
					t.Fatalf("frame %d has %d segments, want %d", i, len(f), Segments)
				}
				if slices.ContainsFunc(f, func(c Color) bool { return c != Color{} }) {
					lit = true
				}
				if i > 0 && !slices.Equal(f, all[i-1]) {
					moved = true
				}
			}
			if !lit {
				t.Error("never lights a segment")
			}
			if !moved {
				t.Error("never changes")
			}
		})
	}
}

// The catalogue says which colours an effect uses, and the two answers are worth holding to. An
// effect that inherits may only dim the colour it was given: adding a channel of its own means Home
// Assistant asks for red and gets something that is not red, which is the one thing a light entity
// promises. An effect with a palette must ignore the ring's colour completely, or the same name means
// something different depending on what the light was last set to.
func TestEffectsUseTheColoursTheCatalogueSaysTheyDo(t *testing.T) {
	for _, e := range effects {
		t.Run(e.Name, func(t *testing.T) {
			red, blue := frames(built(t, e, Color{R: 255})), frames(built(t, e, Color{B: 255}))

			if len(e.Palette) > 0 {
				for i := range red {
					if !slices.Equal(red[i], blue[i]) {
						t.Fatalf("frame %d differs with the ring's colour, but this effect has a palette", i)
					}
				}
				return
			}

			for i, f := range red {
				for seg, c := range f {
					if c.G != 0 || c.B != 0 {
						t.Fatalf("frame %d segment %d is %+v, want red only", i, seg, c)
					}
				}
			}
		})
	}
}

// Settings store the effect by name — the wake animation per assistant, and the default. A rename
// reads back as an effect that does not exist, which turns the animation off without saying so.
func TestNamesThatSettingsStoreAreStillOffered(t *testing.T) {
	for _, name := range []string{"Comet", "Rainbow", "Pulse"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("effect %q is gone; settings that stored it now mean nothing", name)
		}
	}
}

// Every entry has to be buildable and has to say where it may be used, and the two have to agree:
// an effect that watches the room cannot be offered to a light that has no room to give it, and one
// that watches nothing cannot be offered as something that reacts.
func TestEveryEffectIsBuildableAndSaysWhereItBelongs(t *testing.T) {
	for _, e := range effects {
		t.Run(e.Name, func(t *testing.T) {
			switch {
			case e.New == nil && e.Senses == nil:
				t.Error("has no frame function at all")
			case e.New != nil && e.Senses != nil:
				t.Error("has two frame functions; which one runs is then a matter of luck")
			}

			if e.Kinds == 0 {
				t.Error("says nowhere it may be used, so nothing will ever offer it")
			}
			if (e.Senses != nil) != e.Kinds.Has(KindRoom) {
				t.Errorf("kinds %04b and watching the room disagree", e.Kinds)
			}
			if e.Senses != nil && e.Kinds != KindRoom {
				t.Errorf("kinds %04b: watching the room is all one of these can do", e.Kinds)
			}
		})
	}
}

// The lists Home Assistant is shown are the point of the split. An effect that needs a room in the
// light's dropdown is a broken option: choosing it would fail every time.
func TestTheListsOfferOnlyWhatFitsThem(t *testing.T) {
	for _, name := range Names(KindLight) {
		if byName[name].Senses != nil {
			t.Errorf("the light offers %q, which cannot run without a room", name)
		}
	}
	for _, name := range Names(KindRoom) {
		if byName[name].Senses == nil {
			t.Errorf("the room reaction offers %q, which does not react to anything", name)
		}
	}

	// And asking for one the wrong way round says so rather than showing a dark ring.
	if _, err := effect(EffectRoomGlow, HomeAssistant, nil); err == nil {
		t.Error("building a room effect without a room succeeded, want an error")
	}
	if _, err := effect("Nothing Like This", HomeAssistant, nil); err == nil {
		t.Error("building an effect that does not exist succeeded, want an error")
	}
}

func TestEffectNamesAreUnique(t *testing.T) {
	if len(byName) != len(effects) {
		t.Errorf("catalogue has %d effects but %d names; one is shadowing another", len(effects), len(byName))
	}
}

// A palette laid round the ring has to meet itself, and one laid along a gradient has to keep its
// ends apart. Getting these the wrong way round is a seam where the hottest colour touches the
// coldest, which is exactly what the two samplers exist to avoid.
func TestPaletteWrapsRoundAndClampsAlong(t *testing.T) {
	p := Palette{{R: 255}, {G: 255}, {B: 255}}

	if got, want := p.At(0), p.At(1); got != want {
		t.Errorf("At(0) is %+v and At(1) is %+v, want the circle to close", got, want)
	}
	if got, want := p.At(1.0/3), p[1]; got != want {
		t.Errorf("At(1/3) is %+v, want the second colour %+v", got, want)
	}
	if got, want := p.At(-1.0/3), p[2]; got != want {
		t.Errorf("At(-1/3) is %+v, want the last colour %+v", got, want)
	}
	// Half way between the first two, give or take where the division lands.
	if mid := p.At(1.0 / 6); mid.R < 127 || mid.R > 128 || mid.G < 127 || mid.G > 128 || mid.B != 0 {
		t.Errorf("At(1/6) is %+v, want half of each of the first two", mid)
	}

	if got, want := p.Along(0), p[0]; got != want {
		t.Errorf("Along(0) is %+v, want %+v", got, want)
	}
	if got, want := p.Along(1), p[2]; got != want {
		t.Errorf("Along(1) is %+v, want the last colour %+v, not the first", got, want)
	}
	if got, want := p.Along(2), p[2]; got != want {
		t.Errorf("Along(2) is %+v, want it held at the end %+v", got, want)
	}

	// One colour is what an inheriting effect gets, and neither sampler may turn it into anything
	// else however it is asked.
	one := Palette{HomeAssistant}
	if got := one.Along(0.7); got != HomeAssistant {
		t.Errorf("Along on a single colour gave %+v, want %+v", got, HomeAssistant)
	}
	if got := one.Nth(-5); got != HomeAssistant {
		t.Errorf("Nth on a single colour gave %+v, want %+v", got, HomeAssistant)
	}
}

// The two thumps are the whole point of a heartbeat: one beat per period is a slow blink, and a
// decay that outlasts the gap merges them into a single lump.
func TestHeartbeatThumpsTwiceThenRests(t *testing.T) {
	const period = 1400 * time.Millisecond
	frame := heartbeat(Palette{{R: 255}})

	var peaks int
	var prev, rising = 0.0, false
	for step := range int(period / FrameInterval) {
		f := float64(frame(time.Duration(step) * FrameInterval)[0].R)
		if f > prev {
			rising = true
		} else if rising {
			peaks++
			rising = false
		}
		prev = f
	}
	if peaks != 2 {
		t.Errorf("%d thumps in a period, want 2", peaks)
	}

	// And it is dark by the end, or the rest is not a rest.
	if end := frame(period - FrameInterval)[0]; end != (Color{}) {
		t.Errorf("period ends at %+v, want dark", end)
	}
}

// The marquee only looks right if the gaps are even, including across the seam from segment 11 back
// to segment 0, and if the whole pattern moves rather than one lamp hopping. Each lamp also has to
// keep its colour as the pattern steps, or a wheel turns into a flicker of changing colours.
func TestChaseKeepsEvenGapsAndStepsWithItsColours(t *testing.T) {
	const step = 110 * time.Millisecond
	p := Palette{{R: 255}, {G: 255}, {B: 255}, {R: 255, G: 255}}
	frame := chase(p)

	var first []int
	for s := range 4 {
		var lit []int
		for i, c := range frame(time.Duration(s)*step + step/2) {
			if c == (Color{}) {
				continue
			}
			if want := p.Nth(len(lit)); c != want {
				t.Errorf("step %d segment %d is %+v, want lamp %d's %+v", s, i, c, len(lit), want)
			}
			lit = append(lit, i)
		}
		if len(lit) != Segments/3 {
			t.Fatalf("step %d lit %v, want %d segments", s, lit, Segments/3)
		}
		for i := 1; i < len(lit); i++ {
			if lit[i]-lit[i-1] != 3 {
				t.Errorf("step %d lit %v, want every third segment", s, lit)
			}
		}

		switch s {
		case 0:
			first = lit
		case 3:
			// Three steps at a spacing of three is back where it started.
			if !slices.Equal(lit, first) {
				t.Errorf("after a full cycle lit %v, want %v", lit, first)
			}
		default:
			if lit[0] != first[0]+s {
				t.Errorf("step %d starts at %d, want %d", s, lit[0], first[0]+s)
			}
		}
	}
}
