package discovery

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/ygelfand/libcountertop/pkg/assistant/arbitration"
	"github.com/ygelfand/libcountertop/pkg/media/broadcast"
	shared "github.com/ygelfand/libcountertop/pkg/network/discovery"
	sharedpeer "github.com/ygelfand/libcountertop/pkg/network/peer"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/web"
	"github.com/ygelfand/echolocal/internal/layout"
)

type identity struct{}

func (identity) Self() sharedpeer.Peer {
	b := component.Board()
	return sharedpeer.Peer{
		ID:    ID(),
		Name:  config.Get().Device.Name,
		Model: b.Model,
		Board: b.Codename,
		Caps:  caps(),
		Project: strings.ToLower(
			layout.Manufacturer,
		),
		Version:     layout.Version,
		Port:        web.Port,
		Secure:      web.SecurePort,
		Arbitration: arbitrationPort(),
		Host:        layout.Slug(config.Get().Device.Name),
	}
}

func arbitrationPort() int {
	if config.Get().Wake.Arbitration <= 0 {
		return 0
	}
	return arbitration.Port
}

func ID() string {
	mac, err := layout.FactoryMAC()
	if err != nil {
		slog.Warn("no device id", "err", err)
	}
	return strings.ToLower(strings.ReplaceAll(mac, ":", ""))
}
func caps() []string {
	c := []string{"audio"}
	if component.Board().Has(board.Panel) {
		c = append(c, "sync")
	}
	if component.Board().CameraWidth > 0 {
		c = append(c, "video")
	}
	if config.Get().Call.Broadcasts {
		c = append(c, broadcast.Cap)
	}
	return c
}

var get = sync.OnceValue(func() *shared.Discovery { return shared.New(identity{}, judge{}) })

func Get() *shared.Discovery { return get() }
func init() {
	component.Register(
		sharedcomponent.Network,
		Get,
		sharedcomponent.Order(60),
	)
}
