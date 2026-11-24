package firmware

import (
	"context"
	"sync"

	"github.com/ygelfand/libcountertop/pkg/network/releases"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/system/firmware"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/feedback"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/update"
)

var get = sync.OnceValue(func() *firmware.Firmware[update.Manifest] {
	f := firmware.New(firmware.Dependencies[update.Manifest]{
		Source:    source{},
		Settings:  settings{},
		Product:   layout.Manufacturer,
		Version:   layout.Version,
		Channels:  config.Labels(update.Channels()),
		Busy:      busy{},
		OnFailure: feedback.Failure,
		Reconnect: func() { component.Reconnect.Emit(struct{}{}) },
		ListPath:  layout.ReleasesPath,
	})
	component.Settings.Add(f.Controls())
	component.Subscribed.Listen(func(struct{}) {
		safe.Go("update announce", f.Announce)
		f.ListLater()
	})
	return f
})

func Get() *firmware.Firmware[update.Manifest] { return get() }

type registered struct {
	*firmware.Firmware[update.Manifest]
}

func (r registered) Restore(config.Config) { r.Firmware.Restore() }

func init() {
	component.Register(
		sharedcomponent.Network,
		func() registered { return registered{Get()} },
		sharedcomponent.Order(10),
	)
}

type source struct{}

func (source) Fetch(ctx context.Context, label string) (update.Manifest, error) {
	c, ok := config.ByLabel(update.Channels(), label)
	if !ok {
		c = update.Stable
	}
	return update.Fetch(ctx, c)
}

func (source) Describe(m update.Manifest) firmware.Release {
	return firmware.Release{Version: m.Version, Notes: m.Notes, ReleaseURL: m.ReleaseURL}
}

func (source) Install(ctx context.Context, m update.Manifest, progress func(float32)) error {
	return update.Install(ctx, m, progress)
}

func (source) Restart(why string) { update.Restart(why) }

func (source) Builds(
	ctx context.Context,
) ([]releases.Release, error) {
	return update.Releases(ctx)
}

func (source) FetchBuild(ctx context.Context, version string) (update.Manifest, error) {
	return update.FetchTag(ctx, version)
}

func (source) RolledBack() string { return update.RolledBack() }

func (source) Commit() { update.Commit() }

type settings struct{}

func (settings) Read() schema.Update        { return config.Get().Update }
func (settings) Channel(v string) error     { return config.Set().Update().Channel(v) }
func (settings) LastVersion(v string) error { return config.Set().Update().LastVersion(v) }

type busy struct{}

func (busy) Begin() firmware.Activity { return led.Get().Busy().Start(led.WorkUpdate) }
