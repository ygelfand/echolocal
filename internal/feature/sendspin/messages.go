package sendspin

// The JSON messages the room sends and reads, as the spec shapes them. Every one travels wrapped in an
// envelope naming its type; encodeEnvelope and decodeEnvelope in secure.go do the wrapping.
//
// Fields the room never reads are left out: the decoder ignores what it is not told about, which is
// what the spec asks of it.

// Message types on the wire.
const (
	typeServerHello    = "server/hello"
	typeClientHello    = "client/hello"
	typeServerActivate = "server/activate"
	typeClientTime     = "client/time"
	typeServerTime     = "server/time"
	typeClientState    = "client/state"
	typeClientGoodbye  = "client/goodbye"
	typeServerCommand  = "server/command"
	typeStreamStart    = "stream/start"
	typeStreamClear    = "stream/clear"
	typeStreamEnd      = "stream/end"
	typeGroupUpdate    = "group/update"
	typeServerUnpair   = "server/unpair"
	typePairFinalize   = "client/pair-finalize"
	typePairFinalized  = "server/pair-finalize"
	typePairAbort      = "pair/abort"
)

// Activities a server declares on a connection. Management is from an earlier draft that Music
// Assistant still declares on paired connections; it is accepted and nothing is done with it.
const (
	activityPlayback   = "playback"
	activityPairing    = "pairing"
	activityManagement = "management"
)

// Pairing methods. Only the first is offered: it is the one every client must have, and the one whose
// wire shape has held still while the code-based ones were renamed.
const (
	methodPairingPSK = "pairing_psk"
)

// Goodbye reasons.
const (
	goodbyeAnotherServer     = "another_server"
	goodbyeShutdown          = "shutdown"
	goodbyeRestart           = "restart"
	goodbyeUserRequest       = "user_request"
	goodbyeUnauthorized      = "unauthorized"
	goodbyePairingRequired   = "pairing_required"
	goodbyeConcurrentAttempt = "concurrent_attempt"
	goodbyeUnpaired          = "unpaired"
)

// Pair abort reasons the room sends.
const (
	abortMethodNotSupported = "method_not_supported"
	abortAttemptTimeout     = "attempt_timeout"
)

// Trust levels in client/hello: user once a pairing record exists for the server, none otherwise.
const (
	trustUser = "user"
	trustNone = "none"
)

const rolePlayer = "player@v1"

type serverHello struct {
	Name      string   `json:"name"`
	Languages []string `json:"languages,omitempty"`
}

