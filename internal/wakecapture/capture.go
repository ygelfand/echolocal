// Package wakecapture is a bounded, local-only wake diagnostic side channel.
// It never changes wake decisions and never performs disk IO on the audio path.
package wakecapture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const Rate = 16000
const Pre = 5 * Rate
const Post = Rate
const MaxEvents = 100
const MaxHours = 24
const PageSize = 32768
const DiskLimit = 64 * 1024 * 1024

type Settings struct {
	Mic         string `json:"microphone"`
	Gain        int    `json:"gain_db"`
	Leveling    bool   `json:"leveling"`
	AEC         bool   `json:"aec"`
	Sensitivity int    `json:"sensitivity"`
	Playing     bool   `json:"playing_at_detection"`
	Muted       bool   `json:"muted"`
}
type Point struct {
	Sample     uint64  `json:"sample"`
	Slot       int     `json:"slot"`
	Score      float64 `json:"score"`
	Cutoff     float64 `json:"cutoff"`
	Suppressed bool    `json:"suppressed"`
}
type Model struct {
	ID          string `json:"id"`
	Engine      string `json:"engine"`
	SHA256      string `json:"sha256"`
	Window      int    `json:"window"`
	FeatureStep int    `json:"feature_step_ms"`
}
type Event struct {
	Version         int      `json:"version"`
	ID              string   `json:"id"`
	Boot            string   `json:"boot_id"`
	Kind            string   `json:"kind"`
	At              int64    `json:"at_unix_ms"`
	Trigger         uint64   `json:"trigger_sample"`
	Slot            int      `json:"slot"`
	Model           Model    `json:"model"`
	Settings        Settings `json:"settings"`
	Crossing        float64  `json:"crossing"`
	HeldPeak        float64  `json:"hold_peak"`
	Peak            float64  `json:"post_peak"`
	Cutoff          float64  `json:"cutoff"`
	SourceDropped   uint64   `json:"source_dropped"`
	ObserverDropped uint64   `json:"observer_dropped"`
	PreSamples      int      `json:"pre_samples"`
	PostSamples     int      `json:"post_samples"`
	Scores          []Point  `json:"scores"`
	WavSHA256       string   `json:"wav_sha256"`
	Audio           []int16  `json:"-"`
}
type Status struct {
	Version  int    `json:"version"`
	Boot     string `json:"boot_id"`
	Enabled  bool   `json:"enabled"`
	Until    int64  `json:"until_unix_ms"`
	Captured int    `json:"captured"`
	Saved    uint64 `json:"saved"`
	Dropped  uint64 `json:"dropped"`
	Errors   uint64 `json:"errors"`
	Pending  int    `json:"pending"`
	Limit    int    `json:"limit"`
}
type Recorder struct {
	dir                      string
	boot                     string
	now                      func() time.Time
	Snapshot                 func() Settings
	mu                       sync.Mutex
	disk                     sync.Mutex
	enabled                  bool
	until                    time.Time
	limit, captured, seq     int
	marks                    int
	sample                   uint64
	sourceDropped            uint64
	totalSamples             atomic.Uint64
	ring                     [Pre]int16
	head, used               int
	points                   []Point
	pending                  []*Event
	models                   map[int]Model
	queue                    chan *Event
	writes                   atomic.Int32
	saved, dropped, failures atomic.Uint64
}

func New(dir string) *Recorder {
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err != nil {
		panic("wake observer entropy unavailable")
	}
	return &Recorder{dir: dir, boot: hex.EncodeToString(seed[:]), now: time.Now,
		models: map[int]Model{}, queue: make(chan *Event, 4)}
}
func (r *Recorder) Start(hours, limit int) error {
	if hours < 1 || hours > MaxHours || limit < 1 || limit > MaxEvents {
		return errors.New("observation_range")
	}
	r.disk.Lock()
	err := r.prepareDir()
	r.disk.Unlock()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enabled || len(r.pending) > 0 || r.writes.Load() > 0 {
		return errors.New("observation_already_active")
	}
	r.head, r.used, r.captured, r.marks = 0, 0, 0, 0
	clear(r.ring[:])
	r.points = nil
	r.enabled = true
	r.until = r.now().Add(time.Duration(hours) * time.Hour)
	r.limit = limit
	return nil
}
func (r *Recorder) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = false
	r.resetAudio()
}

// resetAudio requires mu. Completed events already queued may still be saved.
func (r *Recorder) resetAudio() {
	r.used, r.head = 0, 0
	r.points = nil
	for _, event := range r.pending {
		clear(event.Audio)
	}
	r.dropped.Add(uint64(len(r.pending)))
	clear(r.pending)
	r.pending = nil
	clear(r.ring[:])
}

