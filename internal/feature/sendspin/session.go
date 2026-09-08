package sendspin

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

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

// What the room asks of the server's scheduling. The lead is how far ahead the first chunk of a stream
// has to land so nothing is cut off; the buffer is what it wants kept queued against Wi-Fi jitter.
// Neither includes the speaker's hardware tail, which the renderer takes off itself.
const (
	requiredLeadMs = 1000
	minBufferMs    = 2000
)

// bufferCapacity caps how far ahead the server may send, which is the stall the room can ride out. The
// spec counts bytes and carries one number for every format, so it is sized for the widest we offer:
// every codec then gets the same cushion, bounded by the server's 30 s cap rather than by bytes.
const bufferSeconds = 30

const bufferCapacity = bufferSeconds * speaker.Rate * speaker.Channels * speaker.Bits / 8

// pairTimeout bounds a pairing attempt from its first message, as the spec recommends.
const pairTimeout = 2 * time.Minute

// commands is what the server may set on this player.
var commands = []string{"volume", "mute"}

// What the security sensor says about the connection.
const (
	securityOff      = "off"
	securityWaiting  = "waiting"
	securityUnpaired = "unpaired"
	securityPairing  = "pairing"
	securityPaired   = "paired"
)

// session is one server's hold on this room, from the socket opening to it closing. The server dials
// us, so there is no reconnect here.
type session struct {
	c     *conn
	trust *trust
	clock *ssync.ClockSync
	out   *out
	bg    *speaker.Arbiter
	name  string

	report   func(string)
	security func(string)

	// goodbye is why the room is leaving, asked when ctx ends.
	goodbye func() string

	// What the handshake settled and the server then declared. The category is read from outside the
	// reader too, when a setting changes under a live connection.
	catMu      sync.Mutex
	category   pskCategory
	fellBack   bool
	serverName string
	activities []string
	roles      []string

	// pairingIndex counts pairing activations since the last handshake, which the spec has the room
	// echo so a stale attempt can be told from a live one.
	pairingIndex int

	// pending is the long-term key sent in client/pair-finalize and not yet acknowledged.
	pairMu    sync.Mutex
	pending   *psk
	pairTimer *time.Timer

	// dec is non-nil exactly while a stream is running.
	dec    decoder
	chunks int

	// lastTS is the newest chunk's timestamp, kept to measure what a new stream overlaps.
	lastTS int64
	opened bool
	muted  bool

	// timeResp carries server/time replies from the reader to the sync loop, which alone consumes them.
	// established gates it: the spec has nothing but a goodbye leave the room before the first activate,
	// and a re-key starts that silence over.
	timeResp    chan serverTime
	established atomic.Bool
	wakeSync    chan struct{}
	synced      atomic.Bool
	done        chan struct{}
}

