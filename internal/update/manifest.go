package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Manifest is what a release says about itself, and the only thing a device reads to decide there is
// something newer. It is written by the release build and served beside the binary it describes.
//
// The device does not compare versions: Home Assistant does that, with a parser that forces the update
// card permanently on for anything it cannot rank. So Version has to stay something AwesomeVersion can
// read — dotted numerals, optionally a prerelease, and any build detail after an underscore, which is
// where Home Assistant truncates before comparing.
type Manifest struct {
	// Version is what a device reports as available, and what Home Assistant ranks against what it is
	// running.
	Version string `json:"version"`

	// URL is the binary. SHA256 and Size are its own, and both are checked before anything is written:
	// a length that matches proves nothing, and neither proves the file came from us, which is what
	// signing is for.
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`

	Title      string `json:"title,omitempty"`
	Notes      string `json:"notes,omitempty"`
	ReleaseURL string `json:"release_url,omitempty"`
}

// manifestTimeout bounds the fetch. Home Assistant asks for this on connect and after every selection
// change, so it has to fail quickly rather than hold up a configuration reply.
const manifestTimeout = 10 * time.Second

// maxManifest is a sanity bound on the response. A manifest is a few hundred bytes; anything else is a
// captive portal or a mistake.
const maxManifest = 64 << 10

// Fetch reads the channel's manifest and checks that it describes something installable. A manifest
// that arrives without a version or without somewhere to fetch the binary from is a broken release, and
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

// Valid reports whether the manifest describes something installable, which is checked both where one
// is written and where one is read.
func (m Manifest) Valid() error {
	switch {
	case m.Version == "":
		return fmt.Errorf("update: the manifest names no version")
	case m.URL == "":
		return fmt.Errorf("update: the manifest for %s has no url", m.Version)
	case len(m.SHA256) != 64:
		return fmt.Errorf("update: the manifest for %s has no usable sha256", m.Version)
	case m.Size <= 0:
		return fmt.Errorf("update: the manifest for %s gives no size", m.Version)
	}
	return nil
}

// Matches reports whether these bytes are what the manifest described. Size is checked first because a
// truncated download is the common failure and saying so is more use than a hash mismatch.
func (m Manifest) Matches(data []byte) error {
	if int64(len(data)) != m.Size {
		return fmt.Errorf("update: %d bytes, offered as %d", len(data), m.Size)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != m.SHA256 {
		return fmt.Errorf("update: hash %s, offered as %s", got, m.SHA256)
	}
	return nil
}
