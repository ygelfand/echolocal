//go:build !board_biscuit

package a2dp

import (
	"sync"
	"time"

	sharedvolume "github.com/ygelfand/libcountertop/pkg/audio/volume"
	sharedspeaker "github.com/ygelfand/libcountertop/pkg/bluetooth/a2dp"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/volume"
	"github.com/ygelfand/echolocal/internal/hardware/ble"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
)

var once sync.Once
var shared *sharedspeaker.Sink

func build() *sharedspeaker.Sink {
	return sharedspeaker.New(sharedspeaker.Dependencies{
		Queue:            speaker.Get().Lane(config.StreamMedia),
		Radio:            ble.Get,
		Bonds:            layout.StateDir + "/bonds",
		CaptureDirectory: "/data/local/tmp",
		Name:             func() string { return config.Get().Device.Name },
		Settings:         config.BluetoothSection,
		Volume:           sharedvolume.For(volume.Get(), config.StreamMedia),
		Media:            media.Get(),
		Arbitration:      speaker.Sound().Backgrounds(),
		Ducking:          config.MediaSection,
	})
}
func Get() *sharedspeaker.Sink {
	once.Do(func() {
		shared = build()
		component.Settings.Add(shared.Controls())
	})
	return shared
}
func init() {
	component.Register(
		sharedcomponent.Device,
		Get,
		sharedcomponent.Order(70),
		component.Needs(board.Panel),
		sharedcomponent.Supervise(service.Restart(5*time.Second, time.Minute)),
	)
}
