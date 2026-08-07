package sendspin

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	ssync "github.com/Sendspin/sendspin-go/pkg/sync"
	"github.com/gorilla/websocket"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

// Clock sync runs in bursts, matching the reference player. A round that spent longer in the network
// says less about the offset, so the best of eight is used and the rest thrown away: measured on device,
// single rounds let one at rtt=3.09 s into the filter.
const (
	syncEvery   = 10 * time.Second
	syncBurst   = 8
	syncTimeout = 500 * time.Millisecond
)

// protocolVersion: the library exports no constant and does not default it. Zero is refused.
const protocolVersion = 1

// session is one server's hold on this room. The server dials us, so there is no reconnect here.
type session struct {
	client *protocol.Client
	clock  *ssync.ClockSync
	out    *out
	bg     *speaker.Arbiter
	report func(string)

	// dec is non-nil exactly while a stream is running.
	dec    decoder
	chunks int

	// lastTS is the newest chunk's timestamp, kept to measure what a new stream overlaps.
	lastTS int64
	opened bool
	muted  bool
}

func newSession(conn *websocket.Conn, o *out, bg *speaker.Arbiter, name string, report func(string)) *session {
	offered := make([]protocol.AudioFormat, 0, len(formats()))
	for _, f := range formats() {
		offered = append(offered, protocol.AudioFormat{
			Codec: f.Codec, Channels: f.Channels, SampleRate: f.SampleRate, BitDepth: f.BitDepth,
		})
	}

	client := protocol.NewClientFromConn(protocol.Config{
		Name: name,

		Version: protocolVersion,

		// Nothing is activated that is not claimed here.
		SupportedRoles: []string{"player@v1"},

		// The factory mac: survives a reinstall, a rename and a new address.
		ClientID: layout.MAC(layout.Idme("mac_addr")),
		DeviceInfo: protocol.DeviceInfo{
			ProductName:     layout.Model,
			Manufacturer:    layout.Manufacturer,
			SoftwareVersion: layout.Version,
		},
		PlayerV1Support: protocol.PlayerV1Support{
			SupportedFormats:  offered,
			BufferCapacity:    bufferCapacity,
			SupportedCommands: []string{"volume", "mute"},
		},
	}, conn)

	return &session{client: client, clock: ssync.NewClockSync(), out: o, bg: bg, report: report}
}

// bufferCapacity caps how far ahead the server may send. The spec counts bytes of coded audio, so what
// it buys in seconds depends on the codec: 256 KB is about 19 s of Opus but only 1.4 s of 48k PCM.
const bufferCapacity = 1 << 18

// run drives the connection until it closes or ctx ends.
func (s *session) run(ctx context.Context) error {
	defer s.finish()

	if err := s.client.Start(); err != nil {
		return err
	}

	s.reported()
	s.out.use(s.clock)
	safe.Go("sendspin clock", func() { s.synced(ctx) })

	for {
		select {
		case <-ctx.Done():
			s.client.SendGoodbye("user_request")
			return nil

		case <-s.client.Done():
			return nil

		case start, ok := <-s.client.StreamStart:
			if !ok {
				return nil
			}
			s.began(start)

		// Music Assistant ends the stream instead, but the spec has this for seeks and other servers
		// may use it.
		case <-s.client.StreamClear:
			s.cleared()

		case <-s.client.StreamEnd:
			slog.Info("sendspin stream end", "queued_ms", s.out.queuedMs())
			s.ended()

		case chunk, ok := <-s.client.AudioChunks:
			if !ok {
				return nil
			}
			s.heard(chunk)

		case cmd := <-s.client.ControlMsgs:
			s.told(cmd)

		case g := <-s.client.GroupUpdate:
			s.grouped(g)

		// Nothing acts on these, but an undrained channel blocks the reader.
		case st := <-s.client.ServerState:
			slog.Info("sendspin server state", "state", st)
		case <-s.client.ArtworkChunks:
		}
	}
}

// began builds the decoder for the negotiated format and takes the speaker.
func (s *session) began(start protocol.StreamStart) {
	if start.Player == nil {
		return
	}
	p := start.Player

	dec, err := decoderFor(p.Codec, p.SampleRate, p.Channels, p.BitDepth)
	if err != nil {
		slog.Error("sendspin cannot play what was offered", "codec", p.Codec,
			"rate", p.SampleRate, "ch", p.Channels, "bits", p.BitDepth, "err", err)
		return
	}
	if err := s.out.open(p.SampleRate, p.Channels, p.BitDepth); err != nil {
		slog.Error("sendspin", "err", err)
		return
	}

	first := s.dec == nil
	s.dec = dec
	s.opened = true
	if first {
		s.bg.Took(s.out)
		s.report(statePlaying)
		media.Get().External(true)
	}
	slog.Info("sendspin stream", "codec", p.Codec, "rate", p.SampleRate, "ch", p.Channels, "bits", p.BitDepth)
}

