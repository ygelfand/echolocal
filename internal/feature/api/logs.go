package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	proto "github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/android/logd"
)

// waitForReader is how often to look for a client having subscribed. Nothing is lost while waiting,
// so checking often buys nothing.
const waitForReader = time.Second

// pipeLogs sends the device's log to whoever subscribed, which is how it reaches Home Assistant's own
// log. How much arrives is Home Assistant's to decide: it asks for a level.
//
// The lines come by channel because sending them is a socket write, on whatever goroutine logged.
func (a *API) pipeLogs(ctx context.Context) {
	lines := logd.Lines()

	for {
		// Until something is listening there is nowhere to put a line, so they are left in the buffer
		// rather than drained into nothing. What waits there is the most recent of them, which is how
		// Home Assistant gets the log from before it connected — including the boot.
		if !a.srv.LogsSubscribed() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(waitForReader):
			}
			continue
		}

		select {
		case <-ctx.Done():
			return
		case l := <-lines:
			if l.Dropped > 0 {
				a.srv.Log(proto.LogLevel_LOG_LEVEL_WARN,
					fmt.Sprintf("%d log lines dropped", l.Dropped))
			}
			a.srv.Log(logLevel(l.Level), l.Text)
		}
	}
}

func logLevel(l slog.Level) proto.LogLevel {
	switch {
	case l >= slog.LevelError:
		return proto.LogLevel_LOG_LEVEL_ERROR
	case l >= slog.LevelWarn:
		return proto.LogLevel_LOG_LEVEL_WARN
	case l >= slog.LevelInfo:
		return proto.LogLevel_LOG_LEVEL_INFO
	}
	return proto.LogLevel_LOG_LEVEL_DEBUG
}
