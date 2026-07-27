// Package state persists echod's runtime settings across restarts and reboots.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ygelfand/echolocal/internal/layout"
)

// State is what echod remembers across restarts. Config is separate and read-only: it lives in
// /system/etc/echolocal/echod.yaml and nothing here overrides it.
type State struct {
	// MAC is wlan0's address, cached because the interface is not up when echod starts.
	MAC string `json:"mac,omitempty"`

	Settings Settings `json:"settings"`
}

// Settings are the values Home Assistant and the buttons change. Pointers mark a setting that has
// never been touched, so a default wins over a zero value.
type Settings struct {
	Volume        *int  `json:"volume,omitempty"`
	MicMuted      *bool `json:"mic_muted,omitempty"`
	MuteLEDBright *bool `json:"mute_led_bright,omitempty"`
}

// The process-wide store, at layout.StatePath. There is one device and one state file, so
// components call the package functions rather than being handed a Store.
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

// Get returns the current state, loading it on first use.
func Get() State { return store().Get() }

// LoadError reports a problem reading the state file. State is still usable: it starts from
// defaults and overwrites the file on the next change.
func LoadError() error {
	store()
	return loadErr
}

// Use points the process-wide store at another path. For tests.
func Use(path string) {
	once.Do(func() {})
	shared, loadErr = Load(path)
}

func SetVolume(v int) error         { return store().SetVolume(v) }
func SetMicMuted(v bool) error      { return store().SetMicMuted(v) }
func SetMuteLEDBright(v bool) error { return store().SetMuteLEDBright(v) }
func SetMAC(mac string) error       { return store().SetMAC(mac) }

// Store reads and writes one state file.
type Store struct {
	path string

	mu sync.Mutex
	s  State
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
		return st, fmt.Errorf("state: %s: %w", path, err)
	}
	return st, nil
}

// Get returns a copy.
func (st *Store) Get() State {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// Update applies a change and writes the file.
func (st *Store) Update(f func(*State)) error {
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

// VolumeOr reports the stored volume step, or def if it has never been set.
func (s Settings) VolumeOr(def int) int {
	if s.Volume == nil {
		return def
	}
	return *s.Volume
}

// MicMutedOr reports the stored mute state, or def if it has never been set.
func (s Settings) MicMutedOr(def bool) bool {
	if s.MicMuted == nil {
		return def
	}
	return *s.MicMuted
}

// MuteLEDBrightOr reports the stored mute LED brightness, or def if it has never been set.
func (s Settings) MuteLEDBrightOr(def bool) bool {
	if s.MuteLEDBright == nil {
		return def
	}
	return *s.MuteLEDBright
}

// SetVolume, SetMicMuted and SetMuteLEDBright save one setting.
func (st *Store) SetVolume(v int) error {
	return st.Update(func(s *State) { s.Settings.Volume = &v })
}

func (st *Store) SetMicMuted(v bool) error {
	return st.Update(func(s *State) { s.Settings.MicMuted = &v })
}

func (st *Store) SetMuteLEDBright(v bool) error {
	return st.Update(func(s *State) { s.Settings.MuteLEDBright = &v })
}

// SetMAC saves wlan0's address.
func (st *Store) SetMAC(mac string) error {
	return st.Update(func(s *State) { s.MAC = mac })
}
