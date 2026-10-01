package sendspin

// track is what the group is playing, as the metadata role reports it.
type track struct {
	Title  string
	Artist string
	Album  string
}

// merge applies one metadata update. The wire is tristate and all three cases mean something
// different: a key that is absent leaves what was there, a key that is null clears it, and a key with
// a value sets it. Treating absent as empty would blank the title on every progress update.
func (t track) merge(m *metadataState) track {
	t.Title = field(m, "title", m.Title, t.Title)
	t.Artist = field(m, "artist", m.Artist, t.Artist)
	t.Album = field(m, "album", m.Album, t.Album)
	return t
}

func (t track) empty() bool { return t == track{} }

func field(m *metadataState, key string, now *string, was string) string {
	if !m.has(key) {
		return was
	}
	if now == nil {
		return ""
	}
	return *now
}
