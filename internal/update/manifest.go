package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

// Manifest is what a release says about itself, and the only thing a device reads to decide there is
// something newer. It is written by the release build and served beside the binaries it describes.
//
// The device does not compare versions: Home Assistant does that, with a parser that forces the update
// card permanently on for anything it cannot rank. So Version has to stay something AwesomeVersion can
// read — dotted numerals, optionally a prerelease, and any build detail after an underscore, which is
// where Home Assistant truncates before comparing.
type Manifest struct {
	// Version is what a device reports as available, and what Home Assistant ranks against what it is
	// running.
	Version string `json:"version"`

	// URL, SHA256 and Size are the arm64 build. An echod that reads only these is arm64 by
	// construction, and they are its only route onto a newer build.
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`

	// Binaries is keyed by the Go architecture each build targets.
	Binaries map[string]Binary `json:"binaries"`

	Title      string `json:"title,omitempty"`
	Notes      string `json:"notes,omitempty"`
	ReleaseURL string `json:"release_url,omitempty"`
}

// Binary is one build of a release. SHA256 and Size are both checked before anything is written: a
// length that matches proves nothing, and neither proves the file came from us, which is what signing
// is for.
type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

const flatArch = "arm64"

// arch is a variable so a test can stand somewhere other than the machine it runs on.
var arch = runtime.GOARCH

// manifestTimeout bounds the fetch. Home Assistant asks for this on connect and after every selection
// change, so it has to fail quickly rather than hold up a configuration reply.
const manifestTimeout = 10 * time.Second

// maxManifest is a sanity bound on the response. A manifest is a few hundred bytes; anything else is a
// captive portal or a mistake.
const maxManifest = 64 << 10

// Fetch reads the channel's manifest and checks that it describes something installable. A manifest
// that arrives without a version or without somewhere to fetch a binary from is a broken release, and
// saying so here is better than failing half way through an install.
func Fetch(ctx context.Context, c Channel) (Manifest, error) {
	var m Manifest

	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()

	url := c.URL()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return m, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return m, fmt.Errorf("update: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("update: fetching %s: %s", url, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifest)).Decode(&m); err != nil {
		return m, fmt.Errorf("update: reading the manifest at %s: %w", url, err)
	}
	return m, m.Valid()
}

func (m Manifest) flat() Binary {
	return Binary{URL: m.URL, SHA256: m.SHA256, Size: m.Size}
}

// Valid reports whether the manifest describes something installable, which is checked both where one
// is written and where one is read. It does not ask whether this device is served — For does that.
func (m Manifest) Valid() error {
	if m.Version == "" {
		return errors.New("update: the manifest names no version")
	}

	if flat := m.flat(); flat != (Binary{}) {
		if err := flat.valid(m.Version, flatArch); err != nil {
			return err
		}
	} else if len(m.Binaries) == 0 {
		return fmt.Errorf("update: the manifest for %s carries no binaries", m.Version)
	}

	for a, b := range m.Binaries {
		if err := b.valid(m.Version, a); err != nil {
			return err
		}
	}
	return nil
}

func (b Binary) valid(version, arch string) error {
	switch {
	case b.URL == "":
		return fmt.Errorf("update: the %s binary for %s has no url", arch, version)
	case len(b.SHA256) != 64:
		return fmt.Errorf("update: the %s binary for %s has no usable sha256", arch, version)
	case b.Size <= 0:
		return fmt.Errorf("update: the %s binary for %s gives no size", arch, version)
	}
	return nil
}

// For is the build a device of this architecture may install. A manifest carrying only the top-level
// fields answers for arm64.
func (m Manifest) For(arch string) (Binary, error) {
	if b, ok := m.Binaries[arch]; ok {
		return b, nil
	}
	if len(m.Binaries) == 0 && arch == flatArch {
		return m.flat(), nil
	}
	return Binary{}, fmt.Errorf("update: %s carries no %s binary", m.Version, arch)
}
