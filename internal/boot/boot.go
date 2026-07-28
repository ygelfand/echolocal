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
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/prop"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/service"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
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

	if err := settings.LoadError(); err != nil {
		slog.Error("reading saved settings failed, continuing with defaults", "err", err)
	}

	ring := prepareRing()
	defer func() {
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
		Mic:     source,
	})
	if err != nil {
		slog.Error("satellite unavailable", "err", err)
	} else {
		ready.Store(sat)
		addSatellite(group, sat, source)
	}

	group.Add(heartbeat{})

	slog.Info("resident")
	_ = prop.Set(layout.StateProp, "resident")

	err = group.Run(ctx)

	slog.Info("stopping")
	_ = prop.Set(layout.StateProp, "stopped")
	return err
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