func (r *Recorder) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{
		Version: 1,
		Boot:    r.boot,
		Enabled: r.enabled && r.now().Before(r.until),
		Until: func() int64 {
			if r.until.IsZero() {
				return 0
			}
			return r.until.UnixMilli()
		}(),
		Captured: r.captured,
		Saved:    r.saved.Load(),
		Dropped:  r.dropped.Load(),
		Errors:   r.failures.Load(),
		Pending:  len(r.pending),
		Limit:    r.limit,
	}
}
func (r *Recorder) Model(slot int, id, engine, path string, window, step int) {
	// Only called during model loading, never on the scoring/audio path.
	b, err := os.ReadFile(path)
	m := Model{ID: id, Engine: engine, Window: window, FeatureStep: step}
	if err == nil {
		sum := sha256.Sum256(b)
		m.SHA256 = hex.EncodeToString(sum[:])
	}
	r.mu.Lock()
	// Never pair pre-roll or pending audio from a previous model with a new one.
	r.resetAudio()
	r.models[slot] = m
	r.mu.Unlock()
}
func (r *Recorder) Frame(frame []int16, paused bool, sourceDropped uint64) {
	next := r.totalSamples.Add(uint64(len(frame)))
	if !r.mu.TryLock() {
		r.dropped.Add(1)
		return
	}
	defer r.mu.Unlock()
	if next != r.sample+uint64(len(frame)) || sourceDropped != r.sourceDropped {
		r.resetAudio()
	}
	r.sample = next
	r.sourceDropped = sourceDropped
	if !r.enabled && len(r.pending) == 0 {
		return
	}
	if paused || !r.now().Before(r.until) {
		r.resetAudio()
		if !r.now().Before(r.until) {
			r.enabled = false
		}
		return
	}
	pending := r.pending
	remaining := pending[:0]
	for _, e := range r.pending {
		n := min(Post-e.PostSamples, len(frame))
		e.Audio = append(e.Audio, frame[:n]...)
		e.PostSamples += n
		if e.PostSamples == Post {
			e.ObserverDropped = r.dropped.Load()
			r.enqueue(e)
		} else {
			remaining = append(remaining, e)
		}
	}
	clear(pending[len(remaining):])
	r.pending = remaining
	if !r.enabled {
		r.used, r.head = 0, 0
		r.points = nil
		clear(r.ring[:])
		return
	}
	for _, v := range frame {
		r.ring[r.head] = v
		r.head = (r.head + 1) % Pre
		if r.used < Pre {
			r.used++
		}
	}
}

func (r *Recorder) Score(
	slot int,
	score, cutoff float64,
	crossing, suppressed bool,
	sourceDropped uint64,
) {
	if slot < 0 || slot >= 2 {
		return
	}
	if !r.mu.TryLock() {
		r.dropped.Add(1)
		return
	}
	defer r.mu.Unlock()
	if (!r.enabled && len(r.pending) == 0) || !r.now().Before(r.until) {
		return
	}
	p := Point{r.sample, slot + 1, score, cutoff, suppressed}
	r.points = append(r.points, p)
	cut := 0
	for cut < len(r.points) && (r.points[cut].Sample+Pre < r.sample || len(r.points)-cut > 512) {
		cut++
	}
	r.points = r.points[cut:]
	for _, e := range r.pending {
		if e.Slot == slot+1 {
			e.Peak = max(e.Peak, score)
			e.Scores = append(e.Scores, p)
		}
	}
	if !crossing || !r.enabled {
		return
	}
	if len(r.pending) >= 4 || r.used == 0 {
		r.dropped.Add(1)
		return
	}
	settings := Settings{}
	if r.Snapshot != nil {
		settings = r.Snapshot()
	}
	if settings.Muted {
		return
	}
	if r.models[slot].SHA256 == "" {
		r.dropped.Add(1)
		return
	}
	r.seq++
	r.captured++
	e := &Event{
		Version: 1,
		ID:      fmt.Sprintf("%s-%08x", r.boot, r.seq),
		Boot:    r.boot,
		Kind:    "model",
		At: r.now().
			UnixMilli(),
		Trigger:       r.sample,
		Slot:          slot + 1,
		Model:         r.models[slot],
		Settings:      settings,
		Crossing:      score,
		Peak:          score,
		Cutoff:        cutoff,
		SourceDropped: sourceDropped,
		PreSamples:    r.used,
		Scores:        append([]Point(nil), r.points...),
		Audio:         make([]int16, r.used, r.used+Post),
	}
	start := (r.head - r.used + Pre) % Pre
	for i := range e.Audio {
		e.Audio[i] = r.ring[(start+i)%Pre]
	}
	r.pending = append(r.pending, e)
	if r.captured >= r.limit {
		r.enabled = false
	}
}
func (r *Recorder) Peak(slot int, peak float64) {
	if !r.mu.TryLock() {
		r.dropped.Add(1)
		return
	}
	defer r.mu.Unlock()
	for _, e := range r.pending {
		if e.Slot == slot+1 {
			e.HeldPeak = peak
		}
	}
}
func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Stop()
			for {
				select {
				case e := <-r.queue:
					clear(e.Audio)
					r.writes.Add(-1)
					r.dropped.Add(1)
				default:
					return nil
				}
			}
		case <-ticker.C:
			r.mu.Lock()
			expired := (r.enabled || len(r.pending) > 0) && !r.now().Before(r.until)
			r.mu.Unlock()
			if expired {
				r.Stop()
			}
		case event := <-r.queue:
			if err := r.save(event); err != nil {
				r.failures.Add(1)
				r.Stop()
			} else {
				r.saved.Add(1)
			}
			clear(event.Audio)
			r.writes.Add(-1)
		}
	}
}
func wav(samples []int16) []byte {
	b := make([]byte, 44+len(samples)*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], Rate)
	binary.LittleEndian.PutUint32(b[28:], Rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(samples)*2))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}
