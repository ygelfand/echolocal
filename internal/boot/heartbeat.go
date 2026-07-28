package boot

import (
	"context"
	"log/slog"
	"time"
)

// heartbeatEvery is how often echod says it is still there.
const heartbeatEvery = time.Minute

// heartbeat keeps echod resident.
//
// It exists to be found in logcat: a boot can be checked long after it happened, and a process that
// has wedged looks different from one that is merely idle. It is the last service in the group, so its
// absence says the rest never got that far.
type heartbeat struct{}

func (heartbeat) Name() string { return "heartbeat" }

func (heartbeat) Run(ctx context.Context) error {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			slog.Info("alive")
		}
	}
}
