// Package recording keeps the last few turns as audio on disk, and serves them to Home Assistant.
//
// What is kept is exactly what was sent to be transcribed, so listening back answers the question a
// transcript raises: whether the device mis-heard, or heard correctly and the pipeline did the rest.
//
// Each kept turn is two files named by its id: the WAV, and a metadata sidecar. A turn is complete
// only once both are written, which is what lets pruning tell a finished recording from one a crash
// left half-written.
package recording

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/layout"
)

// Slots is how many assistants there are to keep recordings for. Named here rather than taken from the
// wake feature, which serves the turns this records and cannot depend on it back.
const Slots = 2

func init() {
	component.Register(component.Device, Get(), component.Order(20))
}

// Page is how many raw bytes go in one answer. The transport caps a message at 65515 bytes and base64
// costs a third again, so 32 KiB encodes to about 43.7 KiB and leaves room for the rest of the reply.
const Page = 32 * 1024

// Longest is how much of one turn is kept. A request is seconds long; a microphone left open by a
// pipeline that never closed the run is not, and the memory holding a turn until it closes is not
// there for that.
const Longest = 30 * mic.Rate * 2

// KeepMost is the most an assistant can be set to hold. A fresh device keeps none: recording what
// someone said is off until they turn it on.
const KeepMost = 10

const (
	wavExt  = ".wav"
	metaExt = ".json"
)

// meta is what is stored beside a recording: enough to prune it and to report its length after a
// restart, without re-reading the audio.
type meta struct {
	Slot  int   `json:"slot"`
	Bytes int   `json:"bytes"`
	At    int64 `json:"at"`
}

type Store struct {
	// mu guards the turn being recorded. open is its id, empty between turns; buf gathers its audio.
	mu   sync.Mutex
	open string
	slot int
	buf  []byte

	// disk serialises writing a recording against pruning, so a sweep cannot delete a turn in the
	// instant between its two files being written.
	disk sync.Mutex

	// One per assistant, because how much of an assistant is worth keeping is about what it is for.
	keep []*esphome.Number
}

var (
	once   sync.Once
	shared *Store
)

func Get() *Store {
	once.Do(func() {
		shared = &Store{}

		for n := range Slots {
			slot := n
			number := &esphome.Number{
				Base: esphome.Base{
					ObjectID: fmt.Sprintf("keep_recordings_%d", slot+1),
					Name:     "Recordings kept",
					Icon:     "mdi:record-rec",
					DeviceID: component.AssistantDevice(slot),
					Category: esphome.CategoryConfig,
				},
				Min: 0, Max: KeepMost, Step: 1,
				Mode: esphome.NumberBox,
			}

			number.OnCommand = func(v float32) {
				number.Set(v)
				if err := config.Set().Wake(slot).Recordings(int(v)); err != nil {
					slog.Error("saving how many recordings to keep failed", "slot", slot+1, "err", err)
				}
				shared.Prune()
			}
			shared.keep = append(shared.keep, number)
		}
	})
	return shared
}

func (s *Store) Name() string { return "recording" }

func (s *Store) Entities() []esphome.Entity {
	out := make([]esphome.Entity, 0, len(s.keep))
	for _, number := range s.keep {
		out = append(out, number)
	}
	return out
}

func (s *Store) Restore(c config.Config) {
	for slot, number := range s.keep {
		count := 0
		if slot < len(c.Wake.Words) {
			count = c.Wake.Words[slot].Recordings
		}
		number.Set(float32(count))
	}
	s.Prune()
}

// Actions is how Home Assistant reaches a recording: recordings lists what is held, turn_audio fetches
// one, paged because a recording is far larger than a message.
func (s *Store) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name:    "recordings",
			Answers: true,
			Run: func(esphome.Call) (any, error) {
				return s.available()
			},
		},
		{
			Name:    "turn_audio",
			Args:    []esphome.Arg{{Name: "id", Type: esphome.ArgString}, {Name: "page", Type: esphome.ArgInt}},
			Answers: true,
			Run: func(c esphome.Call) (any, error) {
				return s.page(c.String("id"), c.Int("page"))
			},
		},
	}
}

// Available is the ids the device still holds audio for, so the card offers to play only those rather
// than every turn that once had a recording. A turn's event says it had audio for as long as the event
// lives; the file behind it does not.
type Available struct {
	Version int      `json:"version"`
	IDs     []string `json:"ids"`
}

func (s *Store) available() (*Available, error) {
	out := &Available{Version: 1, IDs: []string{}}

	entries, err := os.ReadDir(layout.RecordingDir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}

	// Keyed off the metadata, so a WAV still being written or left by a crash is not offered as ready.
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, metaExt) {
			out.IDs = append(out.IDs, strings.TrimSuffix(name, metaExt))
		}
	}
	return out, nil
}

// Opens starts recording a turn. An assistant set to keep none records nothing, which is how somebody
// turns it off for one of them without touching the other.
func (s *Store) Opens(id string, slot int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if keeps(slot) <= 0 {
		s.open, s.buf = "", nil
		return
	}
	s.open, s.slot, s.buf = id, slot, nil
}

// Frame takes audio for whichever turn is open. Called from the streamer, which does not know or care
// whether anything is being kept.
func (s *Store) Frame(pcm []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.open == "" || len(s.buf) >= Longest {
		return
	}
	s.buf = append(s.buf, pcm...)
}