func (r *Recorder) prepareDir() error {
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return errors.New("observation_directory")
	}
	s, err := os.Lstat(r.dir)
	if err != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return errors.New("observation_directory")
	}
	if err = os.Chmod(r.dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return err
	}
	var size int64
	for _, e := range entries {
		i, err := e.Info()
		if err != nil || !i.Mode().IsRegular() {
			return errors.New("observation_file_type")
		}
		size += i.Size()
	}
	if size+44+2*(Pre+Post)+131072 > DiskLimit || len(entries)+2 > 1000 {
		return errors.New("observation_storage_full")
	}
	return nil
}
func exclusive(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	return f.Sync()
}
func (r *Recorder) save(e *Event) error {
	r.disk.Lock()
	defer r.disk.Unlock()
	if err := r.prepareDir(); err != nil {
		return err
	}
	if e.Kind != "model" {
		meta, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return exclusive(filepath.Join(r.dir, e.ID+".json"), meta)
	}
	b := wav(e.Audio)
	sum := sha256.Sum256(b)
	e.WavSHA256 = hex.EncodeToString(sum[:])
	meta, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(meta) > 131072 || len(b) > 44+2*(Pre+Post) {
		return errors.New("observation_size")
	}
	if err = exclusive(filepath.Join(r.dir, e.ID+".wav"), b); err != nil {
		return err
	}
	return exclusive(filepath.Join(r.dir, e.ID+".json"), meta)
}

var validID = regexp.MustCompile(`^[a-f0-9]{16}-[a-f0-9]{8}$`)

func (r *Recorder) List() ([]string, error) {
	r.disk.Lock()
	defer r.disk.Unlock()
	root, err := r.openRoot()
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && filepath.Ext(name) == ".json" &&
			validID.MatchString(name[:len(name)-5]) {
			ids = append(ids, name[:len(name)-5])
		}
	}
	sort.Strings(ids)
	return ids, nil
}
func (r *Recorder) Data(id, kind string, page int) ([]byte, int, error) {
	if !validID.MatchString(id) || (kind != "wav" && kind != "json") || page < 0 || page > 8 {
		return nil, 0, errors.New("observation_request")
	}
	r.disk.Lock()
	defer r.disk.Unlock()
	root, err := r.openRoot()
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	name := id + "." + kind
	s, err := root.Lstat(name)
	if err != nil || !s.Mode().IsRegular() || s.Size() > 44+2*(Pre+Post) {
		return nil, 0, errors.New("observation_file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(45+2*(Pre+Post))))
	if err != nil {
		return nil, 0, err
	}
	if len(b) > 44+2*(Pre+Post) {
		return nil, 0, errors.New("observation_size")
	}
	pages := (len(b) + PageSize - 1) / PageSize
	if page >= pages {
		return nil, pages, errors.New("observation_page")
	}
	return b[page*PageSize : min((page+1)*PageSize, len(b))], pages, nil
}

// Mark stores only a fixed event category: never transcript, reply or credentials.
func (r *Recorder) Mark(kind string, slot int) {
	switch kind {
	case "api_subscribed",
		"api_unsubscribed",
		"wake_requested",
		"button_tap",
		"button_hold",
		"turn_start",
		"follow_up":
	default:
		return
	}
	if !r.mu.TryLock() {
		r.dropped.Add(1)
		return
	}
	defer r.mu.Unlock()
	if !r.enabled || !r.now().Before(r.until) || r.marks >= 256 {
		return
	}
	r.seq++
	r.marks++
	e := &Event{
		Version: 1,
		ID:      fmt.Sprintf("%s-%08x", r.boot, r.seq),
		Boot:    r.boot,
		Kind:    kind,
		At:      r.now().UnixMilli(),
		Slot:    slot + 1,
		Trigger: r.sample,
	}
	r.enqueue(e)
}

// enqueue counts both queued and in-flight writes so Start cannot race a
// previous session's writer. Called with mu held; it never waits for storage.
func (r *Recorder) enqueue(e *Event) {
	r.writes.Add(1)
	select {
	case r.queue <- e:
	default:
		r.writes.Add(-1)
		clear(e.Audio)
		r.dropped.Add(1)
	}
}

// openRoot confines reads even if a path is replaced while a request is in flight.
func (r *Recorder) openRoot() (*os.Root, error) {
	s, err := os.Lstat(r.dir)
	if err != nil {
		return nil, err
	}
	if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("observation_directory")
	}
	return os.OpenRoot(r.dir)
}
