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
	// MAC is wlan0's address, cached because the interface is not up when echod starts. Not a
	// setting, but it belongs to the same file: one document, one atomic write.
	MAC string `json:"mac,omitempty"`

	Speaker    Speaker    `json:"speaker,omitzero"`
	Microphone Microphone `json:"microphone,omitzero"`
	Wake       Wake       `json:"wake,omitzero"`
}

type Speaker struct {
	Volume *int `json:"volume,omitempty"`

	// Resampling is how voice is stretched to the playback rate.
	Resampling *Resampling `json:"resampling,omitempty"`
}

type Microphone struct {
	Muted     *bool `json:"muted,omitempty"`
	LEDBright *bool `json:"led_bright,omitempty"`

	// Mixing is how the seven microphones are combined. Which one wins depends on the room.
	Mixing *Mixing `json:"mixing,omitempty"`
}

// Wake is the wake word configuration. Words is keyed by backend and indexed by Home Assistant's
// wake word slot, so switching backends brings back the words that backend was last used with,
// tuning and all, rather than starting over. Thresholds especially do not carry across: the two
// engines score on different scales.
type Wake struct {
	Backend *WakeBackend `json:"backend,omitempty"`

	Words map[WakeBackend][]WakeWord `json:"words,omitempty"`
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

func SetMAC(mac string) error { return store().SetMAC(mac) }

func SetSpeakerVolume(v int) error            { return store().SetSpeakerVolume(v) }
func SetSpeakerResampling(v Resampling) error { return store().SetSpeakerResampling(v) }

func SetMicMuted(v bool) error     { return store().SetMicMuted(v) }
func SetMicLEDBright(v bool) error { return store().SetMicLEDBright(v) }
func SetMicMixing(v Mixing) error  { return store().SetMicMixing(v) }

func SetWakeBackend(v WakeBackend) error         { return store().SetWakeBackend(v) }
func SetWakeWord(slot int, id string) error      { return store().SetWakeWord(slot, id) }
func SetWakeThreshold(slot int, v float64) error { return store().SetWakeThreshold(slot, v) }
func SetWakeTone(slot int, v Tone) error         { return store().SetWakeTone(slot, v) }
func SetWakeEffect(slot int, v string) error     { return store().SetWakeEffect(slot, v) }

// Store reads and writes one file.
type Store struct {
	path string

	mu sync.Mutex
	s  Stored
}

// Load reads the file. A missing or unreadable file is not an error: echod runs with defaults and
// writes the file on the first change.
func Load(path string) (*Store, error) {
	st := &Store{path: path}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return st, fmt.Errorf("settings: %s: %w", path, err)
	}
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

// write replaces the file through a temporary one, so a kill mid-write cannot truncate it.
func (st *Store) write() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}

	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// SetMAC saves wlan0's address.
func (st *Store) SetMAC(mac string) error {
	return st.Update(func(s *Stored) { s.MAC = mac })
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

func (st *Store) SetWakeBackend(v WakeBackend) error {
	return st.Update(func(s *Stored) { s.Wake.Backend = &v })
}

// The wake setters write into whichever backend is selected, so no caller has to say which: the
// slot the user is editing is a slot of the engine that is running.
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

func (st *Store) updateWord(slot int, f func(*WakeWord)) error {
	if slot < 0 {
		return fmt.Errorf("settings: wake slot %d", slot)
	}
	return st.Update(func(s *Stored) {
		backend := s.Wake.BackendOr(BackendOpenWakeWord)
		if s.Wake.Words == nil {
			s.Wake.Words = map[WakeBackend][]WakeWord{}
		}

		words := s.Wake.Words[backend]
		for len(words) <= slot {
			words = append(words, WakeWord{})
		}
		f(&words[slot])
		s.Wake.Words[backend] = words
	})
}
