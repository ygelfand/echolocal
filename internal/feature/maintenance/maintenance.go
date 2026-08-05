// Package maintenance is the housekeeping the device does to itself on a timer: the periodic work
// that keeps disk from filling, with no setting because there is no choice to make about it.
package maintenance

import (
	"context"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/feature/recording"
)

// Period is how often the housekeeping runs. Not a setting: recordings are already pruned as each
// turn ends, so this is only the backstop for what a restart or a changed limit left behind, and
// nothing about the device changes fast enough for the interval to be worth a knob.
const Period = 5 * time.Minute

func init() {
	component.Register(component.Device, Get(), component.Order(80))
}

var (
	once   sync.Once
	shared *Maintenance
)

type Maintenance struct{}

func Get() *Maintenance {
	once.Do(func() { shared = &Maintenance{} })
	return shared
}

func (m *Maintenance) Name() string { return "maintenance" }

// Run sweeps once at start, so a restart clears its leftovers straight away, then on the period.
func (m *Maintenance) Run(ctx context.Context) error {
	t := time.NewTicker(Period)
	defer t.Stop()

	for {
		recording.Get().Prune()

		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
