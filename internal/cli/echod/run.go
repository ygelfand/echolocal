package echod

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/prop"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/speaker"
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

			// The ring spins from here until everything is up.
			splashCtx, online := context.WithCancel(ctx)
			defer online()
			go func() {
				if err := led.Splash(splashCtx, ring); err != nil && ctx.Err() == nil {
					slog.Error("splash failed", "err", err)
				}
			}()

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
				go func() {
					if err := spk.Run(ctx); err != nil && ctx.Err() == nil {
						slog.Error("speaker stopped", "err", err)
					}
				}()
			}

			sat, err := satellite.New(satellite.Config{
				Name:    satelliteName(name),
				Version: Version,
				Addr:    addr,
				Ring:    ring,
				Mute:    mute,
				MuteLED: muteLED,
				Speaker: spk,
			})
			if err != nil {
				slog.Error("satellite unavailable", "err", err)
			} else {
				go func() {
					slog.Info("serving esphome api", "addr", addr)
					if err := sat.Serve(ctx); err != nil && ctx.Err() == nil {
						slog.Error("satellite stopped", "err", err)
					}
				}()
			}


			online()
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

