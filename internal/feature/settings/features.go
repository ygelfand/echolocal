//go:build !board_biscuit

package settings

import (
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/say"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/a2dp"
	"github.com/ygelfand/echolocal/internal/feature/airplay"
	"github.com/ygelfand/echolocal/internal/feature/bluetooth"
	"github.com/ygelfand/echolocal/internal/feature/sendspin"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func featuresPage() *sharedshell.Page {
	return sharedsettings.FeaturesPage(func() []sharedsettings.Feature {
		c := config.Get()
		return []sharedsettings.Feature{
			{Key: "features.sendspin", On: c.Sendspin.Enabled, Set: sendspin.Get().SetEnabled},
			{Key: "features.cast", Page: func() sharedshell.View { return castPage() }},
			{Key: "features.airplay", Page: func() sharedshell.View { return airplayPage() }},
			{Key: "call.settings", Page: func() sharedshell.View { return callsPage() }},
			{Key: "features.proxy", On: c.Bluetooth.Proxy, Set: bluetooth.Get().SetProxy},
			{Key: "features.speaker", On: c.Bluetooth.Speaker, Set: a2dp.Get().SetEnabled},
			{Key: "features.rtsp", Page: func() sharedshell.View { return streamPage() }},
		}
	}, shell.Get().Push)
}

func castPage() *sharedshell.Page { return servicePages().Cast() }

func airplayPage() *sharedshell.Page {
	return sharedsettings.ServicePage(say.T("features.airplay"), airplay.Get().Settings())
}
