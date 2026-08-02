// Package component is what the device is made of.
//
// A component is a part of the device that owns itself: it holds its own hardware or state, says
// what it needs done to it, and carries whatever it shows Home Assistant. Each lives in its own
// package as a singleton and registers itself, so adding one touches one place instead of five.
//
// Only Name is required. Everything else is an optional interface, found by type assertion, so a
// component implements what it actually is — a thing with a loop, a thing with entities, both, or
// neither:
//
//	func init() {
//	    component.Register(component.Hardware, Get(), component.Order(10))
//	}
package component

import (
	"context"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/hook"
	"github.com/ygelfand/echolocal/internal/service"
)

// Phase is when a component comes up. Everything in one phase is up before the next begins, which is
// the only ordering guarantee between packages: hardware exists before anything presents it, and
// nothing is on the network until there is something to talk about.
type Phase int

const (
	// Hardware takes a device off Android: the ring, the speaker, the microphones, the radio.
	Hardware Phase = iota

	// Device is what that hardware becomes for Home Assistant, plus the parts with no hardware of
	// their own, such as the conversation.
	Device

	// Network is the API server, the mDNS advert and anything that reaches outside.
	Network
)

func (p Phase) String() string {
	switch p {
	case Hardware:
		return "hardware"
	case Device:
		return "device"
	case Network:
		return "network"
	}
	return "unknown"
}

// Component is a part of the device.
type Component interface {
	// Name is how it appears in logs and diagnostics.
	Name() string
}

// The optional halves. Starter, Closer and the Run half of service.Service are the supervisor's
// own, so a component with a loop is a service without a shim in between.
type (
	// Starter acquires whatever the component needs, before Run and again on every restart.
	Starter = service.Starter

	// Closer releases it again.
	Closer = service.Closer
)

// Runner is a loop the supervisor keeps alive.
type Runner interface {
	Run(ctx context.Context) error
}

// Entities is what the component shows Home Assistant.
type Entities interface {
	Entities() []esphome.Entity
}

// Handler is a component that answers protocol messages itself rather than through an entity, which
// is the voice satellite and the Bluetooth proxy.
type Handler interface {
	esphome.Handler
}

// Restorer puts the component back the way the device was left, once, at start-up.
type Restorer interface {
	Restore(config.Config)
}

// Sampler publishes whatever drifts on its own. The heartbeat calls these, so they share one
// timestamp rather than each keeping a timer.
type Sampler interface {
	Sample()
}

// Reconnect asks whatever is serving to drop its clients and serve again, which is the only way a
// change to what the device says it is reaches Home Assistant: device info is read once per
// connection and never pushed.
//
// A hook rather than a call, so a component can ask for one without knowing what is listening — the
// server is a component like any other and may not exist.
var Reconnect hook.Hook[struct{}]

// Subscribed fires when Home Assistant asks for state, which is the first moment anything sent to it
// will arrive. Something that happened while the device was alone waits for this.
//
// It fires on every connection, so a listener has to decide for itself whether it still has anything
// to say.
var Subscribed hook.Hook[struct{}]
