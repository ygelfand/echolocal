//go:build !board_biscuit

package settings

import (
	"github.com/ygelfand/libcountertop/pkg/assistant/turn"
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedha "github.com/ygelfand/libcountertop/pkg/homeassistant"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/detect"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/tones"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func assistantPages() *sharedsettings.AssistantPages {
	mac := func() string {
		mac, _ := layout.FactoryMAC()
		return mac
	}
	return sharedsettings.NewAssistantPages(
		sharedsettings.AssistantDependencies{
			Slots:       wakeword.Slots,
			Pryon:       detect.Get().Pryon(),
			PryonStop:   detect.Get().Pryon().Stop(),
			Gemini:      detect.Get().Gemini(),
			Preferences: preferences(),
			Models:      wake.Lib().Ours,
			Phrases:     wake.Lib().Phrases,
			Chooser:     sharedha.WakeWords{Controller: homeassistant.Get(), MAC: mac},
			Pipelines:   sharedha.Pipelines{Controller: homeassistant.Get(), MAC: mac},
			Requested:   &wakeword.Requested,
			Words: turn.Words{
				Settings: config.WakeSection,
				Speaker:  speaker.Sound(),
				Sounds:   tones.Get(),
			},
			Look:  lookPages(),
			Shell: shell.Get(),
		},
	)
}
