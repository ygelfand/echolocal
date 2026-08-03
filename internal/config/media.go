package config

// Media is what happens to a track that is playing when the device is spoken to.
type Media struct {
	// OnTurn is whether music carries on quietly under a turn or stops for it.
	OnTurn OnTurn `json:"on_turn"`

	// DuckDB is how far down, in dB, when it carries on. Negative.
	DuckDB int `json:"duck_db"`
}

// OnTurn is what a turn does to music.
type OnTurn string

const (
	// OnTurnDuck lowers the track and keeps playing it.
	OnTurnDuck OnTurn = "duck"

	// OnTurnPause stops it and picks up where it left off.
	OnTurnPause OnTurn = "pause"
)

// Label is how the setting is shown.
func (o OnTurn) Label() string {
	switch o {
	case OnTurnDuck:
		return "Duck"
	case OnTurnPause:
		return "Pause"
	}
	return string(o)
}

const (
	DefaultOnTurn = OnTurnDuck

	// Far enough down that a reply wins, not so far that the track sounds stopped.
	DefaultDuckDB = -15
)

func defaultMedia() Media {
	return Media{
		OnTurn: DefaultOnTurn,
		DuckDB: DefaultDuckDB,
	}
}

type MediaWriter struct{ st *Store }

func (w MediaWriter) OnTurn(v OnTurn) error {
	return w.st.Update(func(c *Config) { c.Media.OnTurn = v })
}

func (w MediaWriter) DuckDB(db int) error {
	return w.st.Update(func(c *Config) { c.Media.DuckDB = db })
}
