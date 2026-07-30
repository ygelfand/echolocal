// Package framework tells the Android framework what echod did to the microphone mute line.
//
// Nothing here moves the line — gpio does that. This exists because Fire OS keeps its own idea of
// the mute state in Settings.Global privacy_status, and HeadlessKeyPolicyManager inside
// system_server re-applies it at BOOT_COMPLETED whether it changed or not, about 23s in, by calling
// AudioManager.setMicrophoneMute. The audio HAL reaches the same pin echod does, so a stale
// privacy_status silently unmutes a device the user left muted, a few seconds after echod restored
// it and with nothing of ours in the log. Keeping the setting in step is what stops that.
//
// On stock the setting is written by amazon.speech.sim, which echod hides, so the writer is gone
// while the part that acts on it lives on in system_server. That asymmetry is the whole reason this
// package exists.
//
// SettingsProvider has no socket protocol to speak the way init's property service does, so the only
// ways in are binder or the command line, and this is not worth a binder client. app_process is
// called directly rather than through /system/bin/settings because that wrapper has no shebang and
// so cannot be execed, only sourced by a shell.
package framework

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	micMuteSetting = "privacy_status"

	runtime   = "/system/bin/app_process"
	class     = "com.android.commands.settings.SettingsCmd"
	classpath = "/system/framework/settings.jar"

	// A write starts a Dalvik VM and costs about 540ms on this hardware. The timeout is for a VM that
	// never comes up rather than one that is slow.
	timeout = 15 * time.Second
)

var (
	mu      sync.Mutex
	pending *bool
	running bool
)

// SetMicMuted records muted as the framework's own mute state, without waiting for it.
//
// Fire and forget because 540ms must not sit in front of the mute button, and coalescing because a
// second press during that window would otherwise race the first: what matters is that the value
// written last is the one the line ended up at, not that every press reaches the setting.
func SetMicMuted(muted bool) {
	mu.Lock()
	defer mu.Unlock()

	pending = &muted
	if running {
		return
	}
	running = true
	go drain()
}

func drain() {
	for {
		mu.Lock()
		if pending == nil {
			running = false
			mu.Unlock()
			return
		}
		muted := *pending
		pending = nil
		mu.Unlock()

		if err := putGlobal(micMuteSetting, value(muted)); err != nil {
			slog.Error("recording mute state for the framework failed", "muted", muted, "err", err)
		}
	}
}

func value(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

func putGlobal(name, v string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, runtime, "/system/bin", class, "put", "global", name, v)
	cmd.Env = append(os.Environ(), "CLASSPATH="+classpath)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s=%s: %w: %s", name, v, err, msg)
		}
		return fmt.Errorf("%s=%s: %w", name, v, err)
	}
	return nil
}
