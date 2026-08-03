// Package config is everything the device is set to, and the one place that decides it.
//
// Reading takes no lock and carries no default:
//
//	c := config.Get()
//	c.Speaker.Volume
//
// Writing names the thing being changed, and persists it:
//
//	config.Set().Speaker().Volume(8)
//
// Each part of the device gets a file here holding its own struct, its defaults and its writer, so
// a setting and everything about it is in one place. Nothing distinguishes "never set" from "set to
// the default": loading starts from the defaults and lets the file write over what it mentions.
//
// The values the user can choose between are named here too, as closed sets of identifiers with a
// label — the identifier is written to the file and branched on, the label is what Home Assistant
// shows. Keeping both here means persistence does not import the subsystem a setting belongs to,
// and the subsystem does not import persistence.
package config

import "fmt"

// Config is what the device is set to.
type Config struct {
	Device     Device     `json:"-"`
	Speaker    Speaker    `json:"speaker"`
	Microphone Microphone `json:"microphone"`
	Wake       Wake       `json:"wake"`
	Ring       Ring       `json:"ring"`
	Update     Update     `json:"update"`
	Bluetooth  Bluetooth  `json:"bluetooth"`
	Diag       Diag       `json:"diag"`
	Media      Media      `json:"media"`
}

// Defaults is a device nobody has set anything on.
func Defaults() Config {
	return Config{
		Speaker:    defaultSpeaker(),
		Microphone: defaultMicrophone(),
		Ring:       defaultRing(),
		Update:     defaultUpdate(),
		Bluetooth:  defaultBluetooth(),
		Diag:       defaultDiag(),
		Media:      defaultMedia(),

		// Only the stop word. The slots are absent until something chooses one, and Slot fills in the
		// defaults for whichever have not been.
		Wake: Wake{Stop: defaultStop()},
	}
}

// Device is what echod was told at start-up rather than what anyone chose. It is read like
// everything else, and not written to the file: the next process is told again.
//
// Nothing here is readable until boot has called Started, so a component built during init must not
// reach for it.
type Device struct {
	Name string
	Addr string
}

// Writer is what Set hands back: one method per part of the device, each with its own settings.
//
// Nothing here holds a lock. The leaf call does the whole thing — take the lock, change the value,
// write the file — and returns whether the file was written.
type Writer struct{ st *Store }

func (w Writer) Speaker() SpeakerWriter       { return SpeakerWriter(w) }
func (w Writer) Microphone() MicrophoneWriter { return MicrophoneWriter(w) }
func (w Writer) Ring() RingWriter             { return RingWriter(w) }
func (w Writer) Update() UpdateWriter         { return UpdateWriter(w) }
func (w Writer) Bluetooth() BluetoothWriter   { return BluetoothWriter(w) }
func (w Writer) Diag() DiagWriter             { return DiagWriter(w) }
func (w Writer) Media() MediaWriter           { return MediaWriter(w) }

// Wake names one slot, since every wake word setting belongs to one.
func (w Writer) Wake(slot int) WakeWriter { return WakeWriter{st: w.st, slot: slot} }

// Stop is the stop word, which is not a slot.
func (w Writer) Stop() StopWriter { return StopWriter(w) }

func errSlot(n int) error { return fmt.Errorf("config: wake slot %d", n) }

// Labelled is a setting whose values name themselves. The entity layer binds any of these to a
// select without knowing which setting it is.
type Labelled interface{ Label() string }

// Labels is the list Home Assistant shows for a set of values, in the order given.
func Labels[T Labelled](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Label())
	}
	return out
}

// ByLabel resolves what Home Assistant sent back to the value it names. A select speaks labels,
// everything else speaks values, and this is the one place the two meet.
func ByLabel[T Labelled](values []T, label string) (T, bool) {
	for _, v := range values {
		if v.Label() == label {
			return v, true
		}
	}
	var zero T
	return zero, false
}
