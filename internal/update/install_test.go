package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// release stands in for a published build: a manifest describing a binary, and the binary itself.
func release(t *testing.T, binary []byte) Manifest {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/echod", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(binary) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sum := sha256.Sum256(binary)
	return Manifest{
		Version: "0.0.1",
		URL:     srv.URL + "/echod",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(binary)),
	}
}

func TestDownloadChecksWhatArrived(t *testing.T) {
	dir := t.TempDir()
	m := release(t, []byte("a new echod"))

	for name, tc := range map[string]struct {
		break_ func(*Manifest)
		want   string
	}{
		"as offered":   {func(*Manifest) {}, ""},
		"wrong hash":   {func(m *Manifest) { m.SHA256 = strings.Repeat("a", 64) }, "hash"},
		"wrong size":   {func(m *Manifest) { m.Size = 4 }, "bytes"},
		"gone away":    {func(m *Manifest) { m.URL += "/missing" }, "404"},
		"no such host": {func(m *Manifest) { m.URL = "http://127.0.0.1:1/echod" }, "fetching"},
	} {
		offered := m
		tc.break_(&offered)

		to := filepath.Join(dir, name)
		err := download(context.Background(), offered, to, nil)

		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && err == nil:
			t.Errorf("%s: accepted a download it should have refused", name)
		case tc.want != "" && !strings.Contains(err.Error(), tc.want):
			t.Errorf("%s: %v, want something about %q", name, err, tc.want)
		}
	}
}

// A download that was refused must not be left behind looking like a binary somebody could run.
func TestInstallRefusesAManifestItCannotUse(t *testing.T) {
	somewhere(t)

	for name, m := range map[string]Manifest{
		"no version": {URL: "http://example/echod", SHA256: strings.Repeat("a", 64), Size: 1},
		"no url":     {Version: "0.0.1", SHA256: strings.Repeat("a", 64), Size: 1},
		"no hash":    {Version: "0.0.1", URL: "http://example/echod", Size: 1},
		"no size":    {Version: "0.0.1", URL: "http://example/echod", SHA256: strings.Repeat("a", 64)},
	} {
		if err := Install(context.Background(), m, nil); err == nil {
			t.Errorf("%s: installed something unusable", name)
		}
	}
}

// Progress is what somebody watches instead of wondering whether it has stalled, so it has to reach 1
// and never run past it.
func TestProgressReachesTheEnd(t *testing.T) {
	dir := t.TempDir()
	m := release(t, []byte(strings.Repeat("x", 64<<10)))

	var last float32
	var calls int
	err := download(context.Background(), m, filepath.Join(dir, "echod"), func(at float32) {
		calls++
		if at < last {
			t.Errorf("progress went backwards: %v then %v", last, at)
		}
		if at > 1 {
			t.Errorf("progress reported %v", at)
		}
		last = at
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("nothing was reported")
	}
	if last < 0.9 {
		t.Errorf("finished at %v", last)
	}
}

// The staged file is what gets copied into /system, so it has to be exactly what was offered.
func TestDownloadWritesWhatItVerified(t *testing.T) {
	dir := t.TempDir()
	want := []byte("a new echod")
	m := release(t, want)

	to := filepath.Join(dir, "echod")
	if err := download(context.Background(), m, to, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("staged %q, want %q", got, want)
	}
}