// cleared drops what has not been heard, both what is still coded and what is queued.
func (s *session) cleared() {
	drained := 0
	for {
		select {
		case <-s.client.AudioChunks:
			drained++
		default:
			slog.Info("sendspin clear", "undecoded", drained, "queued_ms", s.out.queuedMs())
			s.out.flush()
			return
		}
	}
}

// grouped only reports. Stopping is stream/end's job, which the server sends on stop as well as on skip
// and seek. Fields are deltas, so an absent state means unchanged.
func (s *session) grouped(g protocol.GroupUpdate) {
	if g.PlaybackState == nil {
		return
	}
	slog.Info("sendspin group", "state", *g.PlaybackState, "queued_ms", s.out.queuedMs())
}

// ended drops what is held: the spec has stream/end stop output and clear buffers, and the server sends
// it on stop, skip and seek. A track running into the next one keeps the stream and says nothing.
func (s *session) ended() {
	if s.dec == nil {
		return
	}
	s.dec = nil
	s.cleared()
	s.out.close()
	s.bg.Gave(s.out)
	s.report(stateJoined)
	media.Get().External(false)
}

// heard plays a chunk as it arrives.
func (s *session) heard(chunk protocol.AudioChunk) {
	if s.dec == nil {
		return
	}

	pcm, err := s.dec.decode(chunk.Data)
	if err != nil {
		slog.Warn("sendspin decode", "err", err)
		return
	}
	s.out.write(chunk.Timestamp, pcm)

	// A new stream's first chunk against the outgoing one's last says whether the server means it to
	// follow on or to replace what is still queued. Both look the same at stream/end.
	if s.opened {
		s.opened = false
		slog.Info("sendspin stream first chunk",
			"lead_ms", (chunk.Timestamp-s.clock.ServerMicrosNow())/1000,
			"prev_last_lead_ms", (s.lastTS-s.clock.ServerMicrosNow())/1000,
			"overlap_ms", (s.lastTS-chunk.Timestamp)/1000,
			"queued_ms", s.out.queuedMs())
	}
	s.lastTS = chunk.Timestamp

	// lead_ms is when the server wanted this played, against now. We play on arrival instead, so it is
	// also how far behind the intended point the room is running.
	if s.chunks++; s.chunks%250 == 0 {
		slog.Info("sendspin ahead",
			"queued_ms", s.out.queuedMs(),
			"undecoded", len(s.client.AudioChunks),
			"lead_ms", (chunk.Timestamp-s.clock.ServerMicrosNow())/1000)
	}
}

func (s *session) told(cmd protocol.PlayerCommand) {
	switch cmd.Command {
	case "volume":
		s.out.setVolume(cmd.Volume)
	case "mute":
		s.muted = cmd.Mute
		s.out.setMuted(cmd.Mute)
	}
	s.reported()
}

// reported echoes what took effect. The server has no other way to learn a command landed, and the
// protocol carries no position, so this is the whole of what we say back.
func (s *session) reported() {
	if err := s.client.SendState(protocol.PlayerState{
		State:  "synchronized",
		Volume: config.Get().Speaker.Volume * 100 / speaker.VolumeSteps,
		Muted:  s.muted,
	}); err != nil {
		slog.Debug("sendspin client state", "err", err)
	}
}

// synced keeps the clock filter fed. It owns TimeSyncResp: nothing else may read that channel, or the
// burst would lose rounds to whoever got there first.
func (s *session) synced(ctx context.Context) {
	ticker := time.NewTicker(syncEvery)
	defer ticker.Stop()

	for {
		s.measure(ctx)

		select {
		case <-ctx.Done():
			return
		case <-s.client.Done():
			return
		case <-ticker.C:
		}
	}
}

// measure runs one burst and feeds the filter the round that spent least time in the network.
func (s *session) measure(ctx context.Context) {
drain:
	for {
		select {
		case <-s.client.TimeSyncResp:
		default:
			break drain
		}
	}

	var best protocol.ServerTime
	var arrived int64
	least := int64(math.MaxInt64)

	for range syncBurst {
		sent := micros()
		if err := s.client.SendTimeSync(sent); err != nil {
			slog.Debug("sendspin time sync", "err", err)
			return
		}

		timeout := time.NewTimer(syncTimeout)
		select {
		case <-ctx.Done():
			timeout.Stop()
			return
		case reply := <-s.client.TimeSyncResp:
			timeout.Stop()
			back := micros()
			rtt := (back - reply.ClientTransmitted) - (reply.ServerTransmitted - reply.ServerReceived)
			if rtt < least {
				least, best, arrived = rtt, reply, back
			}
		case <-timeout.C:
		}
	}

	if least == math.MaxInt64 {
		slog.Debug("sendspin time sync", "err", "no reply in the burst")
		return
	}
	s.clock.ProcessSyncResponse(best.ClientTransmitted, best.ServerReceived, best.ServerTransmitted, arrived)
}

func (s *session) finish() {
	s.ended()
	s.client.Close()
}

func micros() int64 { return time.Now().UnixMicro() }
