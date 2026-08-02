package firmware

import (
	"context"
	"time"
)

// checkEvery is how often the device looks for a newer build of itself.
//
// It has to look on its own: esphome entities do not poll, so the only check Home Assistant ever sends
// is somebody pressing refresh, and a device left alone would never learn a release exists. Daily,
// because releases are not frequent and a satellite is not a thing anybody wants generating traffic.
const checkEvery = 24 * time.Hour

// checkSettle is how long after starting the first check happens. Not immediately: a device coming up
// has a wake word to load and a network that may not be there yet, and nothing is waiting on this.
const checkSettle = 5 * time.Minute

// Run looks for a newer build on its own schedule.
func (f *Firmware) Run(ctx context.Context) error {
	first := time.NewTimer(checkSettle)
	defer first.Stop()

	select {
	case <-ctx.Done():
		return nil
	case <-first.C:
		f.Check(ctx)
	}

	t := time.NewTicker(checkEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			f.Check(ctx)
		}
	}
}
