package led

import (
	"context"
	"time"
)

// Until Home Assistant subscribes a voice pipeline the device hears a wake word perfectly well and
// can do nothing with it, so booting and ready have to look different. The wait steps around the
// ring; being ready glides once and stops.
const (
	// WalkStep is how long each position of the boot walk holds. Six positions around the ring, so a
	// revolution takes six of these.
	WalkStep = 250 * time.Millisecond

	// SplashConfirm is how long the comet runs once the pipeline is listening.
	SplashConfirm = 2 * time.Second

	// SplashWait bounds the Home Assistant wait indication. A missing subscription is useful state
	// during startup, but leaving the cyan ring spinning forever looks like recovery or a boot loop.
	SplashWait = 60 * time.Second
)

// Splash animates the ring while the device comes up, then fades out. It steps around the ring
// until ready reports true or SplashWait expires. A ready device runs the confirmation comet; a
// device Home Assistant has not subscribed to simply fades out instead of looking stuck in recovery.
// A nil ready goes straight to the comet.
func Splash(ctx context.Context, r *Ring, ready func() bool) error {
	return splash(ctx, r, ready, SplashWait, SplashConfirm, 250*time.Millisecond)
}

func splash(ctx context.Context, r *Ring, ready func() bool, wait, confirm, fadeDuration time.Duration) error {
	confirmed := ready == nil
	if ready != nil {
		waiting, cancel := context.WithTimeout(ctx, wait)
		var err error
		confirmed, err = until(waiting, r, walk(HomeAssistant), ready)
		cancel()
		if err != nil {
			return err
		}
	}
	if confirmed {
		if err := play(ctx, r, confirm, comet(Palette{HomeAssistant})); err != nil {
			return err
		}
	}

	// ctx may be done by now, so the fade needs its own.
	fade, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return fadeOut(fade, r, fadeDuration)
}

// until animates frame until ready reports true, or ctx is cancelled. The boolean distinguishes a
// real ready signal from a timeout so callers do not show a false confirmation animation.
func until(ctx context.Context, r *Ring, frame Frame, ready func() bool) (bool, error) {
	t := time.NewTicker(FrameInterval)
	defer t.Stop()

	start := time.Now()
	for !ready() {
		if err := r.SetSegments(frame(time.Since(start))); err != nil {
			return false, err
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-t.C:
		}
	}
	return true, nil
}

// walk lights one segment at a time, hopping two positions per step so it lands on every other
// segment. Holding each position and skipping the one between reads as stepping, where the comet
// glides and has a tail: waiting should not look like a slow version of being ready.
//
// It is not in the catalogue. Boot is the one thing on the ring nobody chooses.
func walk(base Color) Frame {
	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		out[2*int(elapsed/WalkStep)%Segments] = base
		return out
	}
}

func fadeOut(ctx context.Context, r *Ring, d time.Duration) error {
	steps := int(d / FrameInterval)
	for i := steps; i > 0; i-- {
		if err := r.SetSegments(around(Palette{HomeAssistant}, float64(i)/float64(steps))); err != nil {
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
