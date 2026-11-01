package network

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/network/policy"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
)

func init() {
	component.Register(sharedcomponent.Network, Get, sharedcomponent.Order(5))
}

type Network struct{ *policy.Policy }

var get = sync.OnceValue(func() *Network {
	n := &Network{policy.New(config.NetworkSection)}
	n.DownloadThrough(homeassistant.Get().Downloads())
	component.Settings.Add(n.Settings())
	return n
})

func Get() *Network                          { return get() }
func (n *Network) Restore(cfg config.Config) { n.Policy.Restore(cfg.Network.Verify) }
