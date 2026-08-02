package config

// Update is which releases the device follows. The channel is a name rather than a URL: where each
// one points is compiled in, so this cannot be used to send the device somewhere else for its next
// binary.
type Update struct {
	Channel string `json:"channel"`

	// Status is how the last attempt ended. Saved because the process that learns of a rollback is
	// the one that just started.
	Status string `json:"status"`
}

const (
	DefaultChannel = "stable"
	DefaultStatus  = "never updated"
)

func defaultUpdate() Update {
	return Update{Channel: DefaultChannel, Status: DefaultStatus}
}

type UpdateWriter struct{ st *Store }

func (w UpdateWriter) Channel(v string) error {
	return w.st.Update(func(c *Config) { c.Update.Channel = v })
}

func (w UpdateWriter) Status(v string) error {
	return w.st.Update(func(c *Config) { c.Update.Status = v })
}
