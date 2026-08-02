package led

import (
	"context"
	"fmt"
	"time"
)

// FrameInterval paces animations at 25 fps. Each frame is one 36-byte i2c write, which the
// driver absorbs comfortably at this rate.
const FrameInterval = 40 * time.Millisecond

// Frame is one moment of an animation: what the twelve segments show at elapsed.
//
// A frame function is a pure function of elapsed and keeps nothing between calls. That is what lets
// the driver interrupt an effect part way through, run it backwards, or start it again later with
// nothing to reset, and what lets a test ask an effect what it looks like at a given moment.
type Frame func(elapsed time.Duration) []Color

// Kinds is where an effect may be used. It is a set rather than one answer, because most animations
// are fit for more than one job: a comet loops perfectly well as the ring's resting appearance and
// also works held for a second and a half to say that something happened. What an effect is good for
// and what it is being used for are different questions.
//
// The kinds are not categories for tidiness. They differ in what an effect is allowed to do and in
// who is allowed to pick it.
type Kinds uint8

const (
	// KindLight is an animation that loops and can be chosen: the ring light's effect list, and the
	// per-event choices — a wake word, a failure, a muted microphone — which are the same animations
	// held for as long as the event lasts. How long that is belongs to the event, not the animation.
	KindLight Kinds = 1 << iota

	// KindRoom reacts to the room instead of to the clock: it is handed how loud the room is and reads
	// it every frame. It is chosen by its own control rather than from the light's effect list, and
	// while it is chosen it is simply on — the resting appearance of a device that is listening, which
	// a conversation covers and then gives back.
	//
	// It is exempt from the rule that a frame function is a pure function of elapsed, necessarily: the
	// same moment looks different depending on the room. So reversing one means nothing, and starting
	// one again does not give back what it showed the first time.
	KindRoom
)

// Has reports whether an effect may be used for a kind.
func (k Kinds) Has(want Kinds) bool { return k&want != 0 }

// Room is what an effect that watches the room can ask about it, read every frame rather than passed as
// numbers: what it shows is whatever is true at the moment it draws.
type Room struct {
	// Level is how loud it is now, 0 for quiet to 1 for someone talking close by, measured against the
	// room's own noise floor.
	Level func() float64

	// Facing is where the loudest sound is, as a fraction clockwise round the ring from segment 0, and
	// whether that is known: it takes a frame of asking, and a room with nothing in it has no answer.
	Facing func() (float64, bool)
}

// Effect is one animation the ring can run: a motion paired with the colours it runs in. Keeping
// them separate is what lets one motion appear both in the ring's own colour and in colours of its
// own.
type Effect struct {
	// Name is what Home Assistant shows and what the config stores, so renaming one reads back as an
	// effect that does not exist and silently turns the animation off.
	Name string

	// Palette is the colours this pairing uses. Empty means it inherits: the ring's own colour, as
	// one palette of one colour, so the frame function cannot tell the difference.
	Palette Palette

	// Kinds is where this one may be used. Zero takes the default for the list it is in, which is how
	// thirty-odd entries avoid repeating the same answer: an entry only says anything here when it
	// differs from its neighbours.
	Kinds Kinds

	// New builds the motion from the palette. Exactly one of New and Senses is set.
	New func(p Palette) Frame

	// Senses builds a motion that watches the room. Only KindRoom effects have one, and having one is
	// what makes an effect unable to be anything else: there is no sensible still frame to fall back
	// on when nothing is listening for it.
	Senses func(p Palette, r Room) Frame
}

// Effect names. A pairing that brings its own colours is named for the palette and the motion, so
// that the list reads as what it is: the same handful of motions, in colours chosen for them.
const (
	// Ambient, in the ring's colour.
	EffectPulse        = "Pulse"
	EffectHeartbeat    = "Heartbeat"
	EffectRipple       = "Ripple"
	EffectStandingWave = "Standing Wave"
	EffectTwinkle      = "Twinkle"

	// Ambient, in their own.
	EffectCrimsonHeartbeat = "Crimson Heartbeat"
	EffectAuroraPulse      = "Aurora Pulse"
	EffectCandle           = "Candle"
	EffectFireplace        = "Fireplace"
	EffectEmbers           = "Embers"
	EffectAurora           = "Aurora"
	EffectSunsetDrift      = "Sunset Drift"
	EffectOceanRipple      = "Ocean Ripple"
	EffectRainbowTwinkle   = "Rainbow Twinkle"
	EffectForestTwinkle    = "Forest Twinkle"

	// Motion, in the ring's colour.
	EffectComet    = "Comet"
	EffectChase    = "Chase"
	EffectScanner  = "Scanner"
	EffectPinwheel = "Pinwheel"
	EffectSpiral   = "Spiral"
	EffectWipe     = "Wipe"
	EffectHelix    = "Helix"
	EffectOrbits   = "Orbits"
	EffectBounce   = "Bounce"
	EffectSpring   = "Spring"

	// Announcements.
	EffectAlert  = "Alert"
	EffectBeacon = "Beacon"

	// Reacting to the room. Named for what they follow, not for the motion, because that is the part
	// worth knowing: the ring is showing the room.
	EffectFollower    = "Follower"
	EffectRoomGlow    = "Room Glow"
	EffectRoomMeter   = "Room Meter"
	EffectRoomOcean   = "Room Ocean"
	EffectRoomVU      = "Room VU"
	EffectRoomFire    = "Room Fire"
	EffectRoomSpin    = "Room Spin"
	EffectRoomAurora  = "Room Aurora"
	EffectRoomTwinkle = "Room Twinkle"
	EffectRoomEmbers  = "Room Embers"

	// Motion, in their own.
	EffectRainbow         = "Rainbow"
	EffectFireComet       = "Fire Comet"
	EffectIceComet        = "Ice Comet"
	EffectRainbowChase    = "Rainbow Chase"
	EffectIceScanner      = "Ice Scanner"
	EffectRainbowPinwheel = "Rainbow Pinwheel"
	EffectSunsetSpiral    = "Sunset Spiral"
	EffectRainbowOrbits   = "Rainbow Orbits"
	EffectDNA             = "DNA"
	EffectPacMan          = "Pac-Man"
)

