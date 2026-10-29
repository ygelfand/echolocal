package control

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	harness "github.com/ygelfand/libcountertop/pkg/runtime/control"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/alarmcmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/blecmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/firmwarecmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/i2ccmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/mixercmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/playercmd"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/feature/alarm"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/firmware"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/hardware/ble"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/logging"
)

func (c *Control) tree() *cobra.Command {
	t := harness.NewTree()
	t.Add(harness.DeviceTask, telling()...)
	t.Add(harness.DeviceTask, harness.WaitCommand())
	t.Add(harness.DeviceTask, harness.PeersCommand(discovery.Get().Peers), i2ccmd.Command())
	t.Add(harness.DeviceTask, alarmcmd.Command(alarm.Get))
	t.Add(harness.InputTask, c.inputEngine().Commands()...)
	t.Add(harness.AudioTask, hearing()...)
	t.Add(harness.AudioTask, cancelling()...)
	t.Add(harness.AudioTask, mixercmd.Command(speaker.Card))
	t.Add(harness.AudioTask, playercmd.Command(func() playercmd.Player { return media.Get() }))
	t.Top(setCommand())
	t.Top(firmwarecmd.Command(firmware.Get))
	t.Top(blecmd.Command(ble.Get()))
	showing(t)
	return t.Root()
}

func telling() []*cobra.Command {
	commands := harness.DeviceCommands(deviceInfo)

	logs := &cobra.Command{
		Use:       "log [debug|info|warn|error]",
		Short:     "Read or change how much goes to logcat, until echod restarts",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"debug", "info", "warn", "error"},
		RunE: func(cmd *cobra.Command, args []string) error {
			level, err := harness.LogLevel(&logging.Log.Level, args)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				priority := "I"
				if logging.Log.Level.Level() < slog.LevelInfo {
					priority = "D"
				}
				if err := prop.Set("log.tag."+layout.LogTag, priority); err != nil {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), level)
			return nil
		},
	}

	return append(commands, logs)
}
