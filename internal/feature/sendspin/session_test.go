package sendspin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"

	"github.com/ygelfand/echolocal/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sendspin-test")
	if err != nil {
		panic(err)
	}
	config.Use(filepath.Join(dir, "state.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const wait = 5 * time.Second

// room is the device side under test: a listener with one session, and what it told the sensors.
type room struct {
	t     *testing.T
	trust *trust
	url   string

	mu       sync.Mutex
	security []string
	sessions chan *session
	results  chan error
}

func startRoom(t *testing.T, unpaired bool) *room {
	t.Helper()
	if err := config.Set().Sendspin().UnpairedAccess(unpaired); err != nil {
		t.Fatal(err)
	}
	tr, err := loadTrust(filepath.Join(t.TempDir(), "sendspin.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := &room{t: t, trust: tr, sessions: make(chan *session, 4), results: make(chan error, 4)}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ws, err := up.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		s := newSession(ws, tr, newOut(nil), nil, "Test Room", func(string) {}, r.said, func() string { return goodbyeRestart })
		r.sessions <- s
		err = s.run(ctx)
		if err != nil {
			t.Logf("session ended: %v", err)
		}
		r.results <- err
	}))
	t.Cleanup(srv.Close)
	r.url = "ws" + strings.TrimPrefix(srv.URL, "http") + path
	return r
}

func (r *room) said(state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.security = append(r.security, state)
}

func (r *room) toldSecurity(state string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.security {
		if s == state {
			return true
		}
	}
	return false
}

// session waits for the room to have taken a connection.
func (r *room) session() *session {
	select {
	case s := <-r.sessions:
		return s
	case <-time.After(wait):
		r.t.Fatal("no session started")
		return nil
	}
}

// result waits for the session to end.
func (r *room) result() error {
	select {
	case err := <-r.results:
		return err
	case <-time.After(wait):
		r.t.Fatal("session did not end")
		return nil
	}
}

// server is enough of a Sendspin server to drive the room: the initiator side of the handshake, the
// framing, and a reader that answers clock sync the way a server would.
type server struct {
	t  *testing.T
	ws *websocket.Conn
	in *initiator

	mu    sync.Mutex
	tr    *transport
	reasm reassembler

	roomID    string
	msgs      chan envelope
	rekeying  atomic.Bool
	rekeyResp chan []byte
	rekeyDone chan struct{}
	closed    chan struct{}
}

func dial(t *testing.T, r *room) *server {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(r.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return &server{
		t: t, ws: ws, in: newInitiator(t),
		msgs:      make(chan envelope, 64),
		rekeyResp: make(chan []byte, 1),
		rekeyDone: make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (f *server) readText() ([]byte, error) {
	_ = f.ws.SetReadDeadline(time.Now().Add(wait))
	mt, data, err := f.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.TextMessage {
		return nil, errors.New("not a text frame")
	}
	return data, nil
}

// preamble runs the server side up to and including reading message 2, which it does not verify:
// handshake and handshakeFallback do that their own way.
func (f *server) preamble(key psk, category string) (msg1, msg2 []byte, hs handshakeSide, err error) {
	clientInitRaw, err := f.readText()
	if err != nil {
		return nil, nil, hs, err
	}
	var ci clientInit
	if err := decodeEnvelope(clientInitRaw, "client/init", &ci); err != nil {
		return nil, nil, hs, err
	}
	roomPub, err := parseID(ci.ClientID)
	if err != nil {
		return nil, nil, hs, err
	}
	f.roomID = ci.ClientID

	serverInitRaw, _ := encodeEnvelope("server/init", serverInit{ServerID: f.in.id.id(), Version: coreVersion})
	if err := f.ws.WriteMessage(websocket.TextMessage, serverInitRaw); err != nil {
		return nil, nil, hs, err
	}
	hs.prologue = append(append([]byte(nil), clientInitRaw...), serverInitRaw...)
	hs.roomPub = roomPub

	state, msg1 := f.in.message1(f.t, roomPub, hs.prologue, key, category)
	hs.state = state
	env, _ := encodeNoiseHandshake(msg1)
	if err := f.ws.WriteMessage(websocket.TextMessage, env); err != nil {
		return nil, nil, hs, err
	}

	msg2Raw, err := f.readText()
	if err != nil {
		return msg1, nil, hs, err
	}
	msg2, err = parseNoiseHandshake(msg2Raw)
	return msg1, msg2, hs, err
}

// handshakeSide is what the server side holds between message 1 and message 2.
type handshakeSide struct {
	prologue []byte
	roomPub  []byte
	state    *noise.HandshakeState
}

// handshake connects on key, which the room must hold.
func (f *server) handshake(key psk, category string) error {
	_, msg2, hs, err := f.preamble(key, category)
	if err != nil {
		return err
	}
	tr, err := finish(hs.state, msg2)
	if err != nil {
		return err
	}
	f.install(tr)
	return nil
}

// handshakeFallback names a key the room does not hold and verifies message 2 against the Sentinel
// instead, which is what a server does when the room has lost its record.
func (f *server) handshakeFallback(lost psk) error {
	f.in.random = &zeros{}
	_, msg2, hs, err := f.preamble(lost, "lt")
	if err != nil {
		return err
	}
	if _, err := finish(hs.state, msg2); err == nil {
		return errors.New("message 2 verified against the lost key")
	}
	f.in.random = &zeros{}
	state, _ := f.in.message1As(f.t, hs.roomPub, hs.prologue, sentinelPSK(), lost, "lt")
	tr, err := finish(state, msg2)
	if err != nil {
		return err
	}
	f.install(tr)
	return nil
}

func (f *server) install(tr *transport) {
	f.mu.Lock()
	f.tr = tr
	f.mu.Unlock()
	_ = f.ws.SetReadDeadline(time.Time{})
	go f.read()
}

// read answers clock sync itself and hands everything else to the test. A re-handshake reply is handed
// to rekey and the reader waits for the new keys before touching the next frame.
func (f *server) read() {
	defer close(f.closed)
	for {
		mt, data, err := f.ws.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			f.t.Errorf("room sent a text frame in transport mode")
			return
		}
		f.mu.Lock()
		pt, err := f.tr.decrypt(data)
		f.mu.Unlock()
		if err != nil {
			f.t.Errorf("decrypting what the room sent: %v", err)
			return
		}
		msgType, payload, done, err := f.reasm.feed(pt)
		if err != nil {
			f.t.Errorf("room framing: %v", err)
			return
		}
		if !done || msgType != msgJSON {
			continue
		}
		var env envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			f.t.Errorf("room sent malformed JSON: %v", err)
			return
		}
		switch env.Type {
		case typeClientTime:
			// Nothing flows during a re-handshake: a probe sent under the old keys goes unanswered, as a
			// real server leaves it.
			if f.rekeying.Load() {
				continue
			}
			var ct clientTime
			_ = json.Unmarshal(env.Payload, &ct)
			now := time.Now().UnixMicro()
			f.send(typeServerTime, serverTime{ClientTransmitted: ct.ClientTransmitted, ServerReceived: now, ServerTransmitted: now})
		case "noise/handshake":
			f.rekeyResp <- payload
			<-f.rekeyDone
		default:
			f.msgs <- env
		}
	}
}

func (f *server) send(msgType string, payload any) {
	raw, err := encodeEnvelope(msgType, payload)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ct, err := f.tr.encrypt(append([]byte{msgJSON}, raw...))
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.ws.WriteMessage(websocket.BinaryMessage, ct); err != nil {
		f.t.Logf("send %s: %v", msgType, err)
	}
}

// expect is the next message the room sends, which must be of the given type. A goodbye arrives just
// before the socket closes, so a closed socket is only a failure once the queue is empty.
func (f *server) expect(msgType string, into any) {
	f.t.Helper()
	var env envelope
	select {
	case env = <-f.msgs:
	case <-f.closed:
		select {
		case env = <-f.msgs:
		default:
			f.t.Fatalf("room hung up before sending %s", msgType)
		}
	case <-time.After(wait):
		f.t.Fatalf("room did not send %s", msgType)
	}
	if env.Type != msgType {
		f.t.Fatalf("room sent %s, want %s: %s", env.Type, msgType, env.Payload)
	}
	if into != nil {
		if err := json.Unmarshal(env.Payload, into); err != nil {
			f.t.Fatalf("decoding %s: %v", msgType, err)
		}
	}
}

// rekey runs an in-band re-handshake onto key from the server side.
func (f *server) rekey(key psk, category string) {
	f.t.Helper()
	f.mu.Lock()
	prologue := append([]byte(nil), f.tr.hash...)
	f.mu.Unlock()

	state, msg1 := f.in.message1(f.t, mustParseID(f.roomID), prologue, key, category)
	env, _ := encodeNoiseHandshake(msg1)
	f.rekeying.Store(true)
	defer f.rekeying.Store(false)
	f.mu.Lock()
	ct, _ := f.tr.encrypt(append([]byte{msgJSON}, env...))
	_ = f.ws.WriteMessage(websocket.BinaryMessage, ct)
	f.mu.Unlock()

	var msg2Raw []byte
	select {
	case msg2Raw = <-f.rekeyResp:
	case <-f.closed:
		f.t.Fatal("room hung up during re-handshake")
	case <-time.After(wait):
		f.t.Fatal("room did not answer the re-handshake")
	}
	msg2, err := parseNoiseHandshake(msg2Raw)
	if err != nil {
		f.t.Fatal(err)
	}
	tr, err := finish(state, msg2)
	if err != nil {
		f.t.Fatalf("re-handshake: %v", err)
	}
	f.mu.Lock()
	f.tr = tr
	f.reasm.reset()
	f.mu.Unlock()
	f.rekeyDone <- struct{}{}
}

func (f *server) hungUp() bool {
	select {
	case <-f.closed:
		return true
	case <-time.After(wait):
		return false
	}
}

func mustParseID(s string) []byte {
	b, err := parseID(s)
	if err != nil {
		panic(err)
	}
	return b
}

// playback is the activate that hands the room the player role.
func playback() serverActivate {
	return serverActivate{Activities: []string{activityPlayback}, ActiveRoles: roles(rolePlayer)}
}

func TestUnpairedServerPlays(t *testing.T) {
	r := startRoom(t, true)
	srv := dial(t, r)
	if err := srv.handshake(sentinelPSK(), "sn"); err != nil {
		t.Fatal(err)
	}

	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	var hello clientHello
	srv.expect(typeClientHello, &hello)
	if hello.TrustLevel != trustNone || !hello.UnpairedAccess.Enabled || hello.Name != "Test Room" {
		t.Fatalf("hello %+v", hello)
	}
	if len(hello.SupportedPairMethods) != 1 || hello.SupportedPairMethods[0].Method != methodPairingPSK {
		t.Fatalf("pair methods %+v", hello.SupportedPairMethods)
	}
	if hello.PlayerSupport == nil || len(hello.PlayerSupport.SupportedFormats) != 3 || hello.PlayerSupport.BufferCapacity != bufferCapacity {
		t.Fatalf("player support %+v", hello.PlayerSupport)
	}

	// Held at nothing until the operator approves, then given playback.
	srv.send(typeServerActivate, serverActivate{Activities: []string{}, ActiveRoles: roles()})
	srv.send(typeServerActivate, playback())

	var st clientState
	srv.expect(typeClientState, &st)
	if !st.Available || st.Player == nil || st.Player.RequiredLeadTimeMs != requiredLeadMs || st.Player.MinBufferMs != minBufferMs {
		t.Fatalf("state %+v", st)
	}
	if len(hello.PlayerSupport.SupportedCommands) != 2 {
		t.Fatalf("commands %v", hello.PlayerSupport.SupportedCommands)
	}
	if !r.toldSecurity(securityUnpaired) {
		t.Fatalf("security %v", r.security)
	}

	_ = srv.ws.Close()
	if err := r.result(); err != nil {
		t.Fatalf("session ended with %v", err)
	}
}

func TestUnpairedAccessOffDemandsPairing(t *testing.T) {
	r := startRoom(t, false)
	srv := dial(t, r)
	if err := srv.handshake(sentinelPSK(), "sn"); err != nil {
		t.Fatal(err)
	}
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	srv.expect(typeClientHello, nil)
	srv.send(typeServerActivate, playback())

	var bye clientGoodbye
	srv.expect(typeClientGoodbye, &bye)
	if bye.Reason != goodbyePairingRequired {
		t.Fatalf("goodbye %q", bye.Reason)
	}
	if !srv.hungUp() {
		t.Fatal("room stayed connected")
	}
	if err := r.result(); err != nil {
		t.Fatalf("session ended with %v", err)
	}
}

func TestPairingWithTheToken(t *testing.T) {
	r := startRoom(t, false)
	srv := dial(t, r)

	// The operator entered the token, so the server holds the pairing key and connects on it.
	if err := srv.handshake(r.trust.pairingPSK(), "pr"); err != nil {
		t.Fatal(err)
	}
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	srv.expect(typeClientHello, nil)
	srv.send(typeServerActivate, serverActivate{
		Activities: []string{activityPairing}, ActiveRoles: roles(),
		Pairing: &activatePairing{Method: methodPairingPSK},
	})

	var fin pairFinalize
	srv.expect(typePairFinalize, &fin)
	longTerm, err := parsePSK(fin.LongTermPSK)
	if err != nil {
		t.Fatal(err)
	}
	if !r.toldSecurity(securityPairing) {
		t.Fatalf("security %v", r.security)
	}
	srv.send(typePairFinalized, struct{}{})

	// Promote the channel to the new key and start over on it.
	srv.rekey(longTerm, "lt")
	if !r.trust.paired(srv.in.id.id()) {
		t.Fatal("room did not keep the pairing")
	}
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	var hello clientHello
	srv.expect(typeClientHello, &hello)
	if hello.TrustLevel != trustUser {
		t.Fatalf("trust level %q after pairing", hello.TrustLevel)
	}
	srv.send(typeServerActivate, playback())
	var st clientState
	srv.expect(typeClientState, &st)
	if !st.Available {
		t.Fatal("not available")
	}
	if !r.toldSecurity(securityPaired) {
		t.Fatalf("security %v", r.security)
	}

	// A paired server comes back on its record, with no pairing key involved.
	_ = srv.ws.Close()
	if err := r.result(); err != nil {
		t.Fatal(err)
	}
	again := dial(t, r)
	again.in = srv.in
	if err := again.handshake(longTerm, "lt"); err != nil {
		t.Fatalf("reconnecting on the record: %v", err)
	}
	again.send(typeServerHello, serverHello{Name: "Music Assistant"})
	again.expect(typeClientHello, &hello)
	if hello.TrustLevel != trustUser {
		t.Fatalf("trust level %q on return", hello.TrustLevel)
	}
}

func TestPairingOnTheWrongKeyIsAborted(t *testing.T) {
	r := startRoom(t, true)
	srv := dial(t, r)
	if err := srv.handshake(sentinelPSK(), "sn"); err != nil {
		t.Fatal(err)
	}
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	srv.expect(typeClientHello, nil)
	srv.send(typeServerActivate, serverActivate{
		Activities: []string{activityPairing}, ActiveRoles: roles(),
		Pairing: &activatePairing{Method: methodPairingPSK},
	})
	var ab pairAbort
	srv.expect(typePairAbort, &ab)
	if ab.Reason != abortMethodNotSupported {
		t.Fatalf("abort %q", ab.Reason)
	}
	// The connection stays up and can still be used.
	srv.send(typeServerActivate, serverActivate{Activities: []string{}, ActiveRoles: roles()})
	if srv.hungUp() {
		t.Fatal("room hung up after a pair/abort")
	}
}

func TestLostRecordFallsBackToSentinel(t *testing.T) {
	r := startRoom(t, true)
	srv := dial(t, r)
	lost, _ := newPSK()
	if err := srv.handshakeFallback(lost); err != nil {
		t.Fatal(err)
	}
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	var hello clientHello
	srv.expect(typeClientHello, &hello)
	if hello.TrustLevel != trustNone {
		t.Fatalf("trust level %q after falling back", hello.TrustLevel)
	}
	srv.send(typeServerActivate, serverActivate{Activities: []string{}, ActiveRoles: roles()})
	if srv.hungUp() {
		t.Fatal("room hung up on a sentinel connection")
	}
}

func TestRecordBoundToAnotherServerFailsTheHandshake(t *testing.T) {
	r := startRoom(t, true)
	other, _ := newIdentity()
	key, _ := newPSK()
	if err := r.trust.remember(other.id(), key, ""); err != nil {
		t.Fatal(err)
	}

	srv := dial(t, r)
	if err := srv.handshake(key, "lt"); err == nil {
		t.Fatal("a server presenting another server's key got in")
	}
	if err := r.result(); !errors.Is(err, errPreamble) {
		t.Fatalf("session ended with %v, want a handshake failure", err)
	}
}

func TestUnencryptedServerIsTurnedAway(t *testing.T) {
	r := startRoom(t, true)
	srv := dial(t, r)
	if _, err := srv.readText(); err != nil {
		t.Fatal(err)
	}
	legacy, _ := encodeEnvelope("server/hello", serverHello{Name: "old Music Assistant"})
	if err := srv.ws.WriteMessage(websocket.TextMessage, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.readText(); err == nil {
		t.Fatal("room answered an unencrypted server")
	}
	if err := r.result(); !errors.Is(err, errPreamble) {
		t.Fatalf("session ended with %v", err)
	}
}

func TestServerUnpairForgetsTheRecord(t *testing.T) {
	r := startRoom(t, true)
	srv := dial(t, r)
	key, _ := newPSK()
	if err := r.trust.remember(srv.in.id.id(), key, ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.handshake(key, "lt"); err != nil {
		t.Fatal(err)
	}
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	srv.expect(typeClientHello, nil)
	srv.send(typeServerActivate, playback())
	srv.expect(typeClientState, nil)

	srv.send(typeServerUnpair, struct{}{})
	var bye clientGoodbye
	srv.expect(typeClientGoodbye, &bye)
	if bye.Reason != goodbyeUnpaired {
		t.Fatalf("goodbye %q", bye.Reason)
	}
	if r.trust.paired(srv.in.id.id()) {
		t.Fatal("record kept after server/unpair")
	}
}

func TestWithdrawingUnpairedAccessSaysSo(t *testing.T) {
	r := startRoom(t, true)
	srv := dial(t, r)
	if err := srv.handshake(sentinelPSK(), "sn"); err != nil {
		t.Fatal(err)
	}
	s := r.session()
	srv.send(typeServerHello, serverHello{Name: "Music Assistant"})
	srv.expect(typeClientHello, nil)
	srv.send(typeServerActivate, playback())
	srv.expect(typeClientState, nil)

	s.kick(goodbyePairingRequired)
	var bye clientGoodbye
	srv.expect(typeClientGoodbye, &bye)
	if bye.Reason != goodbyePairingRequired {
		t.Fatalf("goodbye %q", bye.Reason)
	}
	if !srv.hungUp() {
		t.Fatal("room stayed connected")
	}
}
