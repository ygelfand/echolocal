package config

// Update is which releases the device follows. The channel is a name rather than a URL: where each
// one points is compiled in, so this cannot be used to send the device somewhere else for its next
// binary.
type Update struct {
	Channel string `json:"channel"`

	// LastVersion is the build Home Assistant was last told about. It moves only once the telling has
	// happened, so a version that changed while nothing was listening is still reported later.
	LastVersion string `json:"last_version"`
}

const DefaultChannel = "stable"

func defaultUpdate() Update {
	return Update{Channel: DefaultChannel}
}

type UpdateWriter struct{ st *Store }

func (w UpdateWriter) Channel(v string) error {
	return w.st.Update(func(c *Config) { c.Update.Channel = v })
}

func (w UpdateWriter) LastVersion(v string) error {
	return w.st.Update(func(c *Config) { c.Update.LastVersion = v })
}
