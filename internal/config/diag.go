package config

// Diag is how the device reports on itself.
type Diag struct {
	// Interval is how often the readings that drift are collected, in seconds.
	Interval int `json:"interval"`

	RemoteADB bool `json:"remote_adb"`

	// InsecureTLS stops certificates being checked on anything the device downloads.
	InsecureTLS bool `json:"insecure_tls"`

	// MinCores holds cores online that the governor would otherwise park.
	MinCores int `json:"min_cores"`
}

// DefaultInterval is five minutes. The readings move slowly and every one of them costs a read of
// sysfs or proc, so this is a compromise between a stale card and a device busying itself for
// nobody.
const DefaultInterval = 300

const DefaultMinCores = 2

func defaultDiag() Diag {
	return Diag{Interval: DefaultInterval, MinCores: DefaultMinCores}
}

type DiagWriter struct{ st *Store }

func (w DiagWriter) Interval(v int) error {
	return w.st.Update(func(c *Config) { c.Diag.Interval = v })
}

func (w DiagWriter) RemoteADB(v bool) error {
	return w.st.Update(func(c *Config) { c.Diag.RemoteADB = v })
}

func (w DiagWriter) InsecureTLS(v bool) error {
	return w.st.Update(func(c *Config) { c.Diag.InsecureTLS = v })
}

func (w DiagWriter) MinCores(v int) error {
	return w.st.Update(func(c *Config) { c.Diag.MinCores = v })
}
