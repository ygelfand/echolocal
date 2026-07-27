package echod

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/prop"
)

func newRunCmd() *cobra.Command {
	var (
		splash    time.Duration
		heartbeat time.Duration
		logPath   string
		ringPath  string
	)

	c := &cobra.Command{
		Use:   "run",
		Short: "Run the device agent",
		Long: "Plays the boot animation, then stays resident. echod is installed in place of\n" +
			"Amazon's ledcontroller service, so init starts it from on post-fs-data and\n" +
			"restarts it if it exits, and nothing else drives the ring.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lg := newBootLog(logPath, cmd.ErrOrStderr())
			defer lg.Close()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Launched by init or an adb shell, either of which can go away.
			signal.Ignore(syscall.SIGHUP)

			lg.logf("echod %s starting: pid=%d uid=%d context=%s", Version, os.Getpid(), os.Getuid(), selinuxContext())
			_ = prop.Set("echolocal.state", "starting")

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

			lg.logf("splash: %s", splash)
			_ = prop.Set("echolocal.state", "splash")
			if err := led.Splash(ctx, ring, splash); err != nil {
				lg.logf("splash failed: %v", err)
			}
			if err := ring.Off(); err != nil {
				lg.logf("blanking ring failed: %v", err)
			}

			lg.logf("resident")
			_ = prop.Set("echolocal.state", "resident")
			idle(ctx, lg, heartbeat)

			lg.logf("stopping")
			_ = prop.Set("echolocal.state", "stopped")
			return ring.Off()
		},
	}

	c.Flags().DurationVar(&splash, "splash", 12*time.Second, "boot animation duration")
	c.Flags().DurationVar(&heartbeat, "heartbeat", time.Minute, "liveness log interval, 0 to disable")
	c.Flags().StringVar(&logPath, "log", "/dev/echolocal.log", "log file; /dev is tmpfs, so each boot starts clean")
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

func selinuxContext() string {
	b, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		return "unknown"
	}
	return strings.TrimRight(string(b), "\x00\n")
}

// bootLog writes to a log file and stderr, stamping lines with uptime rather than wall
// clock: echod starts before any time sync, so the clock is not yet trustworthy.
type bootLog struct {
	w io.Writer
	f *os.File
}

func newBootLog(path string, stderr io.Writer) *bootLog {
	l := &bootLog{w: stderr}
	if path == "" {
		return l
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "log: %v\n", err)
		return l
	}
	l.f, l.w = f, io.MultiWriter(stderr, f)
	return l
}

func (l *bootLog) logf(format string, args ...any) {
	fmt.Fprintf(l.w, "[%8.2f] %s\n", uptime(), fmt.Sprintf(format, args...))
}

func (l *bootLog) Close() {
	if l.f != nil {
		_ = l.f.Close()
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
