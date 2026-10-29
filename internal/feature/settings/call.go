//go:build !board_biscuit

package settings

import (
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/call"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func callsPage() *sharedshell.Page { return callPages().Calls() }

func callPages() *sharedsettings.CallPages {
	return sharedsettings.NewCallPages(
		sharedsettings.CallDependencies{
			Settings:   config.CallSection,
			Shell:      shell.Get(),
			Controller: call.Get(),
			Peers:      discovery.Get().Peers,
			Home:       homeassistant.Get(),
		},
	)
}
