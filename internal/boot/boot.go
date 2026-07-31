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
	"sync/atomic"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/dns"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/prop"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/service"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/setup"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/update"
)

// Config is what the device needs to know that it cannot work out for itself.
type Config struct {
	// Name is what Home Assistant calls the device. Empty takes the name recorded at install.
	Name string

	// Version is the build, reported to Home Assistant and logged.
	Version string
}

// Run brings everything up and stays until ctx is cancelled.
//
// Hardware that cannot be taken is logged and left out: a device with no speaker still answers, and
// nothing here is worth refusing to start over.
func Run(ctx context.Context, cfg Config) error {
	slog.Info("echod starting", "version", cfg.Version,
		"pid", os.Getpid(), "uid", os.Getuid(), "context", selinuxContext())

	_ = prop.Set(layout.StartedProp, fmt.Sprintf("%.2f", alog.Uptime()))
	_ = prop.Set(layout.StateProp, "starting")

	dns.Use()

	if err := settings.LoadError(); err != nil {
		slog.Error("reading saved settings failed, continuing with defaults", "err", err)
	}

	setup.Apply()
	go alog.Safely("late setup", func() { setup.ApplyLate(ctx) })

	// Before any hardware: an update that was installed and never settled either gets its turn here or
	// sends the device round for another boot, where something outside this binary can put the old one
	// back. A rollback that already happened is read for the same reason — nobody watches logcat.
	onTrial, rebooting := update.Start()
	update.RolledBack()
	if rebooting {
		slog.Warn("waiting for the reboot rather than taking the hardware")
		return nil
	}

	// The boot hooks are what undoes a bad update, so they are kept current by the running binary rather
	// than only by an install. Writes nothing when nothing differs.
	update.Ensure()

	// restarting is the reason echod is going away, and empty means it is not. It decides what the ring
	// is left showing: nothing, or a colour saying the device is coming back.
	var restarting string

	ring := prepareRing()
	defer func() {
		if restarting != "" {
			// Deliberately left lit. Nothing is running to animate anything once this returns, and a
			// still frame is held by the hardware, so the device says what it is doing for the whole
			// gap rather than looking dead.
			if err := ring.SetSegments(led.Solid(led.UpdateColor)); err != nil {
				slog.Error("holding the ring failed", "err", err)
			}
			return
		}
		if err := ring.Off(); err != nil {
			slog.Error("blanking the ring failed", "err", err)
		}
	}()

	group := service.New()

	leds := led.NewDriver(ring)
	group.Add(leds, forever())

	// The boot animation runs until Home Assistant has a pipeline listening, which is the point the
	// device can actually answer. The satellite does not exist yet, so readiness is asked through a
	// pointer filled in once it does.
	var ready atomic.Pointer[satellite.Satellite]
	startSplash(ctx, leds, func() bool {
		s := ready.Load()
		return s != nil && s.PipelineReady()
	})

	mute, muteLED := takeMute()

	// The audio devices are held for the life of the process: whatever is free, Android takes. The
	// handles are made here and the services take the hardware, so a device lost to Android can be
	// taken back on a restart without everything holding a handle being rebuilt.
	//
	// The speaker also feeds silence while idle, because the amplifier hisses when nothing drives the
	// DAC and toggling it pops.
	spk := speaker.New()
	group.Add(spk, forever())
	sound := speaker.NewDriver(spk)

	source := mic.New()
	group.Add(source, forever())

	sat, err := satellite.New(satellite.Config{
		Name:    name(cfg.Name),
		Version: cfg.Version,
		Addr:    listenAddr(),
		Ring:    leds,
		Mute:    mute,
		MuteLED: muteLED,
		Speaker: spk,
		Sound:   sound,
		Mic:     source,
	})
	if err != nil {
		slog.Error("satellite unavailable", "err", err)
	} else {
		ready.Store(sat)
		addSatellite(group, sat, source, leds)
	}

	// The heartbeat samples what drifts, so a device with no satellite still beats and simply has
	// nothing to publish.
	beat := heartbeat{}
	if sat := ready.Load(); sat != nil {
		beat.sample = sat.Sample
	}

	// An update on trial is shown while it is on trial, and kept once this process has reached a beat.
	// Reaching one is not much of a test, but it is the only evidence there is, and it is enough to tell
	// a binary that runs from one that dies on the way up.
	if onTrial {
		held := leds.Busy().Start(led.WorkCommit)
		beat.settled = func() {
			update.Commit()
			held.Done()
			beat.settled = nil
		}
	}
	group.Add(beat)

	slog.Info("resident")
	_ = prop.Set(layout.StateProp, "resident")

	err = run(ctx, group, &restarting)

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
