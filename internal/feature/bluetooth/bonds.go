package bluetooth

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/ygelfand/echolocal/internal/hardware/ble"
	"github.com/ygelfand/echolocal/internal/layout"
)

// bondsPath is where pairing keys are kept, so that a device paired once takes an encrypted link
// again without its pairing mode. Only root reads it: these keys open the device's link.
var bondsPath = filepath.Join(layout.StateDir, "bonds.json")

// bonds is the pairing keys by device address.
type bonds struct {
	mu   sync.Mutex
	path string
}

func (b *bonds) load() (map[string]ble.Keys, error) {
	data, err := os.ReadFile(b.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]ble.Keys{}, nil
	}
	if err != nil {
		return nil, err
	}
	all := map[string]ble.Keys{}
	return all, json.Unmarshal(data, &all)
}

func (b *bonds) get(address uint64) (ble.Keys, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	all, err := b.load()
	if err != nil {
		return ble.Keys{}, false
	}
	k, ok := all[mac(address)]
	return k, ok
}

func (b *bonds) put(address uint64, k ble.Keys) error {
	return b.change(func(all map[string]ble.Keys) { all[mac(address)] = k })
}

func (b *bonds) remove(address uint64) error {
	return b.change(func(all map[string]ble.Keys) { delete(all, mac(address)) })
}

func (b *bonds) change(f func(map[string]ble.Keys)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	all, err := b.load()
	if err != nil {
		all = map[string]ble.Keys{} // an unreadable file is replaced, not kept around
	}
	f(all)
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}
