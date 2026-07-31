package wake

import (
	"os"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// installed writes a manifest for each id so the directory reads back as those models. No .tflite is
// written: Installed lists what it finds by manifest and only parses a model when one is there, which is
// enough for what these tests are about.
func installed(t *testing.T, phrases map[string]string) *Library {
	t.Helper()
	dir := t.TempDir()

	for id, phrase := range phrases {
		body := `{"type":"openwakeword","wake_word":"` + phrase + `","model":"` + id + `.tflite"}`
		if err := os.WriteFile(filepath.Join(dir, id+".tflite"), []byte("not a model"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewLibrary(dir)
}

func offer(id, phrase string) esphome.ExternalWakeWord {
	return esphome.ExternalWakeWord{ID: id, Phrase: phrase, URL: "http://ha/" + id + ".json"}
}

// The device advertises both sets, and a purge changes what it says at once. A stale answer here is what
// makes Home Assistant offer models the device no longer has, and every selection of one then fails.
func TestAdvertiseFollowsTheDisk(t *testing.T) {
	l := installed(t, map[string]string{"ours": "Ours"})
	l.Offered([]esphome.ExternalWakeWord{offer("theirs", "Theirs")})

	words, _ := l.Advertise()
	if len(words) != 2 {
		t.Fatalf("advertised %d, want ours and theirs", len(words))
	}

	if gone, _ := l.Purge(nil); gone != 1 {
		t.Fatalf("purged %d models, want 1", gone)
	}

	words, _ = l.Advertise()
	if len(words) != 1 || words[0].ID != "theirs" {
		t.Errorf("after the purge: %+v, want only theirs", words)
	}
}

// Home Assistant keys its selects by phrase, so a phrase is advertised once. Ours wins it, because it
// needs no download.
func TestAdvertiseShadowsRepeatedPhrases(t *testing.T) {
	l := installed(t, map[string]string{"ours": "Computer"})
	l.Offered([]esphome.ExternalWakeWord{
		offer("theirs_computer", "Computer"),
		offer("theirs_other", "Something Else"),
	})

	words, shadowed := l.Advertise()
	if shadowed != 1 {
		t.Errorf("shadowed %d, want 1", shadowed)
	}

	for _, w := range words {
		if w.Phrase == "Computer" && w.ID != "ours" {
			t.Errorf("advertised %q for Computer, want ours", w.ID)
		}
	}
}

// Being shadowed only decides what Home Assistant is shown. The URL has to survive, because Home
// Assistant selects by id and can ask for a word it was never shown — which is how one becomes
// permanently unobtainable.
func TestShadowedOffersKeepTheirURL(t *testing.T) {
	l := installed(t, map[string]string{"ours": "Computer"})
	l.Offered([]esphome.ExternalWakeWord{offer("theirs_computer", "Computer")})

	if _, shadowed := l.Advertise(); shadowed != 1 {
		t.Fatal("expected the offer to be shadowed")
	}
	if got := l.offer("theirs_computer").URL; got == "" {
		t.Error("a shadowed offer lost its URL, so nothing can fetch it")
	}
}

// Two offers sharing a phrase resolve the same way every time, rather than by map order.
func TestAdvertiseIsStableAcrossCalls(t *testing.T) {
	l := installed(t, nil)
	l.Offered([]esphome.ExternalWakeWord{
		offer("b_second", "Same"),
		offer("a_first", "Same"),
	})

	first, _ := l.Advertise()
	for range 20 {
		again, _ := l.Advertise()
		if len(again) != len(first) || again[0].ID != first[0].ID {
			t.Fatalf("advertised %+v then %+v", first, again)
		}
	}
	if first[0].ID != "a_first" {
		t.Errorf("advertised %q, want the first by id", first[0].ID)
	}
}
