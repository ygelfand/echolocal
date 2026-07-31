package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ygelfand/echolocal/internal/layout"
)

// Stored is what echod remembers across restarts. Config is separate and read-only: it lives in
// /system/etc/echolocal/echod.yaml and nothing here overrides it.
//
// Pointers mark a setting that has never been touched, so a default wins over a zero value. Read
// them through the Or accessors rather than directly.
type Stored struct {
	Speaker    Speaker    `json:"speaker,omitzero"`
	Microphone Microphone `json:"microphone,omitzero"`
	Wake       Wake       `json:"wake,omitzero"`
	Ring       Ring       `json:"ring,omitzero"`
	Update     Update     `json:"update,omitzero"`
}

// Update is which releases the device follows. The channel is a name rather than a URL: where each one
// points is compiled in, so this cannot be used to send the device somewhere else for its next binary.
type Update struct {
	Channel *string `json:"channel,omitempty"`
}

// ChannelOr is the stored channel, or fallback when nothing has been chosen.
func (u Update) ChannelOr(fallback string) string {
	if u.Channel == nil {
		return fallback
	}
	return *u.Channel
}

// Stored reports whether a channel was ever chosen, so a restore can tell "never set" from "set to the
// default".
func (u Update) Stored() bool { return u.Channel != nil }

// Ring is the light, apart from what Home Assistant holds for the light entity itself. Each of these
// is an animation by name, or empty for none, and none of them are appearances the light was set to:
// they outlive it being switched off and come back with it.
type Ring struct {
	// Reaction is the animation that follows the room.
	Reaction *string `json:"reaction,omitempty"`

	// Trouble is what a failure shows, and Muted what a cut microphone shows for as long as it is cut.
	Trouble *string `json:"trouble,omitempty"`
	Muted   *string `json:"muted,omitempty"`

	// Light is the appearance Home Assistant last set, so a restart comes back the way it was left.
	Light Light `json:"light,omitzero"`
}

// Light is the ring light's own state. It is one thing rather than six settings: an appearance is
// chosen all at once, so it is saved and restored all at once.
//
// Off unless it was on. Nothing stored is a ring that stays dark, which is what a device nobody has
// asked for anything yet should do, and what ESPHome defaults its own lights to for the same reason.
type Light struct {
	On         *bool    `json:"on,omitempty"`
	Brightness *float32 `json:"brightness,omitempty"`
	Red        *float32 `json:"red,omitempty"`
	Green      *float32 `json:"green,omitempty"`
	Blue       *float32 `json:"blue,omitempty"`

	// Effect is the animation by name, empty for a plain colour.
	Effect *string `json:"effect,omitempty"`
}

type Speaker struct {
	Volume *int `json:"volume,omitempty"`

	// Resampling is how voice is stretched to the playback rate.
	Resampling *Resampling `json:"resampling,omitempty"`
}

type Microphone struct {
	Muted     *bool `json:"muted,omitempty"`
	LEDBright *bool `json:"led_bright,omitempty"`

	// Gain is the analog gain on the array's converters, in dB.
	Gain *int `json:"gain,omitempty"`

	// Leveling brings the mix up to the level recognition expects.
	Leveling *bool `json:"leveling,omitempty"`

	// Mixing is how the seven microphones are combined. Which one wins depends on the room.
	Mixing *Mixing `json:"mixing,omitempty"`
}

// Wake is the wake word configuration, indexed by Home Assistant's wake word slot.
type Wake struct {
	Words []WakeWord `json:"slots,omitempty"`
}

// WakeWord is one slot: which wake word listens there and how it behaves when it fires. An empty ID
// is the slot switched off, which is also how detection is turned off altogether — there is no
// separate switch for it.
type WakeWord struct {
	ID *string `json:"id,omitempty"`

	// Threshold is the score a detection has to reach. Per word, because models disagree on scale.
	Threshold *float64 `json:"threshold,omitempty"`

	Tone   *Tone   `json:"tone,omitempty"`
	Effect *string `json:"effect,omitempty"`

	// Delivery is how the reply from this slot's pipeline reaches the device.
	Delivery *Delivery `json:"delivery,omitempty"`

	// FollowUp is seconds to listen after a reply, zero to only do it when Home Assistant asks.
	FollowUp *int `json:"follow_up,omitempty"`

	// Buffer is milliseconds of a streamed reply to collect before playing any of it.
	Buffer *int `json:"buffer,omitempty"`

	// Seconds before giving up. Listening holds the microphone open and Home Assistant normally ends
	// it, so that one is a backstop; thinking holds only the ring, and a model can take a minute.
	MaxListen *int `json:"max_listen,omitempty"`
	MaxThink  *int `json:"max_think,omitempty"`
}

