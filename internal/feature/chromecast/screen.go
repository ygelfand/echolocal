//go:build !board_biscuit

package chromecast

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/display/surface"
	"github.com/ygelfand/libcountertop/pkg/display/video"
	"github.com/ygelfand/libcountertop/pkg/media/cast"
	"github.com/ygelfand/libcountertop/pkg/media/cast/engine"
	"github.com/ygelfand/libcountertop/pkg/media/cast/protocols/webreceiver"
	"github.com/ygelfand/libcountertop/pkg/media/castreceiver"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/castcmd"

	"github.com/ygelfand/echolocal/internal/android/dns"
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/dhcp"
	"github.com/ygelfand/echolocal/internal/feature/message"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/ui"
)

func init() {
	component.Register(
		sharedcomponent.Device,
		Engine,
		append([]sharedcomponent.Option{sharedcomponent.Order(59)}, needs()...)...,
	)
}

func configureSettings(r *castreceiver.Receiver) {
	r.Settings().Table = r.Settings().Table.Without("youtube.lounge")
}

func needs() []sharedcomponent.Option {
	return []sharedcomponent.Option{component.Needs(board.Panel)}
}

func helper() *surface.Client { return display.Get().Helper() }

func notice() func(title, body string) {
	return func(title, body string) { message.Get().Quiet(title, body, castreceiver.NoticeFor) }
}

var tuning = sync.OnceValue(func() *engine.Tuning {
	t := engine.NewTuning(config.DiagSection)
	component.Settings.Add(t.Controls())
	return t
})

func Tuning() *engine.Tuning { return tuning() }

var host = sync.OnceValue(func() *engine.Host {
	return engine.NewHost(engine.HostOptions{
		Dir:         layout.CastEngineDir,
		State:       layout.CastEngineState,
		Args:        webreceiver.EngineArgs(),
		Nameservers: nameservers,
		Network:     network,
		Screen: func() (int, int) {
			t := pictures()()
			return t.Width, t.Height
		},
		Tuning: Tuning(),
	})
})

func Engine() *engine.Host { return host() }

var page castPage

type castPage struct {
	mu sync.Mutex
	r  *webreceiver.Receiver
}

func (p *castPage) keep(r *webreceiver.Receiver) {
	p.mu.Lock()
	p.r = r
	p.mu.Unlock()
}

func Page() castcmd.Page {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.r == nil {
		return nil
	}
	return page.r
}

func apps() []cast.Maker {
	return []cast.Maker{
		webreceiver.Maker(webreceiver.Options{
			Engine: Engine(),
			Helper: helper,
			Built:  page.keep,
		}),
	}
}

func nameservers() []string {
	if l := dhcp.Get().Lease(); l != nil && len(l.DNS) > 0 {
		return l.Nameservers()
	}
	return dns.Nameservers()
}

func pictures() func() playback.Target {
	return func() playback.Target {
		return video.Target(display.Get(), component.Board().MaxFPS)
	}
}

func logo() func() []byte { return ui.Logo().PNG }
