package config

// Sendspin is the room's part of a synchronized stream. A server discovers it and dials in, so there
// is no address to set.
//
// UnpairedAccess is whether a server this room has never paired with may play through it. On, the
// room behaves as it always has: any Sendspin server on the network can use it once its operator
// approves the room on their side. Off, a server has to pair first, and one that has not is told so
// and turned away.
//
// OutputDelayMs is how much earlier this room plays than the server's timestamps say, to line it up
// with the others by ear. The server's operator sets it from the server's own player settings; the spec
// has the room keep it across restarts.
type Sendspin struct {
	Enabled        bool `json:"enabled"`
	UnpairedAccess bool `json:"unpaired_access"`
	OutputDelayMs  int  `json:"output_delay_ms"`
}

func defaultSendspin() Sendspin { return Sendspin{Enabled: true, UnpairedAccess: true} }

type SendspinWriter struct{ st *Store }

func (w SendspinWriter) Enabled(v bool) error {
	return w.st.Update(func(c *Config) { c.Sendspin.Enabled = v })
}

func (w SendspinWriter) UnpairedAccess(v bool) error {
	return w.st.Update(func(c *Config) { c.Sendspin.UnpairedAccess = v })
}

func (w SendspinWriter) OutputDelayMs(v int) error {
	return w.st.Update(func(c *Config) { c.Sendspin.OutputDelayMs = v })
}
