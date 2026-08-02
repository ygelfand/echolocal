package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/lib/safe"
)

// Group runs a set of services and keeps them running.
//
// Services are started in the order they were added and stopped in reverse, which is the only
// ordering there is: there is no dependency graph, because the order is short, fixed, and easier to
// read as a list than to derive. Anything that needs to wait for another service waits on a signal of
// its own rather than on being constructed later.
type Group struct {
	mu       sync.Mutex
	entries  []*entry
	started  bool
	stopping bool
}

type entry struct {
	svc    Service
	policy policy

	mu       sync.Mutex
	state    State
	err      error
	restarts int
	since    time.Time
}

// New makes an empty group.
func New() *Group { return &Group{} }

// Add registers a service. It is not started until Run.
func (g *Group) Add(svc Service, opts ...Option) {
	p := defaults()
	for _, o := range opts {
		o(&p)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.started {
		// Adding after Run would silently never start, which is worse than saying so.
		slog.Error("service added after the group started", "service", svc.Name())
		return
	}
	g.entries = append(g.entries, &entry{
		svc:    svc,
		policy: p,
		state:  StateWaiting,
		since:  time.Now(),
	})
}

// Run starts everything and blocks until ctx is cancelled and every service has stopped.
//
// A required service that cannot be acquired is fatal: Run returns its error without starting what
// follows, because a device missing something it cannot work without is better off restarting than
// limping. Anything else that fails is recorded and the rest carry on.
func (g *Group) Run(ctx context.Context) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return errors.New("service: group already running")
	}
	g.started = true
	entries := append([]*entry(nil), g.entries...)
	g.mu.Unlock()

	var wg sync.WaitGroup
	started := make([]*entry, 0, len(entries))

	for _, e := range entries {
		// Acquire before running, in order, so a service that has to take a device off Android does
		// it while nothing else is competing for it.
		began := time.Now()
		if err := e.start(ctx); err != nil {
			e.set(StateFailed, err)
			if e.policy.required {
				slog.Error("required service unavailable, giving up", "service", e.svc.Name(), "err", err)
				g.stop(started, &wg)
				return fmt.Errorf("service: %s: %w", e.svc.Name(), err)
			}
			slog.Error("service unavailable, continuing without it", "service", e.svc.Name(), "err", err)
			continue
		}
		slog.Info("service started", "service", e.svc.Name(), "in", time.Since(began).Round(time.Millisecond))
		started = append(started, e)

		wg.Add(1)
		safe.Go("service "+e.svc.Name(), func() {
			defer wg.Done()
			g.supervise(ctx, e)
		})
	}

	<-ctx.Done()
	g.stop(started, &wg)
	return nil
}

// stop waits for the running services to return, then closes them in reverse order.
func (g *Group) stop(started []*entry, wg *sync.WaitGroup) {
	g.mu.Lock()
	g.stopping = true
	g.mu.Unlock()

	wg.Wait()

	for i := len(started) - 1; i >= 0; i-- {
		started[i].close()
	}
}

// supervise runs one service, restarting it if that is its policy.
func (g *Group) supervise(ctx context.Context, e *entry) {
	wait := e.policy.backoff

	for {
		e.set(StateRunning, nil)
		began := time.Now()

		err := e.svc.Run(ctx)
		ran := time.Since(began)

		if ctx.Err() != nil {
			e.set(StateStopped, nil)
			slog.Info("service stopped", "service", e.svc.Name(), "ran", ran.Round(time.Second))
			return
		}
		if err == nil {
			// Finished on its own, which is what a one-shot service does.
			e.set(StateStopped, nil)
			slog.Info("service finished", "service", e.svc.Name())
			return
		}

		if !e.policy.restart {
			e.set(StateFailed, err)
			slog.Error("service failed", "service", e.svc.Name(), "err", err)
			return
		}

		// A service that stayed up a good while is having a bad moment rather than a bad life, so it
		// starts again from the shortest delay.
		if ran >= e.policy.steady {
			wait = e.policy.backoff
		}

		e.set(StateRetrying, err)
		slog.Error("service failed, restarting", "service", e.svc.Name(),
			"err", err, "ran", ran.Round(time.Millisecond), "in", wait)

		select {
		case <-ctx.Done():
			e.set(StateStopped, nil)
			return
		case <-time.After(wait):
		}

		if next := wait * 2; next <= e.policy.maxBackoff {
			wait = next
		} else {
			wait = e.policy.maxBackoff
		}

		// Let go of whatever it was holding before asking for it again: the point of a restart is
		// usually to re-acquire a device, and holding the old handle would refuse it.
		e.close()

		if err := e.start(ctx); err != nil {
			e.set(StateRetrying, err)
			slog.Error("service could not be reacquired", "service", e.svc.Name(), "err", err)
			continue
		}
		e.countRestart()
		slog.Info("service restarted", "service", e.svc.Name(), "restarts", e.status().Restarts)
	}
}

// Status is what every service is doing.
func (g *Group) Status() []Status {
	g.mu.Lock()
	entries := append([]*entry(nil), g.entries...)
	g.mu.Unlock()

	out := make([]Status, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.status())
	}
	return out
}

func (e *entry) start(ctx context.Context) error {
	s, ok := e.svc.(Starter)
	if !ok {
		return nil
	}
	return s.Start(ctx)
}

func (e *entry) close() {
	c, ok := e.svc.(Closer)
	if !ok {
		return
	}
	if err := c.Close(); err != nil {
		slog.Warn("closing a service failed", "service", e.svc.Name(), "err", err)
	}
}

func (e *entry) set(s State, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.state, e.err, e.since = s, err, time.Now()
}

func (e *entry) countRestart() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.restarts++
}

func (e *entry) status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()

	return Status{
		Name:     e.svc.Name(),
		State:    e.state,
		Err:      e.err,
		Restarts: e.restarts,
		Since:    e.since,
	}
}
