package web

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	libweb "github.com/ygelfand/libcountertop/pkg/runtime/web"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/layout"
)

func init() {
	component.Register(
		sharedcomponent.Network,
		func() *server { return &server{Get()} },
		sharedcomponent.Order(50),
	)
}

const (
	Port       = 80
	SecurePort = 8443
	PublicPort = 443
)

var get = sync.OnceValue(func() *libweb.Server {
	return libweb.New(
		libweb.Options{Port: Port, APIPort: layout.Port, Adopted: Adopted},
	)
})

func Get() *libweb.Server { return get() }

type server struct{ *libweb.Server }

func (s *server) Run(ctx context.Context) error {
	if err := firewall.Open(firewall.Web, Port, SecurePort, PublicPort); err != nil {
		slog.Error(
			"opening the web ports failed",
			"ports",
			[]int{Port, SecurePort, PublicPort},
			"err",
			err,
		)
	}
	return s.Server.Run(ctx)
}

func Handle(pattern string, h http.Handler) { Get().Handle(pattern, h) }

func HandlePeer(pattern string, h http.Handler) { Get().HandlePeer(pattern, h) }

func HandlePeerOpen(pattern string, h http.Handler) { Get().HandlePeerOpen(pattern, h) }

func Adopted() bool { return config.Get().API.Adopted }
