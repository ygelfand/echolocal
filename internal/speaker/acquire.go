package speaker

import (
	"errors"
	"log/slog"
	"time"

	"github.com/ygelfand/echolocal/internal/alsa"
	"github.com/ygelfand/echolocal/internal/prop"
)

// MediaService is the init service that owns Android's audio HAL.
const MediaService = "media"

const (
	acquireRetry    = 250 * time.Millisecond
	acquireAttempts = 8
)

// Acquire opens the speaker, taking the playback device off Android if it got there first.
//
// mediaserver retries the open and wins it within a few hundred milliseconds of echod releasing,
// so a restart can leave us with no speaker. Stopping the service releases it; it is started again
// either way, because leaving it down trips the framework watchdog.
func Acquire() (*Player, error) {
	p, err := NewPlayer()
	if err == nil || !errors.Is(err, alsa.ErrBusy) {
		return p, err
	}

	slog.Warn("playback device busy, stopping "+MediaService+" to take it", "err", err)
	if err := prop.Stop(MediaService); err != nil {
		return nil, err
	}
	defer func() {
		if err := prop.Start(MediaService); err != nil {
			slog.Error("restarting "+MediaService+" failed", "err", err)
		}
	}()

	for range acquireAttempts {
		time.Sleep(acquireRetry)

		p, err = NewPlayer()
		if err == nil {
			slog.Info("playback device acquired")
			return p, nil
		}
		if !errors.Is(err, alsa.ErrBusy) {
			return nil, err
		}
	}
	return nil, err
}
