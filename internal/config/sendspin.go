package config

// Sendspin is the room's part of a synchronized stream. A server discovers it and dials in, so there
// is no address to set.
type Sendspin struct {
	Enabled bool `json:"enabled"`
}

func defaultSendspin() Sendspin { return Sendspin{Enabled: true} }

type SendspinWriter struct{ st *Store }

func (w SendspinWriter) Enabled(v bool) error {
	return w.st.Update(func(c *Config) { c.Sendspin.Enabled = v })
}
