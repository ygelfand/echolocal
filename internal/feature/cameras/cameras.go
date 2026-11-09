//go:build !board_biscuit

package cameras

import (
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/display/cameraview"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	sharedha "github.com/ygelfand/libcountertop/pkg/homeassistant"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/dashboard"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/homecontrol"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

const fetchWait = 15 * time.Second

func init() {
	component.Register(sharedcomponent.Network, Site, component.Needs(board.Panel))
	component.Register(
		sharedcomponent.Device,
		func() *Cameras { return &Cameras{Get()} },
		component.Needs(board.Panel),
	)
}

var Site = sync.OnceValue(func() *sharedha.Site {
	return &sharedha.Site{Override: func() string { return config.Get().Cameras.URL }}
})

var Get = sync.OnceValue(func() *cameraview.Cameras {
	c := cameraview.New(cameraview.Options{
		Shell:    shell.Get(),
		Source:   homeassistant.Get(),
		Streams:  homeassistant.Get(),
		Speaker:  sound{},
		Display:  display.Get(),
		Site:     Site(),
		Settings: config.CamerasSection,
		Client:   fetch.Client(fetchWait),
		Picker:   picker,
		Tabs:     dashboard.Tabs(),
	})
	dashboard.Tabs().Add(20, c.DashboardTabs)
	component.Settings.Add(c.Controls())
	return c
})

type Cameras struct{ *cameraview.Cameras }

type sound struct{}

func (sound) Play(pcm []int16) { speaker.Get().PlayStream(config.StreamMedia, pcm) }

func (sound) Queued() int { return speaker.Get().Lane(config.StreamMedia).Queued() }

func (c *Cameras) Name() string { return "cameras" }

func (c *Cameras) Entities() []esphome.Entity { return c.Controls().Entities() }

func (c *Cameras) Restore(cfg config.Config) { c.Controls().PublishFrom(cfg.Cameras) }

func picker() sharedshell.View {
	e := homecontrol.Get()
	return e.NewPicker(e.Choice(
		"cameras",
		sharedha.Filter{Domains: []string{"camera"}},
		func() schema.HomePick { return config.Get().Cameras.Pick },
		func(p schema.HomePick) error {
			return config.CamerasSection.Update(func(c *schema.Cameras) { c.Pick = p })
		},
	))
}
