package boot

import (
	"context"
	"log/slog"
	"time"
)

// heartbeatEvery is how often echod says it is still there, and how often it samples what drifts.
//
// Five minutes rather than one: the readings that change every time are the load average and the
// temperatures, and numbers nobody watches by the second are not worth a state change a minute in Home
// Assistant's recorder. It keeps the log line rare enough to be worth reading, too.
const heartbeatEvery = 5 * time.Minute

// heartbeat keeps echod resident and samples what changes on its own.
//
// It exists to be found in logcat: a boot can be checked long after it happened, and a process that
// has wedged looks different from one that is merely idle. It is the last service in the group, so its
// absence says the rest never got that far.
type heartbeat struct {
	// sample publishes the readings that drift — the disk, the temperatures, the cores. Nil when there
	// is nothing to publish them to.
	sample func()
}

func (heartbeat) Name() string { return "heartbeat" }

func (h heartbeat) Run(ctx context.Context) error {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			slog.Info("alive")
			if h.sample != nil {
				h.sample()
			}
		}
	}
}
