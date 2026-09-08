package config

// Sendspin is the room's part of a synchronized stream. A server discovers it and dials in, so there
// is no address to set.
//
// UnpairedAccess is whether a server this room has never paired with may play through it. On, the
// room behaves as it always has: any Sendspin server on the network can use it once its operator
// approves the room on their side. Off, a server has to pair first, and one that has not is told so
// and turned away.
type Sendspin struct {
	Enabled        bool `json:"enabled"`
	UnpairedAccess bool `json:"unpaired_access"`
}

func defaultSendspin() Sendspin { return Sendspin{Enabled: true, UnpairedAccess: true} }

type SendspinWriter struct{ st *Store }

func (w SendspinWriter) Enabled(v bool) error {
	return w.st.Update(func(c *Config) { c.Sendspin.Enabled = v })
}

func (w SendspinWriter) UnpairedAccess(v bool) error {
	return w.st.Update(func(c *Config) { c.Sendspin.UnpairedAccess = v })
}
