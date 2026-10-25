package wakecapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewRecorderIsOffAndDoesNotCreateStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	r := New(dir)
	r.Frame([]int16{123}, false, 0)
	r.Score(0, .9, .75, true, false, 0)
	r.Mark("turn_start", 0)
	if r.Status().Enabled || r.used != 0 || len(r.queue) != 0 {
		t.Fatal("default recording enabled")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("default created storage", err)
	}
}

func TestSourceGapDropsPendingAudio(t *testing.T) {
	r := fixture(t)
	pre(r)
	r.Score(0, .9, .75, true, false, 0)
	r.Frame(make([]int16, 160), false, 1)
	if len(r.pending) != 0 || r.used != 160 || r.dropped.Load() == 0 {
		t.Fatal("source gap stitched audio")
	}
}

func TestStopClearsPendingAudioAndRestartHasNoPreroll(t *testing.T) {
	r := fixture(t)
	pre(r)
	r.Score(0, .9, .75, true, false, 0)
	audio := r.pending[0].Audio
	r.Stop()
	for _, v := range audio {
		if v != 0 {
			t.Fatal("pending samples retained")
		}
	}
	if err := r.Start(1, 1); err != nil {
		t.Fatal(err)
	}
	r.Score(0, .9, .75, true, false, 0)
	if len(r.pending) != 0 || r.used != 0 {
		t.Fatal("old pre-roll reused")
	}
}

func TestModelChangeInvalidatesPendingCapture(t *testing.T) {
	r := fixture(t)
	pre(r)
	r.Score(0, .9, .75, true, false, 0)
	path := filepath.Join(t.TempDir(), "synthetic.model")
	data := []byte("synthetic model fixture; not an inference model")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r.Model(0, "synthetic", "test", path, 5, 20)
	sum := sha256.Sum256(data)
	if r.models[0].SHA256 != hex.EncodeToString(sum[:]) || r.used != 0 || len(r.pending) != 0 {
		t.Fatal("model/audio association")
	}
}

func TestPagesReconstructExactAudio(t *testing.T) {
	r := fixture(t)
	pre(r)
	trigger(r)
	e := <-r.queue
	if err := r.save(e); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for page := 0; ; page++ {
		b, pages, err := r.Data(e.ID, "wav", page)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b...)
		if page+1 == pages {
			break
		}
	}
	if !bytes.Equal(got, wav(e.Audio)) {
		t.Fatal("page transport changed audio")
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != e.WavSHA256 {
		t.Fatal("transport hash mismatch")
	}
	restarted := New(r.dir)
	if restarted.Status().Enabled {
		t.Fatal("restart enabled recording")
	}
	ids, err := restarted.List()
	if err != nil || len(ids) != 1 || ids[0] != e.ID {
		t.Fatal("restart lost history", ids, err)
	}
}

func TestReadRejectsSymlinkAndOversize(t *testing.T) {
	r := fixture(t)
	id := "0123456789abcdef-00000001"
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("synthetic private data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.dir, id+".json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Data(id, "json", 0); err == nil {
		t.Fatal("followed symlink")
	}
	ids, err := r.List()
	if err != nil || len(ids) != 0 {
		t.Fatal("listed symlink", ids, err)
	}
	f, err := os.Create(filepath.Join(r.dir, id+".wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(45 + 2*(Pre+Post)); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Data(id, "wav", 0); err == nil {
		t.Fatal("oversize accepted")
	}
}

func TestWriterStopsOnCapacityWithoutRemovingHistory(t *testing.T) {
	r := fixture(t)
	f, err := os.Create(filepath.Join(r.dir, "existing"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(DiskLimit); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	r.Mark("turn_start", 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for r.Status().Enabled && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.Status().Enabled || r.Status().Errors != 1 {
		t.Fatal("writer did not stop")
	}
	if s, err := os.Stat(filepath.Join(r.dir, "existing")); err != nil || s.Size() != DiskLimit {
		t.Fatal("history changed", err)
	}
}

func TestConcurrentAudioAndControl(t *testing.T) {
	r := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()
	audio := make(chan struct{})
	go func() {
		defer close(audio)
		for i := 0; i < 1000; i++ {
			r.Frame([]int16{1, 2}, false, 0)
			r.Score(0, .2, .75, false, false, 0)
			r.Peak(0, .2)
		}
	}()
	for i := 0; i < 100; i++ {
		r.Stop()
		_ = r.Start(1, 100)
		r.Status()
		_, _ = r.List()
		r.Mark("follow_up", 0)
	}
	<-audio
	cancel()
	<-done
	if r.Status().Enabled {
		t.Fatal("cancellation left recording enabled")
	}
}

func TestStartRejectsPreviousWriteEvenAfterDequeue(t *testing.T) {
	r := fixture(t)
	r.Mark("turn_start", 0)
	event := <-r.queue // The worker has taken ownership but has not finished saving.
	r.Stop()
	if err := r.Start(1, 1); err == nil {
		t.Fatal("started while a previous write was in flight")
	}
	if err := r.save(event); err != nil {
		t.Fatal(err)
	}
	r.writes.Add(-1)
	if err := r.Start(1, 1); err != nil {
		t.Fatal("completed writer prevented restart", err)
	}
}

func BenchmarkDisabledAudioPath(b *testing.B) {
	r := New(filepath.Join(b.TempDir(), "captures"))
	frame := make([]int16, 160)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Frame(frame, false, 0)
		r.Score(0, .2, .75, false, false, 0)
	}
}
