// Package firmware is what the device says about its own version, and the channel it follows to
// learn about newer ones.
//
// Home Assistant decides whether an update is worth offering — it compares the two version strings
// itself — and asks for one with a command. All the device does is say what it is running, say what
// it found, and act when told. There is no list of versions in the protocol and no way for Home
// Assistant to ask for a particular one.
package firmware

import (
	"context"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/feedback"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/safe"
	"github.com/ygelfand/echolocal/internal/update"
)

func init() {
	component.Register(component.Network, Get(), component.Order(10))
}

// Event types for the ways an attempt ends.
const (
	EventInstalled  = "installed"
	EventRolledBack = "rolled_back"
	EventFailed     = "failed"
)

type Firmware struct {
	entity  *esphome.Update
	channel *esphome.Select
	look    *esphome.Button
	status  *esphome.TextSensor
	events  *esphome.Event

	mu    sync.Mutex
	found update.Manifest
}

var (
	once   sync.Once
	shared *Firmware
)

func Get() *Firmware {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Firmware {
	u := &Firmware{}

	u.entity = &esphome.Update{
		Base: esphome.Base{
			ObjectID: "firmware",
			Name:     "Firmware",
			Icon:     "mdi:package-up",
		},
		DeviceClass: "firmware",
		OnCommand:   u.command,
	}

	// Up to date until a check says otherwise, rather than an entity with no versions in it.
	u.publish(update.Manifest{Version: layout.Version})

	u.channel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "update_channel",
			Name:     "Update channel",
			Icon:     "mdi:source-branch",
			Category: esphome.CategoryDiagnostic,
		},
	}
	component.Bind(u.channel, update.Channels(),
		func(c update.Channel) update.Channel { return c },
		func(c update.Channel) error { return config.Set().Update().Channel(c.Label()) })

	// Published now, or Home Assistant shows a select with no value until somebody changes it — and a
	// device that has never been asked is on the stable channel, not on nothing.
	u.channel.Set(u.Channel().Label())

	// Home Assistant only asks the device to look when somebody calls homeassistant.update_entity, which
	// is a service call rather than anything on screen. This is that, where it can be found.
	u.look = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "check_for_updates",
			Name:     "Check for updates",
			Icon:     "mdi:cloud-search",
			Category: esphome.CategoryDiagnostic,
		},
		OnPress: func() { safe.Go("update check", func() { u.Check(context.Background()) }) },
	}

	u.status = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "update_status",
			Name:     "Update status",
			Icon:     "mdi:package-variant",
			Category: esphome.CategoryDiagnostic,
		},
	}
	u.status.Set(config.Get().Update.Status)

	u.events = &esphome.Event{
		Base: esphome.Base{
			ObjectID: "update_outcome",
			Name:     "Update outcome",
			Icon:     "mdi:package-up",
		},
		Types: []string{EventInstalled, EventRolledBack, EventFailed},
	}

	// A rollback happened before this process existed, so the boot hook left the version in a property.
	// The event may go nowhere if Home Assistant has not connected yet, which is why the status is saved:
	// that is the part somebody can still find afterwards.
	if was := update.RolledBack(); was != "" {
		u.Settled(EventRolledBack, "rolled back from "+was)
	}

	return u
}

// Settled records how an attempt ended, for a device somebody looks at later and for an automation
// that wants to hear about it now.
func (u *Firmware) Settled(event, status string) {
	u.status.Set(component.Fit(status))
	u.events.Trigger(event)

	if err := config.Set().Update().Status(status); err != nil {
		slog.Error("saving the update status failed", "err", err)
	}
}

// Channel is the stream this device follows, as last chosen.
func (u *Firmware) Channel() update.Channel {
	saved := config.Get().Update.Channel
	if c, ok := config.ByLabel(update.Channels(), saved); ok {
		return c
	}
	return update.Stable
}

// Check looks for something newer and publishes what it found. Nothing is downloaded and nothing is
// installed: Home Assistant reads the versions and decides whether to offer the update.
//
// A fetch that fails leaves the last answer in place, so a device that briefly cannot reach the channel
// keeps reporting what it knew rather than blanking the card.
func (u *Firmware) Check(ctx context.Context) {
	channel := u.Channel()

	found, err := update.Fetch(ctx, channel)
	if err != nil {
		slog.Error("checking for an update failed", "channel", channel.Label(), "err", err)
		return
	}

	u.mu.Lock()
	u.found = found
	u.mu.Unlock()

	slog.Info("update check", "channel", channel.Label(), "running", layout.Version, "offered", found.Version)
	u.publish(found)
}

// command is Home Assistant asking for one of the two things it can ask for. Neither has a reply: what
// the device has to say goes out as entity state.
func (u *Firmware) command(cmd esphome.UpdateCommand) {
	switch cmd {
	case esphome.UpdateCheck:
		safe.Go("update check", func() { u.Check(context.Background()) })

	case esphome.UpdateInstall:
		safe.Go("update install", func() { u.Install(context.Background()) })
	}
}

// Install replaces this binary with what the last check found, and asks for the restart that puts it in
// service. Progress goes to Home Assistant as it downloads, since sixteen megabytes over a satellite's
// wifi is long enough to look stuck.
//
// Nothing is installed that was not offered: the version comes from the manifest this device fetched,
// not from Home Assistant, which has no way to name one.
func (u *Firmware) Install(ctx context.Context) {
	u.mu.Lock()
	found := u.found
	u.mu.Unlock()

	if found.Version == "" || found.Version == layout.Version {
		slog.Warn("an install was asked for with nothing to install", "running", layout.Version)
		return
	}

	// The ring says it is working for the whole of it, which then hands over to the still frame the
	// restart leaves behind — so the device is never silently busy from the moment somebody presses
	// install to the moment the new binary is up.
	working := led.Get().Busy().Start(led.WorkUpdate)
	defer working.Done()

	u.progress(found, 0)
	err := update.Install(ctx, found, func(at float32) { u.progress(found, at) })

	// Home Assistant learns nothing from the command it sent — the update entity has no way to say an
	// install failed, and the card just goes back to offering it. So the failure has to arrive as state,
	// and on the ring for somebody standing in front of the device.
	if err != nil {
		slog.Error("installing an update failed", "version", found.Version, "err", err)
		u.publish(found)
		u.Settled(EventFailed, "installing "+found.Version+" failed: "+err.Error())
		feedback.Failure()
		return
	}
	update.Restart("update to " + found.Version)
}

// progress republishes the state with how far the download has got. The version fields go out with it
// because Home Assistant reads the whole state each time.
func (u *Firmware) progress(found update.Manifest, at float32) {
	state := u.state(found)
	state.InProgress, state.Progress = true, at*100
	u.entity.Set(state)
}

// publish is the state with nothing happening, which is also how a failed install stops looking like one
// that is still running.
func (u *Firmware) publish(found update.Manifest) { u.entity.Set(u.state(found)) }

// state names the channel rather than the version, which Home Assistant already shows twice on its own.
// The channel is the device's, not the release's, so it is not something a manifest could say.
func (u *Firmware) state(found update.Manifest) esphome.UpdateState {
	return esphome.UpdateState{
		CurrentVersion: layout.Version,
		LatestVersion:  found.Version,
		Title:          "EchoLocal (" + u.Channel().Label() + " channel)",
		ReleaseSummary: found.Notes,
		ReleaseURL:     found.ReleaseURL,
	}
}

func (u *Firmware) Name() string { return "firmware" }

func (u *Firmware) Entities() []esphome.Entity {
	return []esphome.Entity{u.entity, u.channel, u.look, u.status, u.events}
}
