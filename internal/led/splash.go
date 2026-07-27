package led

import (
	"context"
	"fmt"
	"math"
	"time"
)

// HomeAssistant is Home Assistant's brand blue.
var HomeAssistant = Color{R: 0x18, G: 0xBC, B: 0xF2}

// FrameInterval paces animations at 25 fps. Each frame is one 36-byte i2c write, which the
// driver absorbs comfortably at this rate.
const FrameInterval = 40 * time.Millisecond

// Effects the ring can run, as Home Assistant names them.
const (
	EffectComet   = "Comet"
	EffectRainbow = "Rainbow"
	EffectPulse   = "Pulse"
)

// EffectNames lists the effects, for the light entity's effect list.
func EffectNames() []string { return []string{EffectComet, EffectRainbow, EffectPulse} }

// Arc lights a clockwise fraction of the ring, from 0 to 1, dimming the leading segment by
// whatever is left over. Twelve segments cannot show thirty volume steps, so the partial
// segment is what makes each step visible — the same trick Amazon's firmware used.
//
// It fills from segment 11 so the arc grows across the front of the device.
func Arc(fraction float64, c Color) []Color {
	fraction = math.Max(0, math.Min(1, fraction))
	lit := fraction * Segments

	out := make([]Color, Segments)
	for i := range Segments {
		remaining := lit - float64(i)
		if remaining <= 0 {
			break
		}
		level := math.Min(1, remaining)
		out[(11+i)%Segments] = scale(c, level)
	}
	return out
}

// effect returns the frame function for a named effect, or nil if there is no such effect.
// base is the colour Home Assistant set, which effects use where it makes sense.
func effect(name string, base Color) func(time.Duration) []Color {
	switch name {
	case EffectComet:
		return comet(base)
	case EffectRainbow:
		return rainbow
	case EffectPulse:
		return breathe(base)
	}
	return nil
}

// RunEffect animates until ctx is cancelled.
func RunEffect(ctx context.Context, r *Ring, name string, base Color) error {
	frame := effect(name, base)
	if frame == nil {
		return fmt.Errorf("led: no effect %q", name)
	}
	return play(ctx, r, 0, frame)
}

// Splash spins a Home Assistant blue comet until ctx is cancelled, then fades out. echod runs it
// from start-up until everything is online.
func Splash(ctx context.Context, r *Ring) error {
	if err := play(ctx, r, 0, comet(HomeAssistant)); err != nil {
		return err
	}

	// ctx is done by now, so the fade needs its own.
	fade, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return fadeOut(fade, r, 250*time.Millisecond)
}

// play runs an animation for d, or until ctx is cancelled when d is zero.
func play(ctx context.Context, r *Ring, d time.Duration, frame func(time.Duration) []Color) error {
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
			return r.Off()
		case <-t.C:
		}
	}
}

// comet runs a bright head clockwise with a decaying tail behind it.
func comet(base Color) func(time.Duration) []Color {
	// Two frames per segment, so the head advances evenly.
	const perSegment = 2 * FrameInterval
	tail := []float64{1, 0.55, 0.3, 0.16, 0.08}

	return func(elapsed time.Duration) []Color {
		head := int(elapsed/perSegment) % Segments
		out := make([]Color, Segments)
		for i, f := range tail {
			out[((head-i)%Segments+Segments)%Segments] = scale(base, f)
		}
		return out
	}
}

// rainbow spins a full hue wheel around the ring.
func rainbow(elapsed time.Duration) []Color {
	const revolution = 1500 * time.Millisecond

	phase := float64(elapsed%revolution) / float64(revolution)
	out := make([]Color, Segments)
	for i := range out {
		out[i] = scale(hue(math.Mod(float64(i)/Segments+phase, 1)), 0.6)
	}
	return out
}

// breathe pulses the whole ring.
func breathe(base Color) func(time.Duration) []Color {
	const period = 1200 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		f := (1 - math.Cos(2*math.Pi*float64(elapsed%period)/float64(period))) / 2
		out := make([]Color, Segments)
		for i := range out {
			out[i] = scale(base, f)
		}
		return out
	}
}

func fadeOut(ctx context.Context, r *Ring, d time.Duration) error {
	steps := int(d / FrameInterval)
	for i := steps; i > 0; i-- {
		all := make([]Color, Segments)
		for j := range all {
			all[j] = scale(HomeAssistant, float64(i)/float64(steps))
		}
		if err := r.SetSegments(all); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return r.Off()
		case <-time.After(FrameInterval):
		}
	}
	return r.Off()
}

func scale(c Color, f float64) Color {
	clamp := func(v byte) byte { return byte(math.Round(math.Max(0, math.Min(255, float64(v)*f)))) }
	return Color{R: clamp(c.R), G: clamp(c.G), B: clamp(c.B)}
}

// hue converts a 0-1 position on the colour wheel to full-saturation RGB.
func hue(h float64) Color {
	x := math.Mod(h*6, 6)
	ramp := func(v float64) byte { return byte(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	switch int(x) {
	case 0:
		return Color{R: 255, G: ramp(x), B: 0}
	case 1:
		return Color{R: ramp(2 - x), G: 255, B: 0}
	case 2:
		return Color{R: 0, G: 255, B: ramp(x - 2)}
	case 3:
		return Color{R: 0, G: ramp(4 - x), B: 255}
	case 4:
		return Color{R: ramp(x - 4), G: 0, B: 255}
	default:
		return Color{R: 255, G: 0, B: ramp(6 - x)}
	}
}
