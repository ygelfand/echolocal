//go:build !board_biscuit

package settings

import (
	gogui "github.com/go-gui-org/go-gui/gui"
	sharedvolume "github.com/ygelfand/libcountertop/pkg/audio/volume"
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	sharedvisual "github.com/ygelfand/libcountertop/pkg/display/visual"
	"github.com/ygelfand/libcountertop/pkg/say"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/certs"
	"github.com/ygelfand/echolocal/internal/feature/dhcp"
	"github.com/ygelfand/echolocal/internal/feature/frontend"
	"github.com/ygelfand/echolocal/internal/feature/homecontrol"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/network"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/visuals"
	"github.com/ygelfand/echolocal/internal/feature/volume"
	"github.com/ygelfand/echolocal/internal/hardware/wifi"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/ui/visual"
)

func root() *sharedshell.Page {
	return sharedsettings.RootPage(sharedsettings.RootDependencies{
		Network:      wifi.Get(),
		Screen:       config.ScreenSection,
		Basic:        basicPages(),
		Assistants:   assistantPages(),
		Home:         homecontrol.Get(),
		Shell:        shell.Get(),
		SearchQuery:  &Get().SearchQuery,
		Version:      layout.Version,
		Volume:       sharedvolume.Steps(media.Get(), media.VolumeSteps),
		NetworkPage:  func() sharedshell.View { return networkPage() },
		FeaturesPage: func() sharedshell.View { return featuresPage() },
		VolumePage:   func() sharedshell.View { return volume.Page() },
		AboutPage:    func() sharedshell.View { return aboutPage() },
		Sections: func() []sharedsettings.Section {
			return []sharedsettings.Section{
				{
					Label: "settings.camera",
					Glyph: gogui.IconCamera,
					Page:  func() sharedshell.View { return cameraPage() },
				},
				{
					Label: "voice.title",
					Glyph: gogui.IconMegaphone,
					Page:  func() sharedshell.View { return voicePage() },
				},
			}
		},
	})
}

func visualPage() *sharedshell.Page {
	return visualPicker(say.T("debug.visuals"), "", nil, nil, func(k sharedvisual.Kind) {
		visuals.Get().SetKind(k)
		shell.Get().Push(visuals.Get().View())
	})
}

func visualPicker(
	title, empty string,
	none func(),
	chosen func() string,
	pick func(sharedvisual.Kind),
) *sharedshell.Page {
	return sharedsettings.VisualPicker(
		title,
		empty,
		none,
		chosen,
		pick,
		visual.Thumbs().Fit,
		previewKit(),
	)
}

func uiSize() string {
	if s := config.Get().Screen.Size; s != "" {
		return s
	}
	return component.Board().UISize
}

func networkPage() *sharedshell.Page {
	return sharedsettings.NetworkPage(func() sharedsettings.NetworkState {
		c := sharedsettings.NetworkState{
			SSID:    wifi.Get().Network(),
			Address: address(),
			MAC:     wifi.Get().MAC(),
			Verify:  config.Get().Network.Verify,
		}
		if l := dhcp.Get().Lease(); l != nil {
			c.Router = l.Router.String()
			c.Renew = l.Renew
		}
		return c
	}, network.Get().SetVerify, &sharedsettings.WebAccess{
		Admin:       frontend.Get(),
		Certificate: certs.Get(),
	}, shell.Get())
}

func aboutPage() *sharedshell.Page {
	return sharedsettings.AboutPage(func() sharedsettings.About {
		return sharedsettings.About{
			Name:    config.Get().Device.Name,
			Version: layout.Version,
			Built:   layout.BuildDate,
			Commit:  layout.GitCommit,
		}
	})
}

func address() string {
	l := dhcp.Get().Lease()
	if l == nil {
		return say.T("network.address.none")
	}
	return l.Address.IP.String()
}
