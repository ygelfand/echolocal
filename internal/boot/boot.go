// Package boot brings the device up.
//
// It owns what echod is made of and in what order: which hardware is taken, which parts are
// supervised, and what is only needed once at start-up. The command line's job is to parse flags and
// call Run.
package boot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/component"
	_ "github.com/ygelfand/echolocal/internal/component/all"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/metrics"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/service"
	"github.com/ygelfand/echolocal/internal/update"
)

// Run brings everything up and stays until ctx is cancelled.
//
// Hardware that cannot be taken is logged and left out: a device with no speaker still answers, and
// nothing here is worth refusing to start over.
func Run(ctx context.Context) error {
	slog.Info("echod starting", "version", layout.Version,
		"pid", os.Getpid(), "uid", os.Getuid(), "context", selinuxContext())

	_ = prop.Set(layout.StartedProp, fmt.Sprintf("%.2f", metrics.Uptime()))
	_ = prop.Set(layout.StateProp, "starting")

	// What this process was told, put where everything else reads its settings from, so nothing has to
	// be handed a struct to find out what the device is called or where it listens. Before anything
	// else: the components were built during init and read this the moment they are asked to restore.
	config.Started(config.Device{
		Name: name(),
		Addr: listenAddr(),
	})
	if err := config.LoadError(); err != nil {
		slog.Error("reading the saved config failed, continuing with defaults", "err", err)
	}

	// Before any hardware: an update that was installed and never settled either gets its turn here or
	// sends the device round for another boot, where something outside this binary can put the old one
	// back.
	_, rebooting := update.Start()
	if rebooting {
		slog.Warn("waiting for the reboot rather than taking the hardware")
		return nil
	}

	// The boot hooks are what undoes a bad update, so they are kept current by the running binary rather
	// than only by an install. Writes nothing when nothing differs.
	update.Ensure()

	// restarting is the reason echod is going away, and empty means it is not.
	var restarting string

	// The boot animation runs until Home Assistant has a pipeline listening, which is the point the
	// device can actually answer. It only takes a claim, so it can be asked for before the ring is up.
	startSplash(ctx, led.Get(), voice.Get().Ready)

	// Everything the device remembers, put back in the order the components registered and before
	// anything is listening: how the device behaves is not Home Assistant's business. Silent —
	// restoring is not an event, so nothing chimes and nothing reaches the logbook.
	component.Default().Restore(config.Get())
	slog.Info("state restored")

	group := service.New()
	component.Default().AddTo(group)

	slog.Info("resident")
	_ = prop.Set(layout.StateProp, "resident")

	err := run(ctx, group, &restarting)

	slog.Info("stopping", "restarting", restarting)
	_ = prop.Set(layout.StateProp, "stopped")
	return err
}

// run keeps everything going until the context is done or something asks to be restarted, and reports
// which it was through restarting.
//
// A restart cancels the same context a shutdown does, so the services unwind the way they always do:
// the speaker gating the amplifier before it lets go of the device is what keeps a restart from ending
// in a pop, and there is no separate path here that could forget to do it.
func run(ctx context.Context, group *service.Group, restarting *string) error {
	inner, stop := context.WithCancel(ctx)
	defer stop()

	done := make(chan error, 1)
	go func() { done <- group.Run(inner) }()

	select {
	case err := <-done:
		return err
	case why := <-update.Wanted():
		*restarting = why
		slog.Info("restarting", "why", why)

		// Something is coming back, so the ring says so rather than going dark for the gap.
		led.Get().HoldOnStop(led.UpdateColor)

		stop()
		return <-done
	}
}

// selinuxContext is the domain echod is running in, which decides what it may touch. Logging it makes
// a permission failure obvious: as the ledcontroller service it runs in init's domain, and anywhere
// else it will not reach the hardware.
func selinuxContext() string {
	b, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		return "unknown"
	}
	return strings.TrimRight(string(b), "\x00\n")
}
