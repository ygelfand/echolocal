package sendspin

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// trust is what the room holds that makes it itself to a server: its identity, the pairing key an
// operator types in to pair it, and the record of every server it has paired with. It is one file,
// separate from the settings, because everything in it is a secret and none of it is a preference.
type trust struct {
	path string

	mu sync.Mutex
	f  trustFile
	id *identity
}

type trustFile struct {
	IdentityKey string   `json:"identity_key"`
	PairingPSK  string   `json:"pairing_psk"`
	Records     []record `json:"records"`

	// LastPlayback is the server that most recently played through this room, which the spec has the
	// room remember to break ties between servers.
	LastPlayback string `json:"last_playback_server,omitempty"`
}

// record is one pairing: the long-term key and the server it is bound to.
type record struct {
	ServerID string    `json:"server_id"`
	PSK      string    `json:"psk"`
	LastUsed time.Time `json:"last_used"`
}

// maxRecords is how many servers the room stays paired with. The spec asks for at least five; past
// this the one used longest ago is forgotten, and its server finds out the next time it connects.
const maxRecords = 8

// loadTrust reads the file, or makes a new identity and pairing key when there is none. An unreadable
// file is an error rather than a fresh start: replacing it would silently turn the room into a new
// device for every server that knew it.
func loadTrust(path string) (*trust, error) {
	t := &trust{path: path}

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &t.f); err != nil {
			return nil, fmt.Errorf("sendspin trust: %s: %w", path, err)
		}
	case os.IsNotExist(err):
	default:
		return nil, fmt.Errorf("sendspin trust: %w", err)
	}

	changed := false
	if t.f.IdentityKey == "" {
		id, err := newIdentity()
		if err != nil {
			return nil, err
		}
		t.f.IdentityKey = b64.EncodeToString(id.priv[:])
		changed = true
	}
	if t.f.PairingPSK == "" {
		p, err := newPSK()
		if err != nil {
			return nil, err
		}
		t.f.PairingPSK = p.String()
		changed = true
	}

	priv, err := b64.DecodeString(t.f.IdentityKey)
	if err != nil {
		return nil, fmt.Errorf("sendspin trust: identity key: %w", err)
	}
	if t.id, err = identityFrom(priv); err != nil {
		return nil, fmt.Errorf("sendspin trust: %w", err)
	}
	if _, err := parsePSK(t.f.PairingPSK); err != nil {
		return nil, fmt.Errorf("sendspin trust: pairing key: %w", err)
	}

	if changed {
		if err := t.save(); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *trust) identity() *identity { return t.id }
func (t *trust) clientID() string    { return t.id.id() }

func (t *trust) pairingPSK() psk {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, _ := parsePSK(t.f.PairingPSK)
	return p
}

// token is the pairing token an operator enters into a server: the room's public key and pairing key
// together, in the spec's SP:0 form.
func (t *trust) token() string {
	p := t.pairingPSK()
	return encodeToken(0, append(append([]byte(nil), t.id.pub[:]...), p[:]...))
}

// encodeToken is the spec's pairing token: a prefix, a version, then base32 without padding and with
// every 2 written as 9, so the whole thing is in the QR alphanumeric set.
func encodeToken(version byte, payload []byte) string {
	body := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(payload)
	return "SP:" + string('0'+version) + strings.ReplaceAll(body, "2", "9")
}

// lookup is the candidate list for one handshake, bound to the server that sent server/init: a record
// for another server matching is not a miss but a misbinding, and fails.
func (t *trust) lookup(serverID string) pskLookup {
	return func(pskID string) (psk, pskCategory, error) {
		t.mu.Lock()
		defer t.mu.Unlock()

		for _, r := range t.f.Records {
			p, err := parsePSK(r.PSK)
			if err != nil || p.id() != pskID {
				continue
			}
			if r.ServerID != serverID {
				return psk{}, 0, fmt.Errorf("key is bound to server %s, not %s", short(r.ServerID), short(serverID))
			}
			return p, categoryLongTerm, nil
		}
		if p, err := parsePSK(t.f.PairingPSK); err == nil && p.id() == pskID {
			return p, categoryPairing, nil
		}
		if s := sentinelPSK(); s.id() == pskID {
			return s, categorySentinel, nil
		}
		return psk{}, 0, errPSKMiss
	}
}

// paired says whether the room holds a record for the server.
func (t *trust) paired(serverID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.ContainsFunc(t.f.Records, func(r record) bool { return r.ServerID == serverID })
}

// remember stores a pairing, replacing what the room held for that server. Past capacity the record
// used longest ago goes, never the one being written or the one named as still in use.
func (t *trust) remember(serverID string, key psk, inUse string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.f.Records = slices.DeleteFunc(t.f.Records, func(r record) bool { return r.ServerID == serverID })
	t.f.Records = append(t.f.Records, record{ServerID: serverID, PSK: key.String(), LastUsed: time.Now()})

	for len(t.f.Records) > maxRecords {
		oldest := -1
		for i, r := range t.f.Records {
			if r.ServerID == serverID || r.ServerID == inUse {
				continue
			}
			if oldest < 0 || r.LastUsed.Before(t.f.Records[oldest].LastUsed) {
				oldest = i
			}
		}
		if oldest < 0 {
			break
		}
		t.f.Records = slices.Delete(t.f.Records, oldest, oldest+1)
	}
	return t.save()
}

// touch marks a record as used now, so eviction picks another one.
func (t *trust) touch(serverID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.f.Records {
		if t.f.Records[i].ServerID == serverID {
			t.f.Records[i].LastUsed = time.Now()
			_ = t.save()
			return
		}
	}
}

// forget drops one server's record, which is what server/unpair asks for.
func (t *trust) forget(serverID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	before := len(t.f.Records)
	t.f.Records = slices.DeleteFunc(t.f.Records, func(r record) bool { return r.ServerID == serverID })
	if len(t.f.Records) == before {
		return nil
	}
	return t.save()
}

// forgetAll drops every pairing. The identity and pairing key stay: the room is still the same device,
// it just trusts nobody until paired again.
func (t *trust) forgetAll() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.f.Records = nil
	t.f.LastPlayback = ""
	return t.save()
}

func (t *trust) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.f.Records)
}

func (t *trust) setLastPlayback(serverID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f.LastPlayback == serverID {
		return
	}
	t.f.LastPlayback = serverID
	_ = t.save()
}

// save writes the file the way config does: through a temporary, flushed before the rename and the
// directory after it, so losing power leaves either the old file or the new one. Mode 0600: the
// private key is the room's identity.
//
// Wants mu.
func (t *trust) save() error {
	b, err := json.MarshalIndent(t.f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(t.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
	if err := os.Rename(tmp, t.path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

// short is an identity as it reads in a log line: enough to tell two apart, not enough to fill it.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
