package boot

import (
	"context"

	"github.com/ygelfand/echolocal/internal/buttons"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/service"
)

// addSatellite registers what Home Assistant drives, once there is a satellite to drive it.
func addSatellite(group *service.Group, sat *satellite.Satellite, source *mic.Source) {
	// Buttons come first: they are the one part of the device that should work whatever else is wrong,
	// so they must not be downstream of a network listener or lost to one read error.
	group.Add(buttons.New(sat.OnButton), forever())

	group.Add(runner{name: "conversation", run: func(ctx context.Context) error {
		sat.RunConversation(ctx)
		return nil
	}}, forever())

	// Detection comes up before the API, so Home Assistant cannot read the wake words while they are
	// still loading and be told about one that then fails. Nothing installed is not fatal: the device
	// works, it just cannot be woken.
	if source != nil {
		addWake(group, sat, source)
	}

	group.Add(runner{name: "logs", run: sat.PipeLogs}, forever())
	group.Add(runner{name: "api", run: sat.Serve}, forever())
}