// The process-wide store, at layout.StatePath. There is one device and one file, so callers use the
// package functions rather than being handed a Store.
var (
	once   sync.Once
	shared *Store
)

func store() *Store {
	once.Do(func() {
		var err error
		shared, err = Load(layout.StatePath)
		if err != nil {
			loadErr = err
		}
	})
	return shared
}

// loadErr is whatever the first load reported, for callers that want to log it.
var loadErr error

// Get returns the current settings, loading them on first use.
func Get() Stored { return store().Get() }

// LoadError reports a problem reading the file. Settings are still usable: they start from defaults
// and the file is overwritten on the next change.
func LoadError() error {
	store()
	return loadErr
}

// Use points the process-wide store at another path. For tests.
func Use(path string) {
	once.Do(func() {})
	shared, loadErr = Load(path)
}

func SetSpeakerVolume(v int) error            { return store().SetSpeakerVolume(v) }
func SetSpeakerResampling(v Resampling) error { return store().SetSpeakerResampling(v) }

func SetMicMuted(v bool) error     { return store().SetMicMuted(v) }
func SetMicLEDBright(v bool) error { return store().SetMicLEDBright(v) }
func SetMicMixing(v Mixing) error  { return store().SetMicMixing(v) }
func SetMicGain(db int) error      { return store().SetMicGain(db) }

func SetMicLeveling(v bool) error { return store().SetMicLeveling(v) }

func SetRingReaction(v string) error { return store().SetRingReaction(v) }
func SetRingTrouble(v string) error  { return store().SetRingTrouble(v) }
func SetRingMuted(v string) error    { return store().SetRingMuted(v) }
func SetRingLight(v Light) error     { return store().SetRingLight(v) }

func SetUpdateChannel(v string) error { return store().SetUpdateChannel(v) }

func SetWakeWord(slot int, id string) error      { return store().SetWakeWord(slot, id) }
func SetWakeThreshold(slot int, v float64) error { return store().SetWakeThreshold(slot, v) }
func SetWakeTone(slot int, v Tone) error         { return store().SetWakeTone(slot, v) }
func SetWakeEffect(slot int, v string) error     { return store().SetWakeEffect(slot, v) }
func SetWakeDelivery(slot int, v Delivery) error { return store().SetWakeDelivery(slot, v) }
func SetWakeFollowUp(slot, seconds int) error    { return store().SetWakeFollowUp(slot, seconds) }
func SetWakeBuffer(slot, ms int) error           { return store().SetWakeBuffer(slot, ms) }
func SetWakeMaxListen(slot, seconds int) error   { return store().SetWakeMaxListen(slot, seconds) }
func SetWakeMaxThink(slot, seconds int) error    { return store().SetWakeMaxThink(slot, seconds) }

// Store reads and writes one file.
type Store struct {
	path string

	mu sync.Mutex
	s  Stored

	// readable is whether what is on disk was understood. It gates writing, because everything here is
	// written as one whole document: a store that fell back to defaults would replace a file it could
	// not read with a file that has nothing in it, turning a bad read into permanent loss.
	//
	// A missing file is readable. There is nothing to lose and the first change creates it.
	readable bool
}

// Load reads the file. A missing file is not an error: echod runs with defaults and writes it on the
// first change. An unreadable or unparseable one is — the caller may carry on with defaults, but
// nothing will be saved over it until someone has looked.
func Load(path string) (*Store, error) {
	st := &Store{path: path}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			st.readable = true
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return st, fmt.Errorf("settings: %s: %w", path, err)
	}

	st.readable = true
	return st, nil
}

// Get returns a copy.
func (st *Store) Get() Stored {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// Update applies a change and writes the file.
func (st *Store) Update(f func(*Stored)) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	f(&st.s)
	return st.write()
}

