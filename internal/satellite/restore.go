package satellite

import (
	"log/slog"

	"github.com/ygelfand/echolocal/internal/settings"
)

// restore puts the device back the way it was left, once, at start-up.
//
// It is one pass rather than each piece restoring itself as it is built, because the order matters and
// construction order is not it: the hardware has to be where it was before anything shows what it is
// doing, and what follows the microphones or the light's colour needs both of those settled first.
//
// Everything here is silent. Restoring is not an event: nothing chimes, nothing reaches the logbook,
// and nothing writes back what it just read — a write at start-up is a write on the path most likely to
// be interrupted by another restart. The exception is a stored name this build no longer has, which is
// corrected in the file as well as in the entity, or it would be tried again every time.
//
// Every step says what it applied and whether it came from the file or from a default. Without that a
// device that comes up wrong gives no clue which piece decided that.
func restore(k *kit, mute *muteSwitch, opts *options, room *roomReaction) {
	saved := settings.Get()

	// The hardware first, so the device behaves as it was left whether or not Home Assistant is ever
	// reachable: how the microphones are mixed and levelled, how loud it is, whether it is cut.
	if opts != nil {
		opts.restore(saved)
	}
	if k.Player != nil {
		k.Player.restore(saved)
	}
	if mute != nil {
		mute.restore(saved)
	}

	// Then what is shown, which depends on the above: the room reaction inherits the light's colour, and
	// the ring cannot say the microphones are cut before it knows that they are.
	if k.Ring != nil {
		k.Ring.restore(saved.Ring)
	}
	if room != nil {
		room.restore(saved.Ring)
	}

	// Last, because a wake word slot is a pipeline's worth of settings and nothing else depends on it.
	if k.Wake != nil {
		k.Wake.restoreSlots()
	}

	// What the disk holds, which the slots decide: a model no slot wants is cache.
	if k.Diag != nil {
		k.Diag.measure()
	}

	slog.Info("state restored")
}

// from names where a value came from, for the log: a setting that has never been touched reads as a
// default rather than as something the user chose.
func from(stored bool) string {
	if stored {
		return "file"
	}
	return "default"
}
