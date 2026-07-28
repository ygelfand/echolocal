package echod

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/prop"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/state"
	"github.com/ygelfand/echolocal/internal/wake"
)

func newRunCmd() *cobra.Command {
	var (
		heartbeat time.Duration
		tag       string
		ringPath  string
		name      string
		addr      string
	)

	c := &cobra.Command{
		Use:   "run",
		Short: "Run the device agent",
		Long: "Plays the boot animation, then stays resident. echod is installed in place of\n" +
			"Amazon's ledcontroller service, so init starts it from on post-fs-data and\n" +
			"restarts it if it exits, and nothing else drives the ring.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := alog.NewHandler(tag, cmd.ErrOrStderr())
			defer h.Close()
			slog.SetDefault(slog.New(h))

			// init discards our stderr, so a panic would otherwise vanish and look like a silent
			// restart. Log it, then let it kill the process as it would have.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic", "panic", r, "stack", string(debug.Stack()))
					h.Close()
					panic(r)
				}
			}()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Launched by init or an adb shell, either of which can go away.
			signal.Ignore(syscall.SIGHUP)

			slog.Info("echod starting", "version", Version, "pid", os.Getpid(), "uid", os.Getuid(), "context", selinuxContext())

			_ = prop.Set(layout.StartedProp, fmt.Sprintf("%.2f", alog.Uptime()))
			_ = prop.Set(layout.StateProp, "starting")

			ring := &led.Ring{Path: ringPath}

			// The driver animates the ring itself from power-on and keeps repainting over
			// whatever we write, including our blank frames. Amazon's ledcontroller turned this
			// off before driving frames; taking its place means taking that over too.
			if err := ring.SetBootAnimation(false); err != nil {
				slog.Error("disabling driver boot animation failed", "err", err)
			}

			// ledctrl leaves the global drive current at 0, where frame writes are accepted and
			// read back correctly but nothing lights up. 3 is the driver's own default.
			if cur, err := ring.Current(); err != nil {
				slog.Error("reading led_current failed", "err", err)
			} else if cur == 0 {
				slog.Info("led_current is 0, raising to 3")
				if err := ring.SetCurrent(3); err != nil {
					slog.Error("setting led_current failed", "err", err)
				}
			}

			// The ring spins from here until Home Assistant has a pipeline listening, which is the
			// point the device can actually answer. The satellite does not exist yet, so readiness
			// is asked through a pointer that is filled in once it does.
			var ready atomic.Pointer[satellite.Satellite]

			splashCtx, online := context.WithCancel(ctx)
			defer online()
			go alog.Safely("splash", func() {
				linked := func() bool {
					s := ready.Load()
					return s != nil && s.PipelineReady()
				}
				if err := led.Splash(splashCtx, ring, linked); err != nil && ctx.Err() == nil {
					slog.Error("splash failed", "err", err)
				}
				// The ring is the light entity's from here.
				if s := ready.Load(); s != nil {
					s.ReleaseRing()
				}
			})

			mute, err := gpio.NewMute()
			if err != nil {
				slog.Error("mute unavailable", "err", err)
			}
			muteLED, err := gpio.NewMuteLED()
			if err != nil {
				slog.Error("mute LED unavailable", "err", err)
			} else if err := muteLED.SetBright(true); err != nil {
				slog.Error("setting mute LED bright failed", "err", err)
			}

			// The speaker holds its stream open for the life of the process and feeds silence
			// when idle: the amplifier hisses when nothing drives the DAC, and toggling it
			// pops. Closing it turns the amplifier back off, so every exit path matters.
			spk, err := speaker.Acquire()
			if err != nil {
				slog.Error("speaker unavailable", "err", err)
			} else {
				defer spk.Close()
				go alog.Safely("speaker", func() {
					if err := spk.Run(ctx); err != nil && ctx.Err() == nil {
						slog.Error("speaker stopped", "err", err)
					}
				})
			}

			// The array is held for the life of the process for the same reason as the speaker:
			// whatever is free, Android takes.
			source, err := mic.Acquire()
			if err != nil {
				slog.Error("microphones unavailable", "err", err)
			} else {
				defer source.Close()
				go alog.Safely("capture", func() {
					if err := source.Run(ctx); err != nil && ctx.Err() == nil {
						slog.Error("capture stopped", "err", err)
					}
				})
			}

			sat, err := satellite.New(satellite.Config{
				Name:    satelliteName(name),
				Version: Version,
				Addr:    addr,
				Ring:    ring,
				Mute:    mute,
				MuteLED: muteLED,
				Speaker: spk,
				Mic:     source,
			})
			if err != nil {
				slog.Error("satellite unavailable", "err", err)
			} else {
				ready.Store(sat)
				go alog.Safely("satellite", func() {
					slog.Info("serving esphome api", "addr", addr)
					if err := sat.Serve(ctx); err != nil && ctx.Err() == nil {
						slog.Error("satellite stopped", "err", err)
					}
				})
			}

			// No model installed is not fatal: the device works, it just cannot be woken.
			if source != nil && sat != nil {
				startWake(ctx, source, sat)
			}

			// The ring keeps stepping until Home Assistant subscribes, which is a different thing
			// from the process being up, so it is not stopped here.
			slog.Info("resident")
			_ = prop.Set(layout.StateProp, "resident")
			idle(ctx, heartbeat)

			slog.Info("stopping")
			_ = prop.Set(layout.StateProp, "stopped")
			return ring.Off()
		},
	}

	c.Flags().DurationVar(&heartbeat, "heartbeat", time.Minute, "liveness log interval, 0 to disable")
	c.Flags().StringVar(&tag, "tag", layout.LogTag, "logcat tag")
	c.Flags().StringVar(&name, "name", "", "device name Home Assistant sees; derived from the serial if unset")
	c.Flags().StringVar(&addr, "addr", fmt.Sprintf(":%d", layout.Port), "ESPHome native API listen address")
	c.Flags().StringVar(&ringPath, "ring", led.DefaultPath, "is31fl3236 sysfs directory")
	return c
}

