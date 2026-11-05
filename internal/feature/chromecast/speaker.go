//go:build board_biscuit

package chromecast

import (
	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/libcountertop/pkg/media/cast"
	castpreferences "github.com/ygelfand/libcountertop/pkg/media/cast/preferences"
	"github.com/ygelfand/libcountertop/pkg/media/cast/protocols/youtube"
	"github.com/ygelfand/libcountertop/pkg/media/castreceiver"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
)

func init() {
	component.Register(sharedcomponent.Device, newLounge, sharedcomponent.Order(61))
}

type lounge struct {
	knobs *setting.Controls[schema.Lounge]
}

func newLounge() *lounge {
	l := &lounge{knobs: &setting.Controls[schema.Lounge]{
		Table:  youtube.NewTable(),
		Device: component.DevicePlayback,
		Read:   config.LoungeSection.Read,
		Save: func(s setting.Setting[schema.Lounge], v string) error {
			if err := setting.Store(config.LoungeSection, s, v); err != nil {
				return err
			}
			Get().Restart()
			return nil
		},
	}}
	component.Settings.Add(l.knobs)
	return l
}

func (l *lounge) Name() string { return "youtube lounge" }

func (l *lounge) Entities() []esphome.Entity { return l.knobs.Entities() }

func needs() []sharedcomponent.Option { return nil }

func configureSettings(r *castreceiver.Receiver) {
	r.Settings().Table = r.Settings().Table.Without("youtube.skip")
}

func preferences() castpreferences.Provider {
	return youtube.Preferences("echolocal", config.CastSection, config.LoungeSection)
}

func apps() []cast.Maker { return []cast.Maker{youtube.Maker} }

func pictures() func() playback.Target { return nil }

func notice() func(title, body string) { return nil }

func logo() func() []byte { return nil }