// write replaces the file through a temporary one, flushed at every step, because the thing this has
// to survive is a reboot rather than a crash.
//
// A rename is atomic, which is enough for being killed: either the old file or the new one is there.
// It is not enough for losing power. The rename can reach the disk while the data behind it has not,
// and what comes back is the new name with nothing in it — which is how a device that had settings
// comes up with none. So the contents are flushed before the rename and the directory after it.
func (st *Store) write() error {
	if !st.readable {
		return fmt.Errorf("settings: %s was not readable, refusing to write over it", st.path)
	}

	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := st.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmp, st.path); err != nil {
		return err
	}

	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func (st *Store) SetSpeakerVolume(v int) error {
	return st.Update(func(s *Stored) { s.Speaker.Volume = &v })
}

func (st *Store) SetSpeakerResampling(v Resampling) error {
	return st.Update(func(s *Stored) { s.Speaker.Resampling = &v })
}

func (st *Store) SetMicMuted(v bool) error {
	return st.Update(func(s *Stored) { s.Microphone.Muted = &v })
}

func (st *Store) SetMicLEDBright(v bool) error {
	return st.Update(func(s *Stored) { s.Microphone.LEDBright = &v })
}

func (st *Store) SetMicMixing(v Mixing) error {
	return st.Update(func(s *Stored) { s.Microphone.Mixing = &v })
}

func (st *Store) SetMicLeveling(v bool) error {
	return st.Update(func(s *Stored) { s.Microphone.Leveling = &v })
}

func (st *Store) SetMicGain(db int) error {
	return st.Update(func(s *Stored) { s.Microphone.Gain = &db })
}

func (st *Store) SetRingReaction(v string) error {
	return st.Update(func(s *Stored) { s.Ring.Reaction = &v })
}

func (st *Store) SetRingTrouble(v string) error {
	return st.Update(func(s *Stored) { s.Ring.Trouble = &v })
}

func (st *Store) SetRingMuted(v string) error {
	return st.Update(func(s *Stored) { s.Ring.Muted = &v })
}

// SetRingLight saves the whole appearance at once, since that is how it is set.
func (st *Store) SetRingLight(v Light) error {
	return st.Update(func(s *Stored) { s.Ring.Light = v })
}

func (st *Store) SetUpdateChannel(v string) error {
	return st.Update(func(s *Stored) { s.Update.Channel = &v })
}

func (st *Store) SetWakeWord(slot int, id string) error {
	return st.updateWord(slot, func(w *WakeWord) { w.ID = &id })
}

func (st *Store) SetWakeThreshold(slot int, v float64) error {
	return st.updateWord(slot, func(w *WakeWord) { w.Threshold = &v })
}

func (st *Store) SetWakeTone(slot int, v Tone) error {
	return st.updateWord(slot, func(w *WakeWord) { w.Tone = &v })
}

func (st *Store) SetWakeEffect(slot int, v string) error {
	return st.updateWord(slot, func(w *WakeWord) { w.Effect = &v })
}

func (st *Store) SetWakeDelivery(slot int, v Delivery) error {
	return st.updateWord(slot, func(w *WakeWord) { w.Delivery = &v })
}

func (st *Store) SetWakeFollowUp(slot, seconds int) error {
	return st.updateWord(slot, func(w *WakeWord) { w.FollowUp = &seconds })
}

func (st *Store) SetWakeBuffer(slot, ms int) error {
	return st.updateWord(slot, func(w *WakeWord) { w.Buffer = &ms })
}

func (st *Store) SetWakeMaxListen(slot, seconds int) error {
	return st.updateWord(slot, func(w *WakeWord) { w.MaxListen = &seconds })
}

func (st *Store) SetWakeMaxThink(slot, seconds int) error {
	return st.updateWord(slot, func(w *WakeWord) { w.MaxThink = &seconds })
}

func (st *Store) updateWord(slot int, f func(*WakeWord)) error {
	if slot < 0 {
		return fmt.Errorf("settings: wake slot %d", slot)
	}
	return st.Update(func(s *Stored) {
		for len(s.Wake.Words) <= slot {
			s.Wake.Words = append(s.Wake.Words, WakeWord{})
		}
		f(&s.Wake.Words[slot])
	})
}