type deviceInfo struct {
	ProductName     string `json:"product_name,omitempty"`
	Manufacturer    string `json:"manufacturer,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
	MACAddress      string `json:"mac_address,omitempty"`
}

type playerSupport struct {
	SupportedFormats  []audioFormat `json:"supported_formats"`
	BufferCapacity    int           `json:"buffer_capacity"`
	SupportedCommands []string      `json:"supported_commands"`
}

// pairMethod is one entry of supported_pair_methods. The spec now keys them by method in an object;
// Music Assistant's library, old and new, reads a list with the method inside. A list it is, until
// the servers move.
type pairMethod struct {
	Method    string   `json:"method"`
	Locations []string `json:"locations,omitempty"`
}

type unpairedAccess struct {
	Enabled bool `json:"enabled"`
}

type clientHello struct {
	Name                 string         `json:"name"`
	DeviceInfo           *deviceInfo    `json:"device_info,omitempty"`
	TrustLevel           string         `json:"trust_level"`
	SupportedRoles       []string       `json:"supported_roles"`
	PlayerSupport        *playerSupport `json:"player@v1_support,omitempty"`
	SupportedPairMethods []pairMethod   `json:"supported_pair_methods"`
	UnpairedAccess       unpairedAccess `json:"unpaired_access"`
}

// serverActivate is what the connection is for. ActiveRoles is a pointer because absent means "as
// before" and empty means none.
type serverActivate struct {
	Activities  []string         `json:"activities"`
	ActiveRoles *[]string        `json:"active_roles,omitempty"`
	Pairing     *activatePairing `json:"pairing,omitempty"`

	// SelectedPairMethod is where an earlier draft put the method. Read so a server on it is answered
	// with the right abort rather than a puzzled one.
	SelectedPairMethod string `json:"selected_pair_method,omitempty"`
}

type activatePairing struct {
	Method string `json:"method"`
	Format string `json:"format,omitempty"`
}

// method is the pairing method however the server phrased it.
func (a serverActivate) method() string {
	if a.Pairing != nil {
		return a.Pairing.Method
	}
	return a.SelectedPairMethod
}

func (a serverActivate) has(activity string) bool {
	for _, x := range a.Activities {
		if x == activity {
			return true
		}
	}
	return false
}

type clientTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
}

type serverTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
	ServerReceived    int64 `json:"server_received"`
	ServerTransmitted int64 `json:"server_transmitted"`
}

// clientState is always the whole state: the spec has each message carry every field, and a server
// merging deltas reads a full message the same way.
type clientState struct {
	Available bool         `json:"available"`
	Player    *playerState `json:"player,omitempty"`
}

// playerState carries the delay under both the spec's name and the one Music Assistant still reads.
// Servers ignore fields they do not know, so the spare one costs nothing.
//
// SupportedCommands here names only the delay command. The spec now lists every command in both
// places, but Music Assistant's library rejects a state naming volume or mute and drops the connection
// over it; the hello is where it reads those from. Naming the delay command here is what makes Music
// Assistant show its per-player delay setting for this room.
type playerState struct {
	Volume             int      `json:"volume"`
	Muted              bool     `json:"muted"`
	OutputDelayMs      int      `json:"output_delay_ms"`
	StaticDelayMs      int      `json:"static_delay_ms"`
	RequiredLeadTimeMs int      `json:"required_lead_time_ms"`
	MinBufferMs        int      `json:"min_buffer_ms"`
	SupportedCommands  []string `json:"supported_commands"`
}

type groupUpdate struct {
	PlaybackState string `json:"playback_state"`
	GroupID       string `json:"group_id"`
	GroupName     string `json:"group_name"`
}

type streamStart struct {
	ServerTransmitted int64         `json:"server_transmitted"`
	Player            *streamPlayer `json:"player,omitempty"`
}

type streamPlayer struct {
	Codec       string `json:"codec"`
	SampleRate  int    `json:"sample_rate"`
	Channels    int    `json:"channels"`
	BitDepth    int    `json:"bit_depth"`
	CodecHeader string `json:"codec_header,omitempty"`
}

// streamRoles is stream/clear and stream/end: which roles, or all of them when absent.
type streamRoles struct {
	Roles []string `json:"roles,omitempty"`
}

func (s streamRoles) player() bool {
	if len(s.Roles) == 0 {
		return true
	}
	for _, r := range s.Roles {
		if r == "player" {
			return true
		}
	}
	return false
}

type serverCommand struct {
	Player *playerCommand `json:"player,omitempty"`
}

// playerCommand is one server/command. The delay arrives under the spec's name or the older one.
type playerCommand struct {
	Command       string `json:"command"`
	Volume        *int   `json:"volume,omitempty"`
	Mute          *bool  `json:"mute,omitempty"`
	OutputDelayMs *int   `json:"output_delay_ms,omitempty"`
	StaticDelayMs *int   `json:"static_delay_ms,omitempty"`
}

// delay is the delay a command carries, whichever name it used.
func (c playerCommand) delay() (int, bool) {
	switch {
	case c.OutputDelayMs != nil:
		return *c.OutputDelayMs, true
	case c.StaticDelayMs != nil:
		return *c.StaticDelayMs, true
	}
	return 0, false
}

type clientGoodbye struct {
	Reason string `json:"reason"`
}

type pairFinalize struct {
	LongTermPSK string `json:"long_term_psk,omitempty"`
}

type pairAbort struct {
	Reason string `json:"reason"`
}
