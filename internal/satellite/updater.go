package satellite

import (
	"context"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/update"
)

// updater is what the device says about its own version, and the channel it follows to learn about
// newer ones.
//
// Home Assistant decides whether an update is worth offering — it compares the two version strings
// itself — and asks for one with a command. All the device does is say what it is running, say what it
// found, and act when told. There is no list of versions in the protocol and no way for Home Assistant
// to ask for a particular one.
// Event types for the ways an attempt ends.
const (
	EventInstalled  = "installed"
	EventRolledBack = "rolled_back"
	EventFailed     = "failed"
)

type updater struct {
	entity  *esphome.Update
	channel *esphome.Select
	look    *esphome.Button
	status  *esphome.TextSensor
	events  *esphome.Event
	leds    *led.Driver

	// running is what this build is, which never changes while it is the one running.
	running string

	mu    sync.Mutex
	found update.Manifest
}

func newUpdater(k *kit, version string) *updater {
	u := &updater{running: version, leds: k.LEDs}

	u.entity = &esphome.Update{
		Base: esphome.Base{
			ObjectID: "firmware",
			Name:     "Firmware",
			Icon:     "mdi:package-up",
		},
		DeviceClass: "firmware",
		OnCommand:   u.command,
	}
	u.publish(update.Manifest{Version: version})

	u.channel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "update_channel",
			Name:     "Update channel",
			Icon:     "mdi:source-branch",
			Category: esphome.CategoryDiagnostic,
		},
	}
	bind(u.channel, update.Channels(),
		func(c update.Channel) update.Channel { return c },
		func(c update.Channel) error { return settings.SetUpdateChannel(c.Label()) })

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
		OnPress: func() { go alog.Safely("update check", func() { u.Check(context.Background()) }) },
	}

	u.status = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "update_status",
			Name:     "Update status",
			Icon:     "mdi:package-variant",
			Category: esphome.CategoryDiagnostic,
		},
	}
	u.status.Set(settings.Get().Update.StatusOr("never updated"))

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
		u.settled(EventRolledBack, "rolled back from "+was)
	}

	return u
}

// settled records how an attempt ended, for a device somebody looks at later and for an automation that
// wants to hear about it now.
func (u *updater) settled(event, status string) {
	u.status.Set(status)
	u.events.Trigger(event)

	if err := settings.SetUpdateStatus(status); err != nil {
		slog.Error("saving the update status failed", "err", err)
	}
}

// Channel is the stream this device follows, as last chosen.
func (u *updater) Channel() update.Channel {
	saved := settings.Get().Update.ChannelOr(update.Stable.Label())
	if c, ok := settings.ByLabel(update.Channels(), saved); ok {
		return c
	}
	return update.Stable
}

// Check looks for something newer and publishes what it found. Nothing is downloaded and nothing is
// installed: Home Assistant reads the versions and decides whether to offer the update.
//
// A fetch that fails leaves the last answer in place, so a device that briefly cannot reach the channel
// keeps reporting what it knew rather than blanking the card.
func (u *updater) Check(ctx context.Context) {
	channel := u.Channel()

	found, err := update.Fetch(ctx, channel)
	if err != nil {
		slog.Error("checking for an update failed", "channel", channel.Label(), "err", err)
		return
	}

	u.mu.Lock()
	u.found = found
	u.mu.Unlock()

	slog.Info("update check", "channel", channel.Label(), "running", u.running, "offered", found.Version)
	u.publish(found)
}

// command is Home Assistant asking for one of the two things it can ask for. Neither has a reply: what
// the device has to say goes out as entity state.
func (u *updater) command(cmd esphome.UpdateCommand) {
	switch cmd {
	case esphome.UpdateCheck:
		go alog.Safely("update check", func() { u.Check(context.Background()) })

	case esphome.UpdateInstall:
		go alog.Safely("update install", func() { u.Install(context.Background()) })
	}
}

// Install replaces this binary with what the last check found, and asks for the restart that puts it in
// service. Progress goes to Home Assistant as it downloads, since sixteen megabytes over a satellite's
// wifi is long enough to look stuck.
//
// Nothing is installed that was not offered: the version comes from the manifest this device fetched,
// not from Home Assistant, which has no way to name one.
func (u *updater) Install(ctx context.Context) {
	u.mu.Lock()
	found := u.found
	u.mu.Unlock()

	if found.Version == "" || found.Version == u.running {
		slog.Warn("an install was asked for with nothing to install", "running", u.running)
		return
	}

	// The ring says it is working for the whole of it, which then hands over to the still frame the
	// restart leaves behind — so the device is never silently busy from the moment somebody presses
	// install to the moment the new binary is up.
	working := u.leds.Busy().Start(led.WorkUpdate)
	defer working.Done()

	u.progress(found, 0)
	err := update.Install(ctx, found, func(at float32) { u.progress(found, at) })

	// Home Assistant learns nothing from the command it sent — the update entity has no way to say an
	// install failed, and the card just goes back to offering it. So the failure has to arrive as state,
	// and on the ring for somebody standing in front of the device.
	if err != nil {
		slog.Error("installing an update failed", "version", found.Version, "err", err)
		u.publish(found)
		u.settled(EventFailed, "installing "+found.Version+" failed: "+err.Error())
		troubleRing(u.leds)
		return
	}
	update.Restart("update to " + found.Version)
}

// progress republishes the state with how far the download has got. The version fields go out with it
// because Home Assistant reads the whole state each time.
func (u *updater) progress(found update.Manifest, at float32) {
	state := u.state(found)
	state.InProgress, state.Progress = true, at*100
	u.entity.Set(state)
}

// publish is the state with nothing happening, which is also how a failed install stops looking like one
// that is still running.
func (u *updater) publish(found update.Manifest) { u.entity.Set(u.state(found)) }

// state names the channel rather than the version, which Home Assistant already shows twice on its own.
// The channel is the device's, not the release's, so it is not something a manifest could say.
func (u *updater) state(found update.Manifest) esphome.UpdateState {
	return esphome.UpdateState{
		CurrentVersion: u.running,
		LatestVersion:  found.Version,
		Title:          "EchoLocal (" + u.Channel().Label() + " channel)",
		ReleaseSummary: found.Notes,
		ReleaseURL:     found.ReleaseURL,
	}
}

func (u *updater) entities() []esphome.Entity {
	return []esphome.Entity{u.entity, u.channel, u.look, u.status, u.events}
}
