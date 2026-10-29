package chromecast

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/audio/volume"
	"github.com/ygelfand/libcountertop/pkg/media/cast"
	"github.com/ygelfand/libcountertop/pkg/media/castreceiver"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/system/clock"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/dhcp"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/playout"
	"github.com/ygelfand/echolocal/internal/feature/timesync"
	"github.com/ygelfand/echolocal/internal/feature/web"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
)

func init() {
	component.Register(
		sharedcomponent.Device,
		func() *receiver { return &receiver{Get()} },
		append([]sharedcomponent.Option{sharedcomponent.Order(60)}, needs()...)...,
	)
	web.HandlePeerOpen(
		"GET /setup/*",
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { Get().Setup().ServeHTTP(w, r) },
		),
	)
}

var get = sync.OnceValue(build)

func Get() *castreceiver.Receiver { return get() }

type receiver struct{ *castreceiver.Receiver }

func (r *receiver) Run(ctx context.Context) error {
	if err := firewall.Open(firewall.Cast, cast.Port, cast.SetupPort); err != nil {
		slog.Error("opening the cast ports failed", "err", err)
	}
	return r.Receiver.Run(ctx)
}

func network(ctx context.Context) bool {
	if !component.Board().Has(board.Wifi) {
		return true
	}
	return dhcp.Get().Wait(ctx)
}

func build() *castreceiver.Receiver {
	r := castreceiver.NewReceiver(castreceiver.ReceiverOptions{
		Settings: config.CastSection,
		DeviceID: component.DevicePlayback,
		Network:  network,
		Server: castreceiver.Config{
			Name:            deviceName,
			Manufacturer:    layout.Manufacturer,
			Model:           component.Board().Model,
			Version:         layout.Version,
			Hardware:        hardware,
			Host:            layout.Slug,
			Logo:            logo(),
			Address:         func() string { return config.Get().Network.Address },
			Timezone:        func() string { return timesync.Get().Zone() },
			CredentialsPath: layout.CastCredentialsPath,
			BundlePath:      layout.CastBundlePath,
			AuthorityPath:   layout.CastAuthorityPath,
			AppDir:          layout.CastAppDir,
			Synced: func(do func()) func() {
				return timesync.Get().Synced.Listen(func(clock.Sync) { do() })
			},
			Volume: mediaVolume{},
			Video:  pictures(),
			Output: func(ended func()) playback.Output {
				o := playout.New()
				o.SetEnded(ended)
				return o
			},
			Resample: func(from int) func([]int16) []int16 {
				return resample.NewRational(from, speaker.Rate, speaker.Channels).Run
			},
			Apps:        apps(),
			Notice:      notice(),
			Preferences: castreceiver.Preferences("echolocal", config.CastSection),
		},
	})
	configureSettings(r)
	component.Settings.Add(r.Settings())
	return r
}

type mediaVolume struct{}

func (mediaVolume) Level() int { return media.Get().Levels.Level(config.StreamMedia) }

func (mediaVolume) SetLevel(level int) {
	media.Get().Levels.Set(config.StreamMedia, volume.Clamp(level))
}

func (mediaVolume) Mute(on bool) { media.Get().Levels.Mute(config.StreamMedia, on) }

func (mediaVolume) Muted() bool { return media.Get().Levels.Muted(config.StreamMedia) }

func (mediaVolume) Listen(changed func(int)) func() {
	return media.Get().Levels.Watch(config.StreamMedia, changed)
}

func deviceName() string {
	if name := config.Get().Device.Name; name != "" {
		return name
	}
	return component.Board().DefaultName
}

func hardware() string {
	raw, err := os.ReadFile(layout.MACPath)
	if err != nil {
		slog.Warn("the cast identity has no hardware address to come from", "err", err)
		return ""
	}
	return layout.MAC(string(raw))
}
