package echod

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
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
)

func newRunCmd() *cobra.Command {
	var (
		splash    time.Duration
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
			lg := newBootLog(tag, cmd.ErrOrStderr())
			defer lg.Close()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Launched by init or an adb shell, either of which can go away.
			signal.Ignore(syscall.SIGHUP)

			lg.logf("echod %s starting: pid=%d uid=%d context=%s", Version, os.Getpid(), os.Getuid(), selinuxContext())
			_ = prop.Set(layout.StartedProp, fmt.Sprintf("%.2f", uptime()))
			_ = prop.Set(layout.StateProp, "starting")

			ring := &led.Ring{Path: ringPath}

			// The driver animates the ring itself from power-on and keeps repainting over
			// whatever we write, including our blank frames. Amazon's ledcontroller turned this
			// off before driving frames; taking its place means taking that over too.
			if err := ring.SetBootAnimation(false); err != nil {
				lg.logf("disabling driver boot animation failed: %v", err)
			}

			// ledctrl leaves the global drive current at 0, where frame writes are accepted and
			// read back correctly but nothing lights up. 3 is the driver's own default.
			if cur, err := ring.Current(); err != nil {
				lg.logf("reading led_current failed: %v", err)
			} else if cur == 0 {
				lg.logf("led_current is 0, raising to 3")
				if err := ring.SetCurrent(3); err != nil {
					lg.logf("setting led_current failed: %v", err)
				}
			}

			mute, err := gpio.NewMute()
			if err != nil {
				lg.logf("mute unavailable: %v", err)
			}
			muteLED, err := gpio.NewMuteLED()
			if err != nil {
				lg.logf("mute LED unavailable: %v", err)
			} else if err := muteLED.SetBright(true); err != nil {
				lg.logf("setting mute LED bright failed: %v", err)
			}

			sat, err := satellite.New(satellite.Config{
				Name:    satelliteName(name),
				Version: Version,
				Addr:    addr,
				Ring:    ring,
				Mute:    mute,
				MuteLED: muteLED,
				Logger:  slog.New(slog.NewTextHandler(lg.slogWriter(), nil)),
			})
			if err != nil {
				lg.logf("satellite unavailable: %v", err)
			} else {
				go func() {
					lg.logf("serving esphome api on %s", addr)
					if err := sat.Serve(ctx); err != nil && ctx.Err() == nil {
						lg.logf("satellite stopped: %v", err)
					}
				}()
			}

			lg.logf("splash: %s", splash)
			if sat != nil {
				sat.Splash(splash)
			} else {
				go func() {
					if err := led.Splash(ctx, ring, splash); err != nil && ctx.Err() == nil {
						lg.logf("splash failed: %v", err)
					}
				}()
			}

			lg.logf("resident")
			_ = prop.Set(layout.StateProp, "resident")
			idle(ctx, lg, heartbeat)

			lg.logf("stopping")
			_ = prop.Set(layout.StateProp, "stopped")
			return ring.Off()
		},
	}

	c.Flags().DurationVar(&splash, "splash", 2*time.Second, "boot animation duration")
	c.Flags().DurationVar(&heartbeat, "heartbeat", time.Minute, "liveness log interval, 0 to disable")
	c.Flags().StringVar(&tag, "tag", layout.LogTag, "logcat tag")
	c.Flags().StringVar(&name, "name", "", "device name Home Assistant sees; derived from the serial if unset")
	c.Flags().StringVar(&addr, "addr", fmt.Sprintf(":%d", layout.Port), "ESPHome native API listen address")
	c.Flags().StringVar(&ringPath, "ring", led.DefaultPath, "is31fl3236 sysfs directory")
	return c
}

// idle keeps echod resident, logging liveness so a boot can be checked long after it happened.
func idle(ctx context.Context, lg *bootLog, every time.Duration) {
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
			lg.logf("alive")
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

// bootLog writes to logcat and to stderr, stamped with uptime since the wall clock is not
// synced this early.
type bootLog struct {
	stderr io.Writer
	a      *alog.Logger
}

func newBootLog(tag string, stderr io.Writer) *bootLog {
	l := &bootLog{stderr: stderr}
	a, err := alog.New(tag)
	if err != nil {
		fmt.Fprintf(stderr, "logcat unavailable: %v\n", err)
		return l
	}
	l.a = a
	return l
}

func (l *bootLog) logf(format string, args ...any) {
	msg := fmt.Sprintf("[%8.2f] %s", uptime(), fmt.Sprintf(format, args...))
	fmt.Fprintln(l.stderr, msg)
	if l.a != nil {
		_ = l.a.Infof("%s", msg)
	}
}

// slogWriter feeds slog output to the same places as our own lines.
func (l *bootLog) slogWriter() io.Writer {
	if l.a == nil {
		return l.stderr
	}
	return io.MultiWriter(l.stderr, l.a.Writer(alog.Info))
}

func (l *bootLog) Close() {
	if l.a != nil {
		_ = l.a.Close()
	}
}

func uptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(string(b), " ")
	v, _ := strconv.ParseFloat(first, 64)
	return v
}