// effects is every pairing this build has, flattened in the order Home Assistant offers them, and
// byName is the same by name. The lists they come from are in catalogue.go, and each motion is a file
// of its own: adding one is a file and a line there, and giving an existing motion another set of
// colours is only the line.
var (
	effects []Effect
	byName  = map[string]Effect{}
)

// catalogue is the lists in the order their contents are offered, each with what its entries are for
// unless an entry says otherwise.
var catalogue = []struct {
	kinds   Kinds
	entries []Effect
}{
	{KindLight, ambientEffects},
	{KindLight, motionEffects},
	{KindLight, alertEffects},
	{KindRoom, roomEffects},
}

func init() {
	for _, list := range catalogue {
		for _, e := range list.entries {
			if e.Kinds == 0 {
				e.Kinds = list.kinds
			}
			effects = append(effects, e)
		}
	}
	for _, e := range effects {
		byName[e.Name] = e
	}
}

// Names lists what may be used for a kind, in catalogue order: the light's effect list and the
// per-event choices are Names(KindLight), and what the ring can be set to react to is Names(KindRoom).
func Names(kinds Kinds) []string {
	out := make([]string, 0, len(effects))
	for _, e := range effects {
		if e.Kinds.Has(kinds) {
			out = append(out, e.Name)
		}
	}
	return out
}

// EffectNames lists what the ring light may be set to.
func EffectNames() []string { return Names(KindLight) }

// effect returns the frame function for a named effect. The room is only needed by an effect that
// senses it, and asking for one of those without it is an error rather than a dark ring.
func effect(name string, base Color, room Room) (Frame, error) {
	e, ok := byName[name]
	if !ok {
		return nil, fmt.Errorf("led: no effect %q", name)
	}

	p := e.Palette
	if len(p) == 0 {
		p = Palette{base}
	}

	if e.Senses != nil {
		if room.Level == nil {
			return nil, fmt.Errorf("led: effect %q reacts to the room and was given nothing to react to", name)
		}
		return e.Senses(p, room), nil
	}
	return e.New(p), nil
}

// RunEffect animates until ctx is cancelled. under is what to show wherever this draws nothing, or nil
// to own the ring outright.
func RunEffect(ctx context.Context, r *Ring, name string, base Color, room Room, under Frame) error {
	frame, err := effect(name, base, room)
	if err != nil {
		return err
	}
	return play(ctx, r, 0, through(frame, under))
}

// through prefers what frame draws and falls back to under wherever frame draws nothing at all.
//
// Both are asked for the same moment, so what is underneath keeps one continuous timeline whether or not
// it is being covered: a comet on the light does not restart every time the room goes quiet.
func through(frame, under Frame) Frame {
	if under == nil {
		return frame
	}

	return func(elapsed time.Duration) []Color {
		out := frame(elapsed)
		for _, c := range out {
			if c != (Color{}) {
				return out
			}
		}
		return under(elapsed)
	}
}

// RunEffectReversed animates the same effect the other way round the ring, which is how the device
// says it has stopped listening and is now waiting on an answer.
//
// Reversing an effect that reacts to the room is allowed and means nothing: what it shows comes from
// the room rather than from a direction of travel.
func RunEffectReversed(ctx context.Context, r *Ring, name string, base Color, room Room, under Frame) error {
	frame, err := effect(name, base, room)
	if err != nil {
		return err
	}
	return play(ctx, r, 0, through(reverse(frame), under))
}

// reverse mirrors a frame around the ring, turning clockwise motion into anticlockwise without an
// effect having to know anything about it. Segment 0 stays put so the reversal is a change of
// direction rather than a jump to somewhere else.
func reverse(frame Frame) Frame {
	return func(elapsed time.Duration) []Color {
		in := frame(elapsed)
		out := make([]Color, len(in))
		for i, c := range in {
			out[(len(in)-i)%len(in)] = c
		}
		return out
	}
}

// play runs an animation for d, or until ctx is cancelled when d is zero.
func play(ctx context.Context, r *Ring, d time.Duration, frame Frame) error {
	t := time.NewTicker(FrameInterval)
	defer t.Stop()

	start := time.Now()
	for {
		elapsed := time.Since(start)
		if d > 0 && elapsed >= d {
			return nil
		}
		if err := r.SetSegments(frame(elapsed)); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			// Left as it is rather than blanked: the driver repaints whatever layer is underneath,
			// and a blank in between shows through as a flicker.
			return nil
		case <-t.C:
		}
	}
}
