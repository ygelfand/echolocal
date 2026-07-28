// Package service supervises the parts of echod that have a life of their own.
//
// echod is a handful of long-running loops over hardware that Android can take away: the ring, the
// speaker, the microphones, wake detection, the conversation, the API server, the mDNS advert. Each
// can fail on its own, and a bare goroutine that returns takes its subsystem with it for the rest of
// the process — a speaker that hits an ALSA error means no sound until the device is rebooted.
//
// A Group starts them in order, restarts what breaks, and can say what is broken, which is the part
// that matters on a device with no screen.
package service

import (
	"context"
	"time"
)

// Service is a loop with a name.
type Service interface {
	// Name is how it appears in logs and diagnostics.
	Name() string

	// Run holds the service until ctx is cancelled. Returning nil means it finished or stopped
	// cleanly; returning an error means it broke, and a restartable service is started again.
	Run(ctx context.Context) error
}

// Starter acquires whatever the service needs. It runs before Run, and again on every restart:
// re-acquiring is usually the whole point, since the device it wants may have been taken by Android
// while it was gone.
type Starter interface {
	Start(ctx context.Context) error
}

// Closer releases it again, after Run has returned and before any restart.
type Closer interface {
	Close() error
}

// State is where a service has got to.
type State string

const (
	// StateWaiting is registered but not yet started.
	StateWaiting State = "waiting"

	// StateRunning is up.
	StateRunning State = "running"

	// StateRetrying is down and about to be started again.
	StateRetrying State = "retrying"

	// StateFailed is down and not coming back: either it is not restartable, or it could not be
	// acquired and was optional.
	StateFailed State = "failed"

	// StateStopped is down because it was asked to stop.
	StateStopped State = "stopped"
)

// Status is what a service is doing, for diagnostics.
type Status struct {
	Name     string
	State    State
	Err      error
	Restarts int

	// Since is when it entered this state.
	Since time.Time
}

// Healthy reports whether everything that was meant to be running is.
func Healthy(all []Status) bool {
	for _, s := range all {
		if s.State == StateFailed || s.State == StateRetrying {
			return false
		}
	}
	return true
}

// policy is how a Group treats one service.
type policy struct {
	// required means the process cannot do its job without it: failing to acquire it is fatal.
	required bool

	// restart means Run returning an error should be followed by starting it again.
	restart bool

	backoff    time.Duration
	maxBackoff time.Duration

	// steady is how long a service must stay up before its backoff is forgotten, so a service that
	// breaks once an hour does not creep towards the maximum delay.
	steady time.Duration
}

// Option adjusts how a service is supervised.
type Option func(*policy)

// Required marks a service the process cannot run without. If it cannot be acquired, Run gives up
// and echod exits, which is the right answer when init will start it again in a moment.
func Required() Option {
	return func(p *policy) { p.required = true }
}

// Restart keeps a service alive: when it breaks it is closed, acquired again and run again, waiting
// a little longer each time up to max.
func Restart(initial, max time.Duration) Option {
	return func(p *policy) {
		p.restart = true
		p.backoff = initial
		p.maxBackoff = max
	}
}

// Once marks a service that is expected to finish, such as the boot animation. Returning nil leaves
// it stopped rather than looking like something that died.
func Once() Option {
	return func(p *policy) { p.restart = false }
}

func defaults() policy {
	return policy{
		backoff:    time.Second,
		maxBackoff: 30 * time.Second,
		steady:     time.Minute,
	}
}
