package boot

import (
	"context"
	"time"

	"github.com/ygelfand/echolocal/internal/service"
)

// runner adapts something that already has a run loop to a service, for a subsystem that does not
// need to know it is being supervised. Anything that has to be acquired before it runs, and
// re-acquired after it breaks, implements service.Starter itself instead of going through this.
type runner struct {
	name  string
	run   func(context.Context) error
	close func() error
}

func (r runner) Name() string                  { return r.name }
func (r runner) Run(ctx context.Context) error { return r.run(ctx) }

func (r runner) Close() error {
	if r.close == nil {
		return nil
	}
	return r.close()
}

// forever is the restart policy for anything that should never be down.
func forever() service.Option { return service.Restart(time.Second, 30*time.Second) }
