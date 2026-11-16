//go:build !board_biscuit

package panel

import (
	"sync"
	"time"

	sharedmatch "github.com/ygelfand/libcountertop/pkg/assistant/voicematch"
	sharedscreen "github.com/ygelfand/libcountertop/pkg/display/assistant"
	"github.com/ygelfand/libcountertop/pkg/display/avatar"
	"github.com/ygelfand/libcountertop/pkg/display/voicematchview"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	ha "github.com/ygelfand/libcountertop/pkg/homeassistant"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/assistant"
	"github.com/ygelfand/echolocal/internal/feature/cameras"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/visuals"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/feature/voicematch"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/gpu"
)

const pictureWait = 15 * time.Second

var People = sync.OnceValue(func() *avatar.Book {
	b := avatar.NewBook(avatar.HomeAssistant{
		Entities: homeassistant.Get(),
		Site:     cameras.Site(),
		Client:   fetch.Client(pictureWait),
	}, shell.Get().Redraw)
	homeassistant.Get().Changed.Listen(func(a ha.Access) {
		if a == ha.Allowed {
			b.Refresh()
		}
	})
	return b
})

var Get = sync.OnceValue(func() *voicematchview.View {
	return voicematchview.New(voicematchview.Options{
		UI:      widgets.NewUI(config.ScreenSection),
		Shell:   shell.Get(),
		Voices:  voicematch.Get(),
		People:  People(),
		Wake:    wake,
		GPU:     gpu.Get(),
		Display: display.Get(),
		Visuals: visuals.Get(),
	})
})

var Follow = sync.OnceFunc(func() {
	voicematch.Get().Identified.Listen(func(i sharedmatch.Identified) {
		if !i.Known {
			return
		}
		People().People()
		p := i.Match.Profile
		assistant.Get().Spoke(sharedscreen.Speaker{ID: p.Who(), Name: p.Name}, i.At)
	})
})

func wake() string {
	t := voice.Get().Turn()
	for slot := range wakeword.Slots {
		if phrase, ok := t.PhraseFor(slot); ok {
			return phrase
		}
	}
	return ""
}
