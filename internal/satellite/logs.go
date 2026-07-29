package satellite

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/alog"
)

// waitForReader is how often to look for a client having subscribed. Nothing is lost while waiting,
// so checking often buys nothing.
const waitForReader = time.Second

// PipeLogs sends the device's log to whoever subscribed over the API, which is how it reaches Home
// Assistant's own log. How much arrives is Home Assistant's to decide: it asks for a level.
//
// The lines come by channel because sending them is a socket write, on whatever goroutine logged.
func (s *Satellite) PipeLogs(ctx context.Context) error {
	lines := alog.Lines()

	for {
		// Until something is listening there is nowhere to put a line, so they are left in the buffer
		// rather than drained into nothing. What waits there is the most recent of them, which is how
		// Home Assistant gets the log from before it connected — including the boot.
		if !s.srv.LogsSubscribed() {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(waitForReader):
			}
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case l := <-lines:
			if l.Dropped > 0 {
				s.srv.Log(api.LogLevel_LOG_LEVEL_WARN,
					fmt.Sprintf("%d log lines dropped", l.Dropped))
			}
			s.srv.Log(logLevel(l.Level), l.Text)
		}
	}
}

func logLevel(l slog.Level) api.LogLevel {
	switch {
	case l >= slog.LevelError:
		return api.LogLevel_LOG_LEVEL_ERROR
	case l >= slog.LevelWarn:
		return api.LogLevel_LOG_LEVEL_WARN
	case l >= slog.LevelInfo:
		return api.LogLevel_LOG_LEVEL_INFO
	}
	return api.LogLevel_LOG_LEVEL_DEBUG
}
