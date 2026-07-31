package satellite

import (
	"context"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/alog"
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
type updater struct {
	entity  *esphome.Update
	channel *esphome.Select
	look    *esphome.Button

	// running is what this build is, which never changes while it is the one running.
	running string

	mu    sync.Mutex
	found update.Manifest
}

func newUpdater(version string) *updater {
	u := &updater{running: version}

	u.entity = &esphome.Update{
		Base: esphome.Base{
			ObjectID: "firmware",
			Name:     "Firmware",
			Icon:     "mdi:package-up",
		},
		DeviceClass: "firmware",
		OnCommand:   u.command,
	}
	u.entity.Set(esphome.UpdateState{CurrentVersion: version, LatestVersion: version})

	u.channel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "update_channel",
			Name:     "Update channel",
			Icon:     "mdi:source-branch",
			Category: esphome.CategoryConfig,
		},
	}
	bind(u.channel, update.Channels(),
		func(c update.Channel) update.Channel { return c },
		func(c update.Channel) error { return settings.SetUpdateChannel(c.Label()) })

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

	return u
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

	found, err := update.Fetch(ctx, channel.URL())
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

	u.progress(found, 0)
	err := update.Install(ctx, found, func(at float32) { u.progress(found, at) })

	if err != nil {
		slog.Error("installing an update failed", "version", found.Version, "err", err)
		u.publish(found)
		return
	}
	update.Restart("update to " + found.Version)
}

// progress republishes the state with how far the download has got. The version fields go out with it
// because Home Assistant reads the whole state each time.
func (u *updater) progress(found update.Manifest, at float32) {
	u.entity.Set(esphome.UpdateState{
		CurrentVersion: u.running,
		LatestVersion:  found.Version,
		Title:          found.Title,
		ReleaseSummary: found.Notes,
		ReleaseURL:     found.ReleaseURL,
		InProgress:     true,
		Progress:       at * 100,
	})
}

// publish is the state with nothing happening, which is also how a failed install stops looking like one
// that is still running.
func (u *updater) publish(found update.Manifest) {
	u.entity.Set(esphome.UpdateState{
		CurrentVersion: u.running,
		LatestVersion:  found.Version,
		Title:          found.Title,
		ReleaseSummary: found.Notes,
		ReleaseURL:     found.ReleaseURL,
	})
}

func (u *updater) entities() []esphome.Entity {
	return []esphome.Entity{u.entity, u.channel, u.look}
}
