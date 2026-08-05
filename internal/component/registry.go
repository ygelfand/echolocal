package component

import (
	"cmp"
	"context"
	"slices"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/service"
)

// Registry is everything registered, in the order it happens.
//
// Order is declared, never inferred. Components register from init, and Go decides that order from
// the import graph — which is not something the ring coming up before the splash should depend on.
// So entries sort by phase, then by a declared Order, then by name, and the result is the same
// whatever the linker did.
type Registry struct {
	mu      sync.Mutex
	entries []*entry
}

type entry struct {
	c     Component
	phase Phase
	order int
	opts  []service.Option
}

// Option adjusts one registration.
type Option func(*entry)

// Order places a component within its phase. Lower comes first; equal orders fall back to the name.
func Order(n int) Option { return func(e *entry) { e.order = n } }

// Supervise passes the policy through to the supervisor, for a component with a loop.
func Supervise(opts ...service.Option) Option {
	return func(e *entry) { e.opts = append(e.opts, opts...) }
}

// New is an empty registry. There is a process-wide one for components to register into; this is
// for tests, which want their own.
func New() *Registry { return &Registry{} }

var shared = New()

// Register adds to the process-wide registry, from a component package's init.
func Register(p Phase, c Component, opts ...Option) { shared.Add(p, c, opts...) }

// Default is the process-wide registry.
func Default() *Registry { return shared }

// Add registers a component.
func (r *Registry) Add(p Phase, c Component, opts ...Option) {
	e := &entry{c: c, phase: p}
	for _, o := range opts {
		o(e)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// All is every component, in the order they come up.
func (r *Registry) All() []Component {
	out := make([]Component, 0, len(r.sorted()))
	for _, e := range r.sorted() {
		out = append(out, e.c)
	}
	return out
}

// Entities is everything the components show Home Assistant.
func (r *Registry) Entities() []esphome.Entity {
	var out []esphome.Entity
	for _, e := range r.sorted() {
		if ents, ok := e.c.(Entities); ok {
			out = append(out, ents.Entities()...)
		}
	}
	return out
}

// Actions is everything the components let Home Assistant call.
func (r *Registry) Actions() []*esphome.Action {
	var out []*esphome.Action
	for _, e := range r.sorted() {
		if acts, ok := e.c.(Actions); ok {
			out = append(out, acts.Actions()...)
		}
	}
	return out
}

// Handlers is the components that answer protocol messages themselves.
func (r *Registry) Handlers() []esphome.Handler {
	var out []esphome.Handler
	for _, e := range r.sorted() {
		if h, ok := e.c.(Handler); ok {
			out = append(out, h)
		}
	}
	return out
}

// Restore puts every component back the way the device was left. Order matters and is the
// registration order: the hardware has to be where it was before anything shows what it is doing.
func (r *Registry) Restore(c config.Config) {
	for _, e := range r.sorted() {
		if v, ok := e.c.(Restorer); ok {
			v.Restore(c)
		}
	}
}

func (r *Registry) Group() *service.Group {
	g := service.New()
	r.AddTo(g)
	return g
}

// AddTo hands the components to a supervisor, in order. It adds rather than owning the group, because
// the group starts in add order and there is still hand-wired work either side.
//
// A component with no loop is added too, as long as it has something to start or close: plenty of
// what a device does at boot happens once and still belongs in the ordered list.
func (r *Registry) AddTo(g *service.Group) {
	for _, e := range r.sorted() {
		if svc, ok := e.c.(service.Service); ok {
			g.Add(svc, e.opts...)
			continue
		}

		_, starts := e.c.(Starter)
		_, closes := e.c.(Closer)
		if !starts && !closes {
			continue
		}
		g.Add(oneshot{e.c}, append(slices.Clone(e.opts), service.Once())...)
	}
}

// oneshot gives a component with no loop the Run the supervisor needs. Returning nil immediately is
// how service.Once reads a service that finished rather than one that died.
//
// Start and Close are forwarded by hand: embedding a Component promotes only the methods of that
// interface, so the wrapper would otherwise hide the very halves it exists to run.
type oneshot struct{ Component }

func (oneshot) Run(context.Context) error { return nil }

func (o oneshot) Start(ctx context.Context) error {
	if s, ok := o.Component.(Starter); ok {
		return s.Start(ctx)
	}
	return nil
}

func (o oneshot) Close() error {
	if c, ok := o.Component.(Closer); ok {
		return c.Close()
	}
	return nil
}

// sorted is the entries in the order everything walks them.
func (r *Registry) sorted() []*entry {
	r.mu.Lock()
	out := slices.Clone(r.entries)
	r.mu.Unlock()

	slices.SortStableFunc(out, func(a, b *entry) int {
		if v := cmp.Compare(a.phase, b.phase); v != 0 {
			return v
		}
		if v := cmp.Compare(a.order, b.order); v != 0 {
			return v
		}
		return cmp.Compare(a.c.Name(), b.c.Name())
	})
	return out
}
