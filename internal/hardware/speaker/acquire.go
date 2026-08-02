package speaker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/lib/alsa"
)

// MediaService is the init service that owns Android's audio HAL.
const MediaService = "media"

const (
	acquireRetry    = 250 * time.Millisecond
	acquireAttempts = 8
)

// Start takes the playback device, off Android if it got there first.
//
// mediaserver retries the open and wins it within a few hundred milliseconds of echod releasing,
// so a restart can leave us with no speaker. Stopping the service releases it; it is started again
// either way, because leaving it down trips the framework watchdog.
func (p *Player) Start(context.Context) error {
	err := p.open()
	if err == nil || !errors.Is(err, alsa.ErrBusy) {
		return err
	}

	slog.Warn("playback device busy, stopping "+MediaService+" to take it", "err", err)
	if err := prop.Stop(MediaService); err != nil {
		return err
	}
	defer func() {
		if err := prop.Start(MediaService); err != nil {
			slog.Error("restarting "+MediaService+" failed", "err", err)
		}
	}()

	for range acquireAttempts {
		time.Sleep(acquireRetry)

		err = p.open()
		if err == nil {
			slog.Info("playback device acquired")
			return nil
		}
		if !errors.Is(err, alsa.ErrBusy) {
			return err
		}
	}
	return err
}

// Acquire is New and Start together, for a tool that wants the speaker for the length of one command.
// The supervised path holds the Player from the start and lets the service take the device.
func Acquire() (*Player, error) {
	p := New()
	if err := p.Start(context.Background()); err != nil {
		return nil, err
	}
	return p, nil
}
