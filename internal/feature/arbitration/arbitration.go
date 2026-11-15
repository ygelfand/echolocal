package arbitration

import (
	"log/slog"
	"sync"
	"time"

	sharedarbitration "github.com/ygelfand/libcountertop/pkg/assistant/arbitration"
	sharedpeer "github.com/ygelfand/libcountertop/pkg/network/peer"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/network"
)

var Setting = sharedarbitration.Setting{
	Read: func() schema.Wake { return config.Get().Wake },
	Save: func(v schema.Wake) error {
		return config.WakeSection.Update(func(w *schema.Wake) {
			w.Arbitration, w.ArbitrationOffset = v.Arbitration, v.ArbitrationOffset
		})
	},
}

var Get = sync.OnceValue(func() *sharedarbitration.Arbiter {
	component.Settings.Add(Setting.Controls())
	if err := firewall.Allow(firewall.Arbitration, nil, []int{sharedarbitration.Port}); err != nil {
		slog.Error(
			"opening the arbitration port failed",
			"port",
			sharedarbitration.Port,
			"err",
			err,
		)
	}
	return sharedarbitration.New(sharedarbitration.Options{
		ID:     discovery.ID,
		Offset: Setting.Offset,
		Port:   sharedarbitration.Port,
		Key:    network.Get().HomeKey,
		Peers:  func() []sharedpeer.Peer { return discovery.Get().Peers() },
		Wait:   Setting.Wait,
	})
})

func init() {
	component.Register(sharedcomponent.Network, Get, sharedcomponent.Order(62),
		sharedcomponent.Supervise(service.Restart(5*time.Second, time.Minute)))
}
