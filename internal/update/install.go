package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/layout"
)

// downloadTimeout bounds the fetch. Sixteen megabytes over a satellite's wifi is not quick, and a stalled
// download should give up rather than hold the device in an installing state for ever.
const downloadTimeout = 10 * time.Minute

// installing is held for the whole of an install, so a second request while one is running is refused
// rather than racing it onto the same file.
var installing sync.Mutex

// Install replaces this binary with the one the manifest describes and asks for a restart.
//
// Nothing is written to /system until the download has been fetched whole and its hash checked. What
// this replaces is kept as echod.prev, which is what the boot hook restores if the new one never gets
// far enough to be believed.
//
// progress is called with a fraction as the download runs, for whoever is watching in Home Assistant.
func Install(ctx context.Context, m Manifest, progress func(float32)) error {
	if !installing.TryLock() {
		return fmt.Errorf("update: an install is already running")
	}
	defer installing.Unlock()

	if err := m.Valid(); err != nil {
		return err
	}

	staged := filepath.Join(layout.StateDir, "echod.incoming")
	defer os.Remove(staged)

	if err := download(ctx, m, staged, progress); err != nil {
		return err
	}
	if err := room(m.Size); err != nil {
		return err
	}
	return swap(staged, m.Version)
}

// download fetches the binary and proves it before it is allowed near /system. The hash is taken as the
// bytes go past rather than by reading the file back, so nothing has to hold sixteen megabytes in memory
// on a device with half a gigabyte.
func download(ctx context.Context, m Manifest, to string, progress func(float32)) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("update: fetching %s: %w", m.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: fetching %s: %s", m.URL, resp.Status)
	}

	f, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()

	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, sum), &counter{
		from: resp.Body, size: m.Size, report: progress,
	})
	if err != nil {
		return fmt.Errorf("update: downloading %s: %w", m.Version, err)
	}
	if err := f.Sync(); err != nil {
		return err
	}

	if written != m.Size {
		return fmt.Errorf("update: %d bytes, offered as %d", written, m.Size)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != m.SHA256 {
		return fmt.Errorf("update: hash %s, offered as %s", got, m.SHA256)
	}
	return nil
}

// swap puts the new binary in place, keeping what it replaced. The order matters: the old one is moved
// aside first, so at no point is there no binary at all, and the version being tried is recorded before
// anything is replaced, so a rollback can say what it took out.
func swap(staged, version string) error {
	if err := os.WriteFile(layout.UpdatingPath, []byte(version), 0o644); err != nil {
		slog.Error("recording the version being installed failed", "err", err)
	}

	if err := writable(true); err != nil {
		return fmt.Errorf("update: remounting to install: %w", err)
	}
	defer func() {
		if err := writable(false); err != nil {
			slog.Error("remounting read-only failed", "err", err)
		}
	}()

	if err := os.Rename(layout.Binary, prev); err != nil {
		return fmt.Errorf("update: keeping the running binary: %w", err)
	}
	if err := copyTo(staged, layout.Binary); err != nil {
		// Put it back rather than leaving a device with no binary at all for the next boot to find.
		if back := os.Rename(prev, layout.Binary); back != nil {
			slog.Error("could not put the previous binary back", "err", back)
		}
		return err
	}

	if out, err := exec.Command("chcon", layout.OurLabel, layout.Binary).CombinedOutput(); err != nil {
		slog.Warn("labelling the new binary failed", "err", err, "output", string(out))
	}
	slog.Warn("update installed, restarting into it", "version", version, "previous", prev)
	return nil
}

// copyTo writes the staged binary into /system. A rename would be cheaper and atomic, but /data and
// /system are different filesystems, so there is nothing to rename across.
func copyTo(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("update: writing %s: %w", to, err)
	}
	return dst.Sync()
}

// counter reports how far a download has got, as a fraction.
type counter struct {
	from   io.Reader
	size   int64
	report func(float32)

	read int64
	last float32
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.from.Read(p)
	c.read += int64(n)

	if c.report == nil || c.size <= 0 {
		return n, err
	}

	// Only when it has moved a percent, since every update is a message to Home Assistant.
	if at := float32(c.read) / float32(c.size); at-c.last >= 0.01 {
		c.last = at
		c.report(at)
	}
	return n, err
}