// Closes stops recording and writes what was gathered. A turn that gathered nothing is dropped rather
// than saved as an empty file somebody could press play on.
func (s *Store) Closes() {
	s.mu.Lock()
	id, slot, buf := s.open, s.slot, s.buf
	s.open, s.buf = "", nil
	s.mu.Unlock()

	if id == "" || len(buf) == 0 {
		return
	}

	s.disk.Lock()
	defer s.disk.Unlock()

	if err := write(id, slot, buf); err != nil {
		slog.Error("saving a recording failed", "id", id, "err", err)
		return
	}
	s.prune()
}

// Seconds is how long a turn's recording runs, and zero when there is none, which is what a turn
// reports so nothing offers to play what was never kept. Read from the sidecar so it is still right
// for a recording that outlived the process that made it.
func (s *Store) Seconds(id string) float64 {
	m, err := readMeta(id)
	if err != nil {
		return 0
	}
	return float64(m.Bytes) / float64(mic.Rate*2)
}

// Answer is one page of a recording, as the frontend expects it. Version is the contract the reader
// checks: an answer without it is rejected, so it is not optional.
type Answer struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Page    int    `json:"page"`
	Pages   int    `json:"pages"`
	MIME    string `json:"mime"`
	Data    string `json:"data"`
}

// page reads the WAV and hands back one slice of it. The whole file is already a WAV, so the first
// page opens as audio on its own and the rest append to it.
func (s *Store) page(id string, page int) (*Answer, error) {
	whole, err := os.ReadFile(wavPath(id))
	if err != nil {
		return nil, fmt.Errorf("no recording for turn %s", id)
	}

	pages := (len(whole) + Page - 1) / Page
	if page < 0 || page >= pages {
		return nil, fmt.Errorf("page %d of %d", page, pages)
	}

	end := min((page+1)*Page, len(whole))
	return &Answer{
		Version: 1,
		ID:      id,
		Page:    page,
		Pages:   pages,
		MIME:    "audio/wav",
		Data:    base64.StdEncoding.EncodeToString(whole[page*Page : end]),
	}, nil
}

// Prune keeps the newest few for each assistant and clears away the rest, so a slot set lower, a
// recording left by a restart, or a half-written file a crash left behind does not linger.
func (s *Store) Prune() {
	s.disk.Lock()
	defer s.disk.Unlock()
	s.prune()
}

// prune assumes the disk lock is held.
func (s *Store) prune() {
	entries, err := os.ReadDir(layout.RecordingDir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		slog.Error("reading the recordings failed", "dir", layout.RecordingDir, "err", err)
		return
	}

	type rec struct {
		id string
		at int64
	}
	bySlot := map[int][]rec{}
	complete := map[string]bool{}
	wavs := []string{}

	for _, e := range entries {
		switch name := e.Name(); {
		case strings.HasSuffix(name, metaExt):
			id := strings.TrimSuffix(name, metaExt)
			m, err := readMeta(id)
			if err != nil {
				continue
			}
			complete[id] = true
			bySlot[m.Slot] = append(bySlot[m.Slot], rec{id, m.At})
		case strings.HasSuffix(name, wavExt):
			wavs = append(wavs, strings.TrimSuffix(name, wavExt))
		}
	}

	for slot, recs := range bySlot {
		sort.Slice(recs, func(i, j int) bool { return recs[i].at > recs[j].at })
		for i, r := range recs {
			if i >= keeps(slot) {
				remove(r.id)
			}
		}
	}

	// A WAV without its sidecar is a turn a crash cut short. Pruning runs minutes apart while a close
	// writes both back to back, so this only ever catches a genuine leftover.
	for _, id := range wavs {
		if !complete[id] {
			remove(id)
		}
	}
}

// keeps is how many recordings an assistant is set to hold.
func keeps(slot int) int {
	words := config.Get().Wake.Words
	if slot < 0 || slot >= len(words) {
		return 0
	}
	return max(words[slot].Recordings, 0)
}

func write(id string, slot int, pcm []byte) error {
	if err := os.MkdirAll(layout.RecordingDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(wavPath(id), wav(pcm), 0o644); err != nil {
		return err
	}

	blob, err := json.Marshal(meta{Slot: slot, Bytes: len(pcm), At: time.Now().Unix()})
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(id), blob, 0o644)
}

func remove(id string) {
	for _, path := range []string{wavPath(id), metaPath(id)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Error("removing a recording failed", "path", path, "err", err)
		}
	}
}

func readMeta(id string) (meta, error) {
	blob, err := os.ReadFile(metaPath(id))
	if err != nil {
		return meta{}, err
	}
	var m meta
	return m, json.Unmarshal(blob, &m)
}

func wavPath(id string) string  { return filepath.Join(layout.RecordingDir, id+wavExt) }
func metaPath(id string) string { return filepath.Join(layout.RecordingDir, id+metaExt) }

// wav is a 16-bit mono header for what the microphone gives, which is the only format kept.
func wav(pcm []byte) []byte {
	const header = 44
	out := make([]byte, 0, header+len(pcm))

	put32 := func(v uint32) {
		out = append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	put16 := func(v uint16) { out = append(out, byte(v), byte(v>>8)) }

	out = append(out, "RIFF"...)
	put32(uint32(header - 8 + len(pcm)))
	out = append(out, "WAVEfmt "...)
	put32(16)
	put16(1)
	put16(1)
	put32(mic.Rate)
	put32(mic.Rate * 2)
	put16(2)
	put16(16)
	out = append(out, "data"...)
	put32(uint32(len(pcm)))

	return append(out, pcm...)
}
