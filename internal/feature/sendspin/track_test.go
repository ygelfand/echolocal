package sendspin

import (
	"encoding/json"
	"testing"
)

// Decoded rather than built: absent and null are the same nil pointer in Go, and only the decoder
// records which of the two arrived.
func decode(t *testing.T, raw string) *metadataState {
	t.Helper()

	var m metadataState
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return &m
}

func TestTrackMerge(t *testing.T) {
	was := track{Title: "Teardrop", Artist: "Massive Attack", Album: "Mezzanine"}

	for _, c := range []struct {
		name string
		raw  string
		want track
	}{
		{
			name: "an absent key leaves what was there",
			raw:  `{"progress":{"track_progress":1000}}`,
			want: was,
		},
		{
			name: "a null clears",
			raw:  `{"artist":null}`,
			want: track{Title: "Teardrop", Album: "Mezzanine"},
		},
		{
			name: "a value sets",
			raw:  `{"title":"Angel"}`,
			want: track{Title: "Angel", Artist: "Massive Attack", Album: "Mezzanine"},
		},
		{
			name: "a whole track at once",
			raw:  `{"title":"Rez","artist":"Underworld","album":"Second Toughest"}`,
			want: track{Title: "Rez", Artist: "Underworld", Album: "Second Toughest"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := was.merge(decode(t, c.raw)); got != c.want {
				t.Errorf("merge(%s) = %+v, want %+v", c.raw, got, c.want)
			}
		})
	}
}

func TestTrackEmpty(t *testing.T) {
	if !(track{}).empty() {
		t.Error("the zero track is not empty")
	}
	if (track{Title: "Rez"}).empty() {
		t.Error("a track with a title is empty")
	}
}
