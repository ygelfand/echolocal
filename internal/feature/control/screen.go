//go:build !board_biscuit

package control

import (
	"context"

	sharedapps "github.com/ygelfand/libcountertop/pkg/display/apps"
	"github.com/ygelfand/libcountertop/pkg/inference/asr"
	harness "github.com/ygelfand/libcountertop/pkg/runtime/control"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/btcmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/castcmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/displaycmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/doorbellcmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/homealarmcmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/homecmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/screencmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/speechcmd"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/thermostatcmd"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/a2dp"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/chromecast"
	"github.com/ygelfand/echolocal/internal/feature/doorbell"
	"github.com/ygelfand/echolocal/internal/feature/homealarm"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/message"
	"github.com/ygelfand/echolocal/internal/feature/nws"
	"github.com/ygelfand/echolocal/internal/feature/poster"
	"github.com/ygelfand/echolocal/internal/feature/thermostat"
	"github.com/ygelfand/echolocal/internal/feature/timer"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/gpu"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/parts"
)

func showing(t *harness.Tree) {
	t.Add(harness.DisplayTask, displaycmd.Commands(displaycmd.Dependencies{
		Panel:    display.Get,
		GPU:      gpu.Get,
		Messages: message.Get,
		Label:    config.Defaults().Visual.Label,
		Shot:     layout.StateDir + "/screen.png",
		Thumbs:   "/data/local/tmp/echolocal-thumbs",
	})...)

	open, stack, ready := panelCommands()
	t.Add(
		harness.DisplayTask,
		screencmd.Record(display.Get),
		open,
		stack,
		screencmd.Timer(timer.Get),
		screencmd.Warning(nws.Get),
		screencmd.Wallpaper(poster.Get),
		screencmd.App(func() []sharedapps.App { return apps.Get().List() }),
	)
	t.Add(harness.DeviceTask, ready)
	t.Add(harness.AudioTask, speechcmd.Command(func(ctx context.Context) (asr.Options, error) {
		return asr.Prepare(ctx, layout.SpeechHelper, parts.Speech(), layout.ModelDir)
	}))
	t.Top(
		btcmd.Command(a2dp.Get(), "echolocal"),
		callCommand(),
		cameraCommand(),
		doorbellcmd.Command(doorbell.Get),
		homealarmcmd.Command(homealarm.Get, homealarm.Shown),
		thermostatcmd.Command(thermostat.Get, thermostat.Shown),
		castcmd.Command(chromecast.Page),
	)
	t.Top(homecmd.Commands(homeassistant.Get())...)
}
