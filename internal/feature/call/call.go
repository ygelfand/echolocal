package call

import (
	"sync"

	sharedcall "github.com/ygelfand/libcountertop/pkg/media/call"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/detect"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/tones"
	"github.com/ygelfand/echolocal/internal/feature/web"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
)

type componentCall struct{ *sharedcall.Calls }

func (c *componentCall) Restore(v config.Config) { c.Calls.Restore(v.Call) }

var once sync.Once
var shared *sharedcall.Calls

func Get() *sharedcall.Calls {
	once.Do(func() {
		d := sharedcall.Dependencies{
			Settings: config.CallSection,
			Peers:    discovery.Get(),
			Port:     web.Port,
			Secure:   web.SecurePort,
			Client:   discovery.Client(),
			Media:    media.Get(),
			Sound:    device{},
			Tones:    tones.Get(),
			Home:     homeassistant.Get(),
		}
		front(&d)
		shared = sharedcall.New(d)
		configureSettings(shared)
		component.Settings.Add(shared.Controls())
	})
	return shared
}

func init() {
	component.Register(
		sharedcomponent.Network,
		func() *componentCall { return &componentCall{Get()} },
		sharedcomponent.Order(62),
	)
	web.HandlePeer("POST /call/offer", discovery.Home(Get().OfferHandler()))
	web.HandlePeer("POST /call/answer", discovery.Home(Get().AnswerHandler()))
	web.HandlePeer("POST /call/end", discovery.Home(Get().EndHandler()))
	// TODO: remove the plain-HTTP call routes once no device predates the secure port.
	web.Handle("POST /call/offer", discovery.Legacy(Get().OfferHandler()))
	web.Handle("POST /call/answer", discovery.Legacy(Get().AnswerHandler()))
	web.Handle("POST /call/end", discovery.Legacy(Get().EndHandler()))
	detect.PauseWhen(Get().PausesWake)
	buttons.Get().Take(take)
}

func take(e buttons.Event) bool {
	if e.Kind != buttons.Tap {
		return false
	}
	if c, ok := Get().Now(); !ok || c.State != sharedcall.Ringing {
		return false
	}
	switch e.Name {
	case buttons.Action:
		Get().AnswerAudio()
	case buttons.VolumeDown:
		Get().Hangup()
	default:
		return false
	}
	return true
}
