//go:build !board_biscuit

package settings

import (
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/syncview"

	"github.com/ygelfand/echolocal/internal/feature/access"
	"github.com/ygelfand/echolocal/internal/feature/alerts"
	"github.com/ygelfand/echolocal/internal/feature/chromecast"
	"github.com/ygelfand/echolocal/internal/feature/dashboard"
	"github.com/ygelfand/echolocal/internal/feature/firmware"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/homecontrol"
	"github.com/ygelfand/echolocal/internal/feature/lock"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/network"
	"github.com/ygelfand/echolocal/internal/feature/photos"
	"github.com/ygelfand/echolocal/internal/feature/poster"
	"github.com/ygelfand/echolocal/internal/feature/privacy"
	"github.com/ygelfand/echolocal/internal/feature/screen"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/timesync"
	"github.com/ygelfand/echolocal/internal/feature/visuals"
)

func Visuals() *sharedsettings.BasicPages { return basicPages() }

func basicPages() *sharedsettings.BasicPages {
	return sharedsettings.NewBasicPages(
		sharedsettings.BasicDependencies{
			Preferences: preferences(),
			Shell:       shell.Get(),
			Screen:      screen.Get(),
			Poster:      poster.Get(),
			Photos:      photos.Get(),
			Visuals:     visuals.Get(),
			Engine:      chromecast.Tuning(),
			Privacy:     privacy.Get(),
			Access:      access.Get(),
			Zone:        timesync.Get(),
			Dashboard:   dashboard.Get(),
			Clocks:      clocks,
			Idle:        idlePages(),
			UISize:      uiSize,
			Home:        homecontrol.Get(),
			HA:          homeassistant.Get(),
			Marks:       modeMarks(),
			Alerts:      alerts.Get(),
			Updates:     firmware.Get(),
			Lock:        lock.Get().Gate(),
			HomeKey:     network.Get(),
			Media:       media.Get(),
			Player:      media.Card(),
			Previews:    previewKit(),
			PowerPage:   func() sharedshell.View { return powerPage() },
			SyncPage:    func() sharedshell.View { return &syncview.Directory{} },
			VisualPage:  func() sharedshell.View { return visualPage() },
		},
	)
}
