package boot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ygelfand/libcountertop/pkg/runtime/crash"
	"github.com/ygelfand/libcountertop/pkg/runtime/oom"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"
	"github.com/ygelfand/libcountertop/pkg/system/metrics"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	_ "github.com/ygelfand/echolocal/internal/component/all"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/defaults"
	"github.com/ygelfand/echolocal/internal/feature/firmware"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/parts"
	"github.com/ygelfand/echolocal/internal/update"
)

func Run(ctx context.Context) error {
	b := board.Detect()

	crash.Keep(layout.CrashPath)
	oom.Settle()

	slog.Info("echod starting", "version", layout.Version, "board", b,
		"pid", os.Getpid(), "uid", os.Getuid(), "context", selinuxContext())

	_ = prop.Set(layout.StartedProp, fmt.Sprintf("%.2f", metrics.Reader{}.Uptime().Value))
	_ = prop.Set(layout.StateProp, "starting")

	component.Default().Use(b)
	defaults.Use(b)
	update.Use(b.Codename)

	config.Started(config.Device{
		Name:  name(b),
		Addr:  listenAddr(),
		Board: b,
	})
	if err := config.LoadError(); err != nil {
		slog.Error("reading the saved config failed, continuing with defaults", "err", err)
	}

	_, rebooting := update.Start()
	if rebooting {
		slog.Warn("waiting for the reboot rather than taking the hardware")
		return nil
	}

	update.Ensure()
	firmware.Get().RebootPending(parts.Ensure())

	var restarting string

	startSplash(ctx, led.Get(), voice.Get().Ready)

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

		led.Get().HoldOnStop(led.UpdateColor)

		stop()
		return <-done
	}
}

func selinuxContext() string {
	b, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		return "unknown"
	}
	return strings.TrimRight(string(b), "\x00\n")
}
