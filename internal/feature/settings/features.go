//go:build !board_biscuit

package settings

import (
	"github.com/ygelfand/libcountertop/pkg/display/plexapp"
	sharedrecipes "github.com/ygelfand/libcountertop/pkg/display/recipes"
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/say"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/a2dp"
	"github.com/ygelfand/echolocal/internal/feature/airplay"
	"github.com/ygelfand/echolocal/internal/feature/bluetooth"
	"github.com/ygelfand/echolocal/internal/feature/chromecast"
	"github.com/ygelfand/echolocal/internal/feature/plex"
	"github.com/ygelfand/echolocal/internal/feature/recipes"
	"github.com/ygelfand/echolocal/internal/feature/sendspin"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func featuresPage() *sharedshell.Page {
	return sharedsettings.FeaturesPage(func() []sharedsettings.Feature {
		c := config.Get()
		return []sharedsettings.Feature{
			{Key: "features.sendspin", On: c.Sendspin.Enabled, Set: sendspin.Get().SetEnabled},
			{Key: "features.cast", On: c.Cast.Receiver, Set: chromecast.Get().SetReceiver},
			{Key: "features.airplay", Page: func() sharedshell.View { return airplayPage() }},
			{Key: "call.settings", Page: func() sharedshell.View { return callsPage() }},
			{Key: "features.proxy", On: c.Bluetooth.Proxy, Set: bluetooth.Get().SetProxy},
			{
				Key: "features.speaker",
				Page: func() sharedshell.View {
					return sharedsettings.BluetoothSpeakerPage(a2dp.Get())
				},
			},
			{Key: "features.rtsp", Page: func() sharedshell.View { return streamPage() }},
			{
				Key: "features.mealie",
				Page: func() sharedshell.View {
					return sharedrecipes.SettingsPage(recipes.Get())
				},
			},
			{
				Key: "features.plex",
				Page: func() sharedshell.View {
					return plexapp.SettingsPage(plex.Get(), shell.Get())
				},
			},
		}
	}, shell.Get().Push)
}

func airplayPage() *sharedshell.Page {
	return sharedsettings.ServicePage(say.T("features.airplay"), airplay.Get().Settings())
}
