// Package recording keeps the last few turns as audio, and serves them to Home Assistant.
//
// What is kept is exactly what was sent to be transcribed, so listening back answers the question a
// transcript raises: whether the device mis-heard, or heard correctly and the pipeline did the rest.
package recording

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
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
// pipeline that never closed the run is not, and the memory is not there to hold it.
const Longest = 30 * mic.Rate * 2

// Keep is how many recordings an assistant holds, and the most it can be set to. One is the useful
// default: the reason to listen back is almost always the turn that just went wrong.
const (
	KeepDefault = 1
	KeepMost    = 10
)

type held struct {
	id    string
	slot  int
	audio []byte
}

type Store struct {
	mu   sync.Mutex
	kept []held

	// open is the turn being recorded, by id, so a frame knows where to go without the caller holding
	// anything. Empty between turns.
	open string
	slot int

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
				shared.prune()
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
	s.prune()
}

// Actions is how Home Assistant fetches one. Paged because a recording is far larger than a message.
func (s *Store) Actions() []*esphome.Action {
	return []*esphome.Action{
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

// Opens starts recording a turn. An assistant set to keep none records nothing, which is how somebody
// turns it off for one of them without touching the other.
func (s *Store) Opens(id string, slot int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if keeps(slot) <= 0 {
		s.open = ""
		return
	}

	s.open, s.slot = id, slot
	s.kept = append(s.kept, held{id: id, slot: slot})
	s.trim()
}

// Frame takes audio for whichever turn is open. Called from the streamer, which does not know or care
// whether anything is being kept.
func (s *Store) Frame(pcm []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.open == "" || len(s.kept) == 0 {
		return
	}

	at := &s.kept[len(s.kept)-1]
	if at.id != s.open || len(at.audio) >= Longest {
		return
	}
	at.audio = append(at.audio, pcm...)
}

// Closes stops recording. A turn that gathered nothing is dropped rather than kept as an empty entry
// somebody could press play on.
func (s *Store) Closes() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.open == "" {
		return
	}
	s.open = ""

	if n := len(s.kept); n > 0 && len(s.kept[n-1].audio) == 0 {
		s.kept = s.kept[:n-1]
	}
}

// Seconds is how long a turn's recording runs, and zero when there is none, which is what a turn
// reports so nothing offers to play what was never kept.
func (s *Store) Seconds(id string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, one := range s.kept {
		if one.id == id {
			return float64(len(one.audio)) / float64(mic.Rate*2)
		}
	}
	return 0
}

// Answer is one page of a recording, as the frontend expects it.
type Answer struct {
	ID    string `json:"id"`
	Page  int    `json:"page"`
	Pages int    `json:"pages"`
	MIME  string `json:"mime"`
	Data  string `json:"data"`
}

// page wraps the audio as a WAV and hands back one slice of it. The header goes on the whole thing
// before paging, so the first page opens as audio on its own and the rest append to it.
func (s *Store) page(id string, page int) (*Answer, error) {
	s.mu.Lock()
	audio := []byte(nil)
	for _, one := range s.kept {
		if one.id == id {
			audio = one.audio
			break
		}
	}
	s.mu.Unlock()

	if audio == nil {
		return nil, fmt.Errorf("no recording for turn %s", id)
	}

	whole := wav(audio)
	pages := (len(whole) + Page - 1) / Page
	if page < 0 || page >= pages {
		return nil, fmt.Errorf("page %d of %d", page, pages)
	}

	end := min((page+1)*Page, len(whole))
	return &Answer{
		ID:    id,
		Page:  page,
		Pages: pages,
		MIME:  "audio/wav",
		Data:  base64.StdEncoding.EncodeToString(whole[page*Page : end]),
	}, nil
}

// keeps is how many recordings an assistant is set to hold.
func keeps(slot int) int {
	words := config.Get().Wake.Words
	if slot < 0 || slot >= len(words) {
		return 0
	}
	return max(words[slot].Recordings, 0)
}

// trim keeps the newest few for each assistant, counted per assistant so a busy one cannot push out
// what the other just recorded.
func (s *Store) trim() {
	seen := map[int]int{}
	out := make([]held, 0, len(s.kept))

	for i := len(s.kept) - 1; i >= 0; i-- {
		one := s.kept[i]
		if seen[one.slot] >= keeps(one.slot) {
			continue
		}
		seen[one.slot]++
		out = append(out, one)
	}

	// Reversed back, so the newest stays last and Frame can find the open turn at the end.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	s.kept = out
}

func (s *Store) prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trim()
}

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
