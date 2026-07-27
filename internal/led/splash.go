package led

import (
	"context"
	"math"
	"time"
)

// HomeAssistant is Home Assistant's brand blue.
var HomeAssistant = Color{R: 0x18, G: 0xBC, B: 0xF2}

// FrameInterval paces animations at 25 fps. Each frame is one 36-byte i2c write, which the
// driver absorbs comfortably at this rate.
const FrameInterval = 40 * time.Millisecond

// Splash plays EchoLocal's boot animation for total, then leaves the ring dark.
//
// Three movements — a Home Assistant blue comet, a rainbow rotation, then blue pulses —
// so that a glance at the ring says which firmware booted and that echod reached its
// hardware. Returns early if ctx is cancelled, always blanking the ring on the way out.
func Splash(ctx context.Context, r *Ring, total time.Duration) error {
	if total <= 0 {
		return r.Off()
	}

	movements := []struct {
		share float64
		frame func(elapsed time.Duration) []Color
	}{
		{0.3, comet},
		{0.4, rainbow},
		{0.3, breathe},
	}

	for _, m := range movements {
		if err := play(ctx, r, time.Duration(float64(total)*m.share), m.frame); err != nil {
			return err
		}
	}
	return fadeOut(ctx, r, 400*time.Millisecond)
}

func play(ctx context.Context, r *Ring, d time.Duration, frame func(time.Duration) []Color) error {
	t := time.NewTicker(FrameInterval)
	defer t.Stop()

	start := time.Now()
	for {
		elapsed := time.Since(start)
		if elapsed >= d {
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
func comet(elapsed time.Duration) []Color {
	const perSegment = 60 * time.Millisecond
	tail := []float64{1, 0.55, 0.3, 0.16, 0.08}

	head := int(elapsed/perSegment) % Segments
	out := make([]Color, Segments)
	for i, f := range tail {
		// Segment indices increase clockwise, so the tail sits at lower indices.
		out[((head-i)%Segments+Segments)%Segments] = scale(HomeAssistant, f)
	}
	return out
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

// breathe pulses the whole ring in brand blue.
func breathe(elapsed time.Duration) []Color {
	const period = 1200 * time.Millisecond

	f := (1 - math.Cos(2*math.Pi*float64(elapsed%period)/float64(period))) / 2
	out := make([]Color, Segments)
	for i := range out {
		out[i] = scale(HomeAssistant, f)
	}
	return out
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
