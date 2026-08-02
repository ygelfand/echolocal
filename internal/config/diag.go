package config

// Diag is how the device reports on itself.
type Diag struct {
	// Interval is how often the readings that drift are collected, in seconds.
	Interval int `json:"interval"`
}

// DefaultInterval is five minutes. The readings move slowly and every one of them costs a read of
// sysfs or proc, so this is a compromise between a stale card and a device busying itself for
// nobody.
const DefaultInterval = 300

func defaultDiag() Diag {
	return Diag{Interval: DefaultInterval}
}

type DiagWriter struct{ st *Store }

func (w DiagWriter) Interval(v int) error {
	return w.st.Update(func(c *Config) { c.Diag.Interval = v })
}
