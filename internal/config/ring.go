package config

// Ring is the light, apart from what Home Assistant holds for the light entity itself. Reaction,
// Trouble and Muted are animations by name, or empty for none, and none of them are appearances the
// light was set to: they outlive it being switched off and come back with it.
type Ring struct {
	Reaction string `json:"reaction"`
	Trouble  string `json:"trouble"`
	Muted    string `json:"muted"`

	// Light is the appearance Home Assistant last set, so a restart comes back the way it was left.
	Light Light `json:"light"`
}

// Light is the ring light's own state. It is one thing rather than six settings: an appearance is
// chosen all at once, so it is saved and restored all at once. Brightness and the channels are 0 to
// 1, as Home Assistant sends them.
//
// Off by default, so a device nobody has asked for anything comes up dark rather than lighting the
// room by itself after a power cut — which is what ESPHome makes opt-in for the same reason.
type Light struct {
	On         bool    `json:"on"`
	Brightness float32 `json:"brightness"`
	Red        float32 `json:"red"`
	Green      float32 `json:"green"`
	Blue       float32 `json:"blue"`

	// Effect is the animation by name, empty for a plain colour.
	Effect string `json:"effect"`
}

const (
	// The ring does not follow the room until asked. A device that lights up whenever anyone speaks is
	// a choice, not a default: in a bedroom it is the opposite of what you want.
	DefaultReaction = ""

	// A failure says so, because a request that silently did nothing is the worst of the options.
	DefaultTrouble = "Alert"

	// A cut microphone does not, because the button has its own LED for exactly this and a ring held
	// lit for as long as someone leaves the device muted is both a light nobody asked for and, on this
	// hardware, an audible one.
	DefaultRingMuted = ""

	// Full, for whatever turns the ring on without saying how bright.
	DefaultBrightness = 1.0
)

func defaultRing() Ring {
	return Ring{
		Reaction: DefaultReaction,
		Trouble:  DefaultTrouble,
		Muted:    DefaultRingMuted,
		Light:    Light{Brightness: DefaultBrightness, Red: 1, Green: 1, Blue: 1},
	}
}

type RingWriter struct{ st *Store }

func (w RingWriter) Reaction(v string) error {
	return w.st.Update(func(c *Config) { c.Ring.Reaction = v })
}

func (w RingWriter) Trouble(v string) error {
	return w.st.Update(func(c *Config) { c.Ring.Trouble = v })
}

func (w RingWriter) Muted(v string) error {
	return w.st.Update(func(c *Config) { c.Ring.Muted = v })
}

// Light saves the whole appearance at once, since that is how it is set.
func (w RingWriter) Light(v Light) error {
	return w.st.Update(func(c *Config) { c.Ring.Light = v })
}
