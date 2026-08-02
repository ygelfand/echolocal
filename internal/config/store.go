package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/ygelfand/echolocal/internal/layout"
)

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

// Get is what the device is set to.
func Get() Config { return store().Get() }

// Set names something to change. Each leaf takes the lock, applies the change and writes the file:
//
//	config.Set().Speaker().Volume(8)
func Set() Writer { return store().Set() }

// LoadError reports a problem reading the file. Config is still usable: it starts from defaults and
// the file is overwritten on the next change.
func LoadError() error {
	store()
	return loadErr
}

// Started records what echod was told at start-up.
func Started(d Device) { store().started(d) }

// Use points the process-wide store at another path. For tests.
func Use(path string) {
	once.Do(func() {})
	shared, loadErr = Load(path)
}

// Store reads and writes one file.
type Store struct {
	path string

	mu sync.Mutex
	c  Config

	// readable is whether what is on disk was understood. It gates writing, because everything here is
	// written as one whole document: a store that fell back to defaults would replace a file it could
	// not read with a file that has nothing in it, turning a bad read into permanent loss.
	//
	// A missing file is readable. There is nothing to lose and the first change creates it.
	readable bool
}

// Load reads the file over the defaults, so a key the file does not mention keeps the value it was
// built with.
//
// A missing file is not an error: echod runs with defaults and writes it on the first change. An
// unreadable or unparseable one is — the caller may carry on with defaults, but nothing will be
// saved over it until someone has looked.
func Load(path string) (*Store, error) {
	st := &Store{path: path, c: Defaults()}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			st.readable = true
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st.c); err != nil {
		return st, fmt.Errorf("config: %s: %w", path, err)
	}

	st.readable = true
	return st, nil
}

// Get returns a copy. The wake words are copied too, or a caller holding the snapshot would be
// holding the store's own slice.
func (st *Store) Get() Config {
	st.mu.Lock()
	defer st.mu.Unlock()

	c := st.c
	c.Wake.Words = slices.Clone(st.c.Wake.Words)
	return c
}

// Set names something to change in this store.
func (st *Store) Set() Writer { return Writer{st: st} }

func (st *Store) started(d Device) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.c.Device = d
}

// Update applies a change and writes the file.
func (st *Store) Update(f func(*Config)) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	f(&st.c)
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
		return fmt.Errorf("config: %s was not readable, refusing to write over it", st.path)
	}

	b, err := json.MarshalIndent(st.c, "", "  ")
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
