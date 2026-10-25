package wakecapture

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) *Recorder {
	t.Helper()
	r := New(filepath.Join(t.TempDir(), "captures"))
	r.models[0] = Model{ID: "okay_nabu", Engine: "microwakeword", SHA256: "fixture-model"}
	r.now = func() time.Time { return time.Unix(1000, 0) }
	r.Snapshot = func() Settings { return Settings{Gain: 20, AEC: true, Mic: "center"} }
	if err := r.Start(1, 100); err != nil {
		t.Fatal(err)
	}
	return r
}
func pre(r *Recorder) {
	for i := 0; i < 510; i++ {
		f := make([]int16, 160)
		for j := range f {
			f[j] = int16(i)
		}
		r.Frame(f, false, 0)
		r.Score(0, 0.1, 0.75, false, false, 0)
	}
}
func trigger(r *Recorder) {
	r.Score(0, .8, .75, true, false, 7)
	r.Peak(0, .9)
	for i := 0; i < 100; i++ {
		f := make([]int16, 160)
		for j := range f {
			f[j] = int16(1000 + i)
		}
		r.Frame(f, false, 0)
		r.Score(0, .2, .75, false, false, 7)
	}
}

func TestExactPrePostAndPair(t *testing.T) {
	r := fixture(t)
	pre(r)
	trigger(r)
	e := <-r.queue
	if e.PreSamples != 80000 || e.PostSamples != 16000 || len(e.Audio) != 96000 {
		t.Fatal("wrong length")
	}
	if e.Audio[0] != 10 || e.Audio[79999] != 509 || e.Audio[80000] != 1000 ||
		e.Audio[95999] != 1099 {
		t.Fatal("wrong boundary")
	}
	if e.Crossing != .8 || e.HeldPeak != .9 || e.SourceDropped != 7 || e.Settings.Gain != 20 ||
		e.Model.ID != "okay_nabu" {
		t.Fatal(e)
	}
	if err := r.save(e); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(r.dir, e.ID+".wav"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 192044 || binary.LittleEndian.Uint32(b[24:28]) != 16000 {
		t.Fatal("wav")
	}
	meta, _ := os.ReadFile(filepath.Join(r.dir, e.ID+".json"))
	var stored Event
	if json.Unmarshal(meta, &stored) != nil || stored.WavSHA256 == "" || len(stored.Scores) == 0 {
		t.Fatal("paired evidence missing")
	}
	ids, err := r.List()
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	data, pages, err := r.Data(e.ID, "wav", 5)
	if err != nil || pages != 6 || len(data) != 28204 {
		t.Fatal(pages, len(data), err)
	}
	if s, _ := os.Stat(filepath.Join(r.dir, e.ID+".wav")); s.Mode().Perm() != 0600 {
		t.Fatal("permissions")
	}
	if err := r.save(e); err == nil {
		t.Fatal("overwrote existing recording")
	}
}
func TestNoDiskBeforeTrigger(t *testing.T) {
	r := fixture(t)
	pre(r)
	entries, _ := os.ReadDir(r.dir)
	if len(entries) != 0 {
		t.Fatal("continuous recording")
	}
}
func TestDisabledNoRecording(t *testing.T) {
	r := fixture(t)
	r.Stop()
	pre(r)
	trigger(r)
	if r.used != 0 || len(r.queue) != 0 {
		t.Fatal("disabled recording")
	}
}
func TestMuteAndPauseClear(t *testing.T) {
	for _, mute := range []bool{false, true} {
		r := fixture(t)
		pre(r)
		if mute {
			r.Snapshot = func() Settings { return Settings{Muted: true} }
		} else {
			r.Frame(make([]int16, 160), true, 0)
		}
		trigger(r)
		if len(r.queue) != 0 {
			t.Fatal("muted/paused trigger captured")
		}
	}
}
func TestDiscontinuityDropsPending(t *testing.T) {
	r := fixture(t)
	pre(r)
	r.Score(0, .8, .75, true, false, 0)
	r.mu.Lock()
	r.Frame(make([]int16, 160), false, 0)
	r.mu.Unlock()
	r.Frame(make([]int16, 160), false, 0)
	if len(r.pending) != 0 || r.used != 160 || r.dropped.Load() < 2 {
		t.Fatal("gap stitched into recording")
	}
}
func TestStorageTypeAndCapacity(t *testing.T) {
	r := fixture(t)
	if err := os.Symlink("/tmp", filepath.Join(r.dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := r.prepareDir(); err == nil {
		t.Fatal("symlink accepted")
	}
	r = fixture(t)
	f, err := os.Create(filepath.Join(r.dir, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(DiskLimit - 100); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := r.prepareDir(); err == nil {
		t.Fatal("no capacity reservation")
	}
}
func TestPathAndPageRejection(t *testing.T) {
	r := fixture(t)
	pre(r)
	trigger(r)
	e := <-r.queue
	if err := r.save(e); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../secret", e.ID + "/x", "", e.ID + ".wav"} {
		if _, _, err := r.Data(id, "wav", 0); err == nil {
			t.Fatal(id)
		}
	}
	for _, page := range []int{-1, 6, 9} {
		if _, _, err := r.Data(e.ID, "wav", page); err == nil {
			t.Fatal(page)
		}
	}
	if _, _, err := r.Data(e.ID, "psk", 0); err == nil {
		t.Fatal("kind")
	}
}
func TestExpiryAndLimits(t *testing.T) {
	r := fixture(t)
	r.Stop()
	if r.Start(25, 100) == nil || r.Start(1, 101) == nil {
		t.Fatal("range")
	}
	if err := r.Start(1, 1); err != nil {
		t.Fatal(err)
	}
	pre(r)
	trigger(r)
	if r.Status().Enabled || len(r.queue) != 1 {
		t.Fatal("limit")
	}
	pre(r)
	trigger(r)
	if len(r.queue) != 1 {
		t.Fatal("over limit")
	}
	r = fixture(t)
	pre(r)
	r.now = func() time.Time { return time.Unix(5000, 0) }
	trigger(r)
	if len(r.queue) != 0 || r.used != 0 {
		t.Fatal("expired capture")
	}
}
func TestUnknownModelAndNonModelTrace(t *testing.T) {
	r := fixture(t)
	delete(r.models, 0)
	pre(r)
	trigger(r)
	if len(r.queue) != 0 {
		t.Fatal("unpaired model")
	}
	r.Mark("password", 0)
	r.Mark("follow_up", 0)
	e := <-r.queue
	if e.Kind != "follow_up" || len(e.Audio) != 0 {
		t.Fatal("trace")
	}
	if err := r.save(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, e.ID+".wav")); !os.IsNotExist(err) {
		t.Fatal("trace recorded audio")
	}
}
func TestQueueNeverBlocksAudio(t *testing.T) {
	r := fixture(t)
	for i := 0; i < 20; i++ {
		r.Mark("turn_start", 0)
	}
	if len(r.queue) != 4 || r.dropped.Load() != 16 {
		t.Fatal("backpressure")
	}
}
func TestConcurrentStopReadAndWriter(t *testing.T) {
	r := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := r.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 100; i++ {
		r.Frame(make([]int16, 160), false, 0)
		r.Score(0, .1, .75, false, false, 0)
		r.Status()
	}
	r.Mark("turn_start", 0)
	r.Stop()
	cancel()
	wg.Wait()
}
func BenchmarkIdleAudioPath(b *testing.B) {
	r := New(filepath.Join(b.TempDir(), "captures"))
	if err := r.Start(1, 100); err != nil {
		b.Fatal(err)
	}
	f := make([]int16, 160)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Frame(f, false, 0)
		r.Score(0, .1, .75, false, false, 0)
	}
}