// idle keeps echod resident, logging liveness so a boot can be checked long after it happened.
func idle(ctx context.Context, every time.Duration) {
	if every <= 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			slog.Info("alive")
		}
	}
}

// startWake runs detection on whichever installed model Home Assistant last selected. Nothing
// installed means the device simply cannot be woken; everything else still works.
func startWake(ctx context.Context, source *mic.Source, sat *satellite.Satellite) {
	models, err := wake.Installed(layout.ModelDir)
	if err != nil {
		slog.Warn("no wake word models", "dir", layout.ModelDir, "err", err)
		return
	}

	model := wake.Pick(models, state.Get().Settings.Wake.WordID())
	engine, err := wake.New(model)
	if err != nil {
		slog.Error("loading wake word failed", "model", model.ID, "err", err)
		return
	}

	engine.Enabled = sat.WakeEnabled
	engine.Cutoff = sat.WakeSensitivity
	engine.OnDetect = sat.WakeDetected
	go engine.Run(ctx, source)

	// Home Assistant owns the choice, and it takes effect without a restart.
	sat.OnWakeWord(func(id string) error {
		return engine.Use(wake.Pick(models, id))
	})

	slog.Info("wake word ready", "phrase", model.Phrase, "id", model.ID, "installed", len(models))
}

// satelliteName prefers the name echoctl recorded at install, since Home Assistant keys the
// device on it.
func satelliteName(override string) string {
	if override != "" {
		return override
	}
	if b, err := os.ReadFile(layout.NamePath); err == nil {
		if name := strings.TrimSpace(string(b)); name != "" {
			return name
		}
	}

	b, err := os.ReadFile(layout.MACPath)
	if err != nil {
		return layout.DefaultName
	}
	return layout.NameFromMAC(string(b))
}

func selinuxContext() string {
	b, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		return "unknown"
	}
	return strings.TrimRight(string(b), "\x00\n")
}