func newSession(ws *websocket.Conn, tr *trust, o *out, bg *speaker.Arbiter, name string, report, security func(string), goodbye func() string) *session {
	return &session{
		c:        newConn(ws, tr.identity()),
		trust:    tr,
		clock:    ssync.NewClockSync(),
		out:      o,
		bg:       bg,
		name:     name,
		report:   report,
		security: security,
		goodbye:  goodbye,
		timeResp: make(chan serverTime, syncBurst),
		wakeSync: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// errClosed is a connection the room ended on purpose, with a goodbye already sent.
var errClosed = errors.New("closed")

// run drives the connection until it closes or ctx ends.
func (s *session) run(ctx context.Context) error {
	defer s.finish()

	res, err := s.c.preamble(s.trust.lookup)
	if err != nil {
		return err
	}
	s.settled(res)
	slog.Info("sendspin channel up", "server", short(s.c.serverID), "key", res.category, "fell_back", res.fellBack)

	s.out.use(s.clock)
	safe.Go("sendspin clock", func() { s.syncLoop(ctx) })

	// Leaving is a word and then the socket: the word so the server knows whether to come back, the
	// socket so the reader returns.
	safe.Go("sendspin hangup", func() {
		select {
		case <-ctx.Done():
			_ = s.c.writeJSON(typeClientGoodbye, clientGoodbye{Reason: s.goodbye()})
			_ = s.c.close()
		case <-s.done:
		}
	})

	if err := s.establish(); err != nil {
		return s.closed(ctx, err)
	}
	s.ready()

	for {
		msgType, payload, err := s.c.readFrame()
		if err != nil {
			return s.closed(ctx, err)
		}
		switch msgType {
		case msgJSON:
			if err := s.control(payload); err != nil {
				return s.closed(ctx, err)
			}
		case msgAudio:
			s.heard(payload)
		default:
			slog.Debug("sendspin ignoring binary message", "type", msgType, "bytes", len(payload))
		}
	}
}

// closed turns the ways a connection ends into what the caller wants to hear: nothing for a close the
// room chose or was told to make, the error otherwise.
func (s *session) closed(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, errClosed) {
		return nil
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// ready opens the floor once the first activate is in: clock sync may start, and starts now rather than
// at its next tick.
func (s *session) ready() {
	s.established.Store(true)
	select {
	case s.wakeSync <- struct{}{}:
	default:
	}
}

// settled records what the handshake matched.
func (s *session) settled(res handshakeResult) {
	s.catMu.Lock()
	s.category, s.fellBack = res.category, res.fellBack
	s.catMu.Unlock()
	s.pairingIndex = 0
	if res.category == categoryLongTerm {
		s.trust.touch(s.c.serverID)
	}
}

// cat is the key category the channel is running on.
func (s *session) cat() pskCategory {
	s.catMu.Lock()
	defer s.catMu.Unlock()
	return s.category
}

// establish is the exchange that follows every handshake: the server names itself, the room says what it
// can do, and the server says what the connection is for. Nothing else is allowed in between.
func (s *session) establish() error {
	msgType, payload, err := s.c.readFrame()
	if err != nil {
		return fmt.Errorf("reading server/hello: %w", err)
	}
	var hello serverHello
	if msgType != msgJSON || decodeEnvelope(payload, typeServerHello, &hello) != nil {
		return errors.New("expected server/hello first")
	}
	s.serverName = hello.Name

	if err := s.c.writeJSON(typeClientHello, s.hello()); err != nil {
		return fmt.Errorf("sending client/hello: %w", err)
	}

	msgType, payload, err = s.c.readFrame()
	if err != nil {
		return fmt.Errorf("reading server/activate: %w", err)
	}
	var act serverActivate
	if msgType != msgJSON || decodeEnvelope(payload, typeServerActivate, &act) != nil {
		return errors.New("expected server/activate before anything else")
	}
	if act.ActiveRoles == nil {
		act.ActiveRoles = &[]string{}
	}
	return s.activated(act)
}

// hello is what the room tells a server about itself.
func (s *session) hello() clientHello {
	offered := make([]audioFormat, 0, len(formats()))
	offered = append(offered, formats()...)
	mac, _ := layout.FactoryMAC()

	trustLevel := trustNone
	if s.cat() == categoryLongTerm {
		trustLevel = trustUser
	}
	return clientHello{
		Name:       s.name,
		TrustLevel: trustLevel,
		DeviceInfo: &deviceInfo{
			ProductName:     layout.Model,
			Manufacturer:    layout.Manufacturer,
			SoftwareVersion: layout.Version,
			MACAddress:      mac,
		},
		SupportedRoles: []string{rolePlayer},
		PlayerSupport: &playerSupport{
			SupportedFormats:  offered,
			BufferCapacity:    bufferCapacity,
			SupportedCommands: commands,
		},
		SupportedPairMethods: []pairMethod{{Method: methodPairingPSK, Locations: []string{"device"}}},
		UnpairedAccess:       unpairedAccess{Enabled: config.Get().Sendspin.UnpairedAccess},
	}
}

// control handles one JSON message once the connection is established.
func (s *session) control(payload []byte) error {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("malformed message: %w", err)
	}

	switch env.Type {
	case "noise/handshake":
		// The server is changing keys, typically to promote a pairing. Everything declared on the old
		// keys is void; the exchange after it says what the connection is for now.
		s.established.Store(false)
		res, err := s.c.rehandshake(payload, s.trust.lookup(s.c.serverID))
		if err != nil {
			return fmt.Errorf("re-handshake: %w", err)
		}
		s.settled(res)
		s.cancelPairing()
		slog.Info("sendspin re-keyed", "server", short(s.c.serverID), "key", s.cat())
		if err := s.establish(); err != nil {
			return err
		}
		s.ready()
		return nil

	case typeServerActivate:
		var act serverActivate
		if err := json.Unmarshal(env.Payload, &act); err != nil {
			return fmt.Errorf("malformed server/activate: %w", err)
		}
		return s.activated(act)

	case typeServerTime:
		var st serverTime
		if err := json.Unmarshal(env.Payload, &st); err != nil {
			return nil
		}
		select {
		case s.timeResp <- st:
		default:
		}

	case typeStreamStart:
		var start streamStart
		if err := json.Unmarshal(env.Payload, &start); err != nil {
			return fmt.Errorf("malformed stream/start: %w", err)
		}
		s.began(start)

	// Music Assistant ends the stream instead, but the spec has this for seeks and other servers may
	// use it.
	case typeStreamClear:
		var roles streamRoles
		_ = json.Unmarshal(env.Payload, &roles)
		if roles.player() {
			s.cleared()
		}

	case typeStreamEnd:
		var roles streamRoles
		_ = json.Unmarshal(env.Payload, &roles)
		if roles.player() {
			late, dropped := s.out.misses()
			slog.Info("sendspin stream end", "queued_ms", s.out.queuedMs(), "late", late, "dropped", dropped)
			s.ended()
		}

	case typeGroupUpdate:
		var g groupUpdate
		_ = json.Unmarshal(env.Payload, &g)
		slog.Info("sendspin group", "state", g.PlaybackState, "group", g.GroupName, "queued_ms", s.out.queuedMs())

	case typeServerCommand:
		var cmd serverCommand
		if err := json.Unmarshal(env.Payload, &cmd); err == nil && cmd.Player != nil {
			s.told(*cmd.Player)
		}

	case typeServerUnpair:
		if s.cat() != categoryLongTerm {
			return nil
		}
		slog.Info("sendspin server unpaired us", "server", short(s.c.serverID))
		if err := s.trust.forget(s.c.serverID); err != nil {
			slog.Error("sendspin forgetting a pairing failed", "err", err)
		}
		return s.leave(goodbyeUnpaired)

	case typePairFinalized:
		s.finalized()

	case typePairAbort:
		var ab pairAbort
		_ = json.Unmarshal(env.Payload, &ab)
		slog.Warn("sendspin pairing aborted by server", "reason", ab.Reason)
		s.cancelPairing()

	default:
		slog.Debug("sendspin ignoring message", "type", env.Type)
	}
	return nil
}

// activated applies a server/activate, or refuses it the way the spec says to.
func (s *session) activated(act serverActivate) error {
	unpaired := config.Get().Sendspin.UnpairedAccess
	cat := s.cat()
	v := judge(cat, act, unpaired, []string{methodPairingPSK})
	switch {
	case v.goodbye != "":
		slog.Warn("sendspin refusing activation", "activities", act.Activities, "key", cat, "goodbye", v.goodbye)
		return s.leave(v.goodbye)
	case v.abort != "":
		slog.Warn("sendspin refusing pairing method", "method", act.method(), "key", cat)
		return s.c.writeJSON(typePairAbort, pairAbort{Reason: v.abort})
	}

	s.activities = act.Activities
	if act.ActiveRoles != nil {
		s.roles = *act.ActiveRoles
	}
	if !playbackCapable(cat, s.activities, unpaired) {
		s.roles = nil
	}

	if act.has(activityPairing) {
		s.pairingIndex++
		s.security(securityPairing)
		return s.pair(act)
	}
	s.cancelPairing()

	if act.has(activityPlayback) {
		s.trust.setLastPlayback(s.c.serverID)
	}
	if cat == categoryLongTerm {
		s.security(securityPaired)
	} else {
		s.security(securityUnpaired)
	}

	slog.Info("sendspin activated", "server", s.serverName, "activities", s.activities, "roles", s.roles)
	if !s.player() {
		s.ended()
		return nil
	}
	s.reportState()
	return nil
}

// player says whether the room has been given the player role.
func (s *session) player() bool {
	for _, r := range s.roles {
		if r == rolePlayer {
			return true
		}
	}
	return false
}

// pair starts the one pairing method the room offers: the operator has entered the room's token into
// the server, the handshake was keyed with the pairing key, and the room now hands over a long-term
// key of its own making.
func (s *session) pair(act serverActivate) error {
	if act.method() != methodPairingPSK || s.cat() != categoryPairing {
		return s.c.writeJSON(typePairAbort, pairAbort{Reason: abortMethodNotSupported})
	}
	key, err := newPSK()
	if err != nil {
		return err
	}

	s.pairMu.Lock()
	s.pending = &key
	if s.pairTimer != nil {
		s.pairTimer.Stop()
	}
	s.pairTimer = time.AfterFunc(pairTimeout, s.pairTimedOut)
	s.pairMu.Unlock()

	slog.Info("sendspin pairing", "server", s.serverName, "method", methodPairingPSK)
	return s.c.writeJSON(typePairFinalize, pairFinalize{LongTermPSK: key.String()})
}

// finalized is the server saying it has kept the key. The room keeps it too, and waits for the server
// to re-key onto it.
func (s *session) finalized() {
	s.pairMu.Lock()
	key := s.pending
	s.pending = nil
	if s.pairTimer != nil {
		s.pairTimer.Stop()
	}
	s.pairMu.Unlock()

	if key == nil {
		slog.Debug("sendspin stray server/pair-finalize")
		return
	}
	if err := s.trust.remember(s.c.serverID, *key, s.c.serverID); err != nil {
		slog.Error("sendspin keeping a pairing failed", "err", err)
		return
	}
	slog.Info("sendspin paired", "server", s.serverName, "records", s.trust.count())
}

// cancelPairing drops an attempt the server ended by other means: a re-key, a cancelling activate, an
// abort.
func (s *session) cancelPairing() {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	s.pending = nil
	if s.pairTimer != nil {
		s.pairTimer.Stop()
	}
}

func (s *session) pairTimedOut() {
	s.pairMu.Lock()
	pending := s.pending != nil
	s.pending = nil
	s.pairMu.Unlock()
	if pending {
		slog.Warn("sendspin pairing attempt timed out")
		_ = s.c.writeJSON(typePairAbort, pairAbort{Reason: abortAttemptTimeout})
	}
}

// leave says goodbye and hangs up.
func (s *session) leave(reason string) error {
	_ = s.c.writeJSON(typeClientGoodbye, clientGoodbye{Reason: reason})
	_ = s.c.close()
	return errClosed
}

// kick is leave from outside the reader, for a setting changing under a live connection.
func (s *session) kick(reason string) { _ = s.leave(reason) }

// began builds the decoder for the negotiated format and takes the speaker.
func (s *session) began(start streamStart) {
	if start.Player == nil {
		return
	}
	p := start.Player

	// FLAC arrives as bare frames; what makes them a stream is this.
	header, err := base64.StdEncoding.DecodeString(p.CodecHeader)
	if err != nil {
		slog.Error("sendspin codec header", "codec", p.Codec, "err", err)
		return
	}

	dec, err := decoderFor(p.Codec, p.SampleRate, p.Channels, p.BitDepth, header)
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
	if !first {
		s.dec.close()
	}
	s.dec = dec
	s.opened = true
	if first {
		s.bg.Took(s.out)
		s.report(statePlaying)
		media.Get().External(true)
	}
	slog.Info("sendspin stream", "codec", p.Codec, "rate", p.SampleRate, "ch", p.Channels, "bits", p.BitDepth)
}

// cleared drops what has not been heard yet: the queue, and the anchor with it.
func (s *session) cleared() {
	slog.Info("sendspin clear", "queued_ms", s.out.queuedMs())
	s.out.flush()
}

// ended drops what is held: the spec has stream/end stop output and clear buffers, and the server sends
// it on stop, skip and seek. A track running into the next one keeps the stream and says nothing.
func (s *session) ended() {
	if s.dec == nil {
		return
	}
	s.dec.close()
	s.dec = nil
	s.cleared()
	s.out.close()
	s.bg.Gave(s.out)
	s.report(stateJoined)
	media.Get().External(false)
}

// audioHeader is what precedes the codec payload in a chunk: the server-clock time the first sample is
// due, in microseconds.
const audioHeader = 8

// heard plays a chunk as it arrives.
func (s *session) heard(body []byte) {
	if s.dec == nil || len(body) < audioHeader {
		return
	}
	at := int64(binary.BigEndian.Uint64(body[:audioHeader]))
	data := body[audioHeader:]

	pcm, err := s.dec.decode(data)
	if err != nil {
		slog.Warn("sendspin decode", "err", err)
		return
	}
	// A frame can span chunks, and the one that did not finish it carries no audio of its own.
	if len(pcm) == 0 {
		return
	}
	s.out.write(at, pcm)

	// A new stream's first chunk against the outgoing one's last says whether the server means it to
	// follow on or to replace what is still queued. Both look the same at stream/end.
	if s.opened {
		s.opened = false
		slog.Info("sendspin stream first chunk",
			"lead_ms", (at-s.clock.ServerMicrosNow())/1000,
			"prev_last_lead_ms", (s.lastTS-s.clock.ServerMicrosNow())/1000,
			"overlap_ms", (s.lastTS-at)/1000,
			"queued_ms", s.out.queuedMs())
	}
	s.lastTS = at

	// lead_ms is when the server wanted this played, against now. We play on arrival instead, so it is
	// also how far behind the intended point the room is running.
	if s.chunks++; s.chunks%250 == 0 {
		late, dropped := s.out.misses()
		slog.Info("sendspin ahead",
			"queued_ms", s.out.queuedMs(),
			"lead_ms", (at-s.clock.ServerMicrosNow())/1000,
			"late", late,
			"dropped", dropped)
	}
}

func (s *session) told(cmd playerCommand) {
	switch cmd.Command {
	case "volume":
		if cmd.Volume != nil {
			s.out.setVolume(*cmd.Volume)
		}
	case "mute":
		if cmd.Mute != nil {
			s.muted = *cmd.Mute
			s.out.setMuted(*cmd.Mute)
		}
	}
	s.reportState()
}

// reportState is the whole of what the room says about itself, sent when something changed and once
// the clock has settled: the spec has a player stay silent about being available until it can place
// audio, and has every message carry every field. Tested against Music Assistant 2.11: this is the
// shape it accepts.
func (s *session) reportState() {
	if !s.player() || !s.synced.Load() {
		return
	}
	st := clientState{
		Available: true,
		Player: &playerState{
			Volume:             config.Get().Speaker.Volume * 100 / speaker.VolumeSteps,
			Muted:              s.muted,
			RequiredLeadTimeMs: requiredLeadMs,
			MinBufferMs:        minBufferMs,
		},
	}
	if err := s.c.writeJSON(typeClientState, st); err != nil {
		slog.Debug("sendspin client state", "err", err)
	}
}

// syncLoop keeps the clock filter fed. It owns timeResp: nothing else may read that channel, or the
// burst would lose rounds to whoever got there first.
func (s *session) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(syncEvery)
	defer ticker.Stop()

	for {
		if s.established.Load() {
			s.measure(ctx)
		}

		// The first time the filter can be trusted is the first time the room may call itself available.
		if s.clock.CheckQuality() != ssync.QualityLost && s.synced.CompareAndSwap(false, true) {
			slog.Info("sendspin clock synced")
			s.reportState()
		}

		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
		case <-s.wakeSync:
		}
	}
}

// measure runs one burst and feeds the filter the round that spent least time in the network.
func (s *session) measure(ctx context.Context) {
drain:
	for {
		select {
		case <-s.timeResp:
		default:
			break drain
		}
	}

	var best serverTime
	var arrived int64
	least := int64(math.MaxInt64)

	for range syncBurst {
		// A re-key part way through a burst ends the burst: nothing but the handshake may flow until the
		// server has activated the connection again.
		if !s.established.Load() {
			return
		}
		sent := micros()
		if err := s.c.writeJSON(typeClientTime, clientTime{ClientTransmitted: sent}); err != nil {
			slog.Debug("sendspin time sync", "err", err)
			return
		}

		timeout := time.NewTimer(syncTimeout)
		select {
		case <-ctx.Done():
			timeout.Stop()
			return
		case <-s.done:
			timeout.Stop()
			return
		case reply := <-s.timeResp:
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
	s.cancelPairing()
	s.ended()
	close(s.done)
	_ = s.c.close()
}

func micros() int64 { return time.Now().UnixMicro() }
