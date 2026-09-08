package sendspin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"

	"github.com/flynn/noise"
)

// The published constants from the spec's Encryption section. Getting these wrong means agreeing with
// nobody, so they are pinned here byte for byte.
func TestSentinelMatchesSpec(t *testing.T) {
	want, _ := hex.DecodeString("1b5e24dbc1aed95fc2a5a338a90c05df44bd10f5ec1f4cd66cbf86272767b9d3")
	s := sentinelPSK()
	if !bytes.Equal(s[:], want) {
		t.Fatalf("sentinel psk = %x, want %x", s[:], want)
	}
	if got := s.id(); got != "GFsV9tLaSQm9HcFWpKsgYQOr7wFTvNUtkmFwuVz3zoo" {
		t.Fatalf("sentinel psk_id = %q", got)
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	id, err := newIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(id.id()) != idLength {
		t.Fatalf("id %q is %d chars", id.id(), len(id.id()))
	}
	again, err := identityFrom(id.priv[:])
	if err != nil {
		t.Fatal(err)
	}
	if again.id() != id.id() {
		t.Fatal("rebuilding from the private key changed the identity")
	}
	pub, err := parseID(id.id())
	if err != nil || !bytes.Equal(pub, id.pub[:]) {
		t.Fatalf("parseID: %v", err)
	}
	if _, err := parseID("short"); err == nil {
		t.Fatal("a short id parsed")
	}
}

// initiator is the server's half of the handshake, which the room never runs but every test needs.
// Random is settable so message 1 can be reproduced, which is how a server falls back to the Sentinel
// after the room did: it has to verify the same message 2 against another key.
type initiator struct {
	id     *identity
	random io.Reader
}

func newInitiator(t *testing.T) *initiator {
	t.Helper()
	id, err := newIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return &initiator{id: id}
}

// message1 builds Noise message 1 naming key, with the category if the server is new enough to say.
func (in *initiator) message1(t *testing.T, peerPub, prologue []byte, key psk, category string) (*noise.HandshakeState, []byte) {
	t.Helper()
	return in.message1As(t, peerPub, prologue, key, key, category)
}

// message1As mixes one key while naming another. The payload carries the name, so a server falling back
// to the Sentinel has to keep naming the key it meant to reproduce the message the room answered.
func (in *initiator) message1As(t *testing.T, peerPub, prologue []byte, mix, named psk, category string) (*noise.HandshakeState, []byte) {
	t.Helper()
	cs, _ := cipherSuite(suiteChaChaPoly)
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           cs,
		Random:                in.random,
		Pattern:               noise.HandshakeKK,
		Initiator:             true,
		Prologue:              prologue,
		StaticKeypair:         in.id.keypair(),
		PeerStatic:            peerPub,
		PresharedKey:          append([]byte(nil), mix[:]...),
		PresharedKeyPlacement: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(message1{PSKID: named.id(), Category: category})
	msg1, _, _, err := hs.WriteMessage(nil, payload)
	if err != nil {
		t.Fatal(err)
	}
	return hs, msg1
}

// finish reads message 2 and hands back the server's transport.
func finish(hs *noise.HandshakeState, msg2 []byte) (*transport, error) {
	_, csSend, csRecv, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		return nil, err
	}
	return &transport{send: csSend, recv: csRecv, hash: hs.ChannelBinding(), suite: suiteChaChaPoly}, nil
}

// zeros is a deterministic random source, so two handshake states make the same message 1.
type zeros struct{ b byte }

func (z *zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = z.b
		z.b++
	}
	return len(p), nil
}

func TestHandshakeSelectsTheNamedKey(t *testing.T) {
	room, _ := newIdentity()
	server := newInitiator(t)
	longTerm, _ := newPSK()
	prologue := []byte("client/init+server/init")

	lookup := func(pskID string) (psk, pskCategory, error) {
		if pskID == longTerm.id() {
			return longTerm, categoryLongTerm, nil
		}
		return psk{}, 0, errPSKMiss
	}

	hs, msg1 := server.message1(t, room.pub[:], prologue, longTerm, "lt")
	res, err := respond(suiteChaChaPoly, room, server.id.pub[:], prologue, msg1, lookup, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.category != categoryLongTerm || res.fellBack {
		t.Fatalf("category %v fellBack %v", res.category, res.fellBack)
	}
	st, err := finish(hs, res.message2)
	if err != nil {
		t.Fatal(err)
	}

	// Both directions, and the hash agrees: that is what a re-handshake seeds from.
	ct, _ := st.encrypt([]byte{msgJSON, 'h', 'i'})
	pt, err := res.transport.decrypt(ct)
	if err != nil || string(pt[1:]) != "hi" {
		t.Fatalf("server to room: %v %q", err, pt)
	}
	ct, _ = res.transport.encrypt([]byte{msgJSON, 'y', 'o'})
	pt, err = st.decrypt(ct)
	if err != nil || string(pt[1:]) != "yo" {
		t.Fatalf("room to server: %v %q", err, pt)
	}
	if !bytes.Equal(st.hash, res.transport.hash) {
		t.Fatal("handshake hashes differ")
	}

	// A replayed ciphertext fails: the counters moved on.
	if _, err := st.decrypt(ct); err == nil {
		t.Fatal("replay decrypted")
	}
}

func TestHandshakeCategoryMismatchIsAMiss(t *testing.T) {
	room, _ := newIdentity()
	server := newInitiator(t)
	key, _ := newPSK()
	lookup := func(string) (psk, pskCategory, error) { return key, categoryPairing, nil }

	_, msg1 := server.message1(t, room.pub[:], nil, key, "lt")
	_, err := respond(suiteChaChaPoly, room, server.id.pub[:], nil, msg1, lookup, false)
	if err == nil {
		t.Fatal("a key held as pairing was accepted as long-term")
	}
}

func TestHandshakeFallsBackToSentinel(t *testing.T) {
	room, _ := newIdentity()
	server := newInitiator(t)
	server.random = &zeros{}
	lost, _ := newPSK()
	miss := func(string) (psk, pskCategory, error) { return psk{}, 0, errPSKMiss }

	_, msg1 := server.message1(t, room.pub[:], nil, lost, "")
	res, err := respond(suiteChaChaPoly, room, server.id.pub[:], nil, msg1, miss, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.category != categorySentinel || !res.fellBack {
		t.Fatalf("category %v fellBack %v", res.category, res.fellBack)
	}

	// The server's intended key fails, the Sentinel verifies: the credential-mismatch signal.
	server.random = &zeros{}
	hsLost, _ := server.message1(t, room.pub[:], nil, lost, "")
	if _, err := finish(hsLost, res.message2); err == nil {
		t.Fatal("message 2 verified against a key the room never mixed")
	}
	server.random = &zeros{}
	hsSentinel, msg1Again := server.message1As(t, room.pub[:], nil, sentinelPSK(), lost, "")
	if !bytes.Equal(msg1, msg1Again) {
		t.Fatal("deterministic random did not reproduce message 1")
	}
	if _, err := finish(hsSentinel, res.message2); err != nil {
		t.Fatalf("sentinel verification: %v", err)
	}

	// No fallback in a re-handshake.
	if _, err := respond(suiteChaChaPoly, room, server.id.pub[:], nil, msg1, miss, false); err == nil {
		t.Fatal("re-handshake fell back")
	}
}

func TestHandshakeMisbindingFails(t *testing.T) {
	room, _ := newIdentity()
	server := newInitiator(t)
	key, _ := newPSK()
	bound := func(string) (psk, pskCategory, error) { return psk{}, 0, io.ErrUnexpectedEOF }

	_, msg1 := server.message1(t, room.pub[:], nil, key, "")
	if _, err := respond(suiteChaChaPoly, room, server.id.pub[:], nil, msg1, bound, true); err == nil {
		t.Fatal("a misbound key fell back instead of failing")
	}
}

func TestHandshakePrologueMustMatch(t *testing.T) {
	room, _ := newIdentity()
	server := newInitiator(t)
	lookup := func(string) (psk, pskCategory, error) { return sentinelPSK(), categorySentinel, nil }

	_, msg1 := server.message1(t, room.pub[:], []byte("what the server sent"), sentinelPSK(), "")
	if _, err := respond(suiteChaChaPoly, room, server.id.pub[:], []byte("what the room saw"), msg1, lookup, true); err == nil {
		t.Fatal("tampered init exchange handshook")
	}
}

func TestReassemblerBothEncodings(t *testing.T) {
	// The pre-spec encoding Music Assistant sends today.
	var r reassembler
	feed := func(frame []byte) (byte, []byte, bool) {
		t.Helper()
		mt, p, done, err := r.feed(frame)
		if err != nil {
			t.Fatal(err)
		}
		return mt, p, done
	}
	if _, _, done := feed([]byte{msgFragmentMore, msgAudio, 1, 2}); done {
		t.Fatal("opening fragment completed")
	}
	if _, _, done := feed([]byte{msgFragmentMore, 3}); done {
		t.Fatal("middle fragment completed")
	}
	mt, p, done := feed([]byte{msgFragmentEnd, 4})
	if !done || mt != msgAudio || !bytes.Equal(p, []byte{1, 2, 3, 4}) {
		t.Fatalf("old encoding: done %v type %d payload %v", done, mt, p)
	}

	// The spec's encoding: first flag 0x02, last flag 0x01.
	if _, _, done := feed([]byte{msgFragment, 0x02, msgJSON, 'a'}); done {
		t.Fatal("opening fragment completed")
	}
	if _, _, done := feed([]byte{msgFragment, 0x00, 'b'}); done {
		t.Fatal("middle fragment completed")
	}
	mt, p, done = feed([]byte{msgFragment, 0x01, 'c'})
	if !done || mt != msgJSON || string(p) != "abc" {
		t.Fatalf("new encoding: done %v type %d payload %q", done, mt, p)
	}

	// A single-frame message in one go, and a whole message in one fragment.
	mt, p, done = feed([]byte{msgJSON, '{', '}'})
	if !done || mt != msgJSON || string(p) != "{}" {
		t.Fatal("plain frame")
	}
	mt, p, done = feed([]byte{msgFragment, 0x03, msgAudio, 9})
	if !done || mt != msgAudio || !bytes.Equal(p, []byte{9}) {
		t.Fatal("first-and-last fragment")
	}
}

func TestReassemblerRejectsBrokenSequences(t *testing.T) {
	cases := [][][]byte{
		{{msgFragmentEnd, 1}},                                   // end with nothing in flight
		{{msgFragmentMore, msgAudio, 1}, {msgJSON, 1}},          // plain frame mid-fragment
		{{msgFragment, 0x00, 1}},                                // continuation with nothing in flight
		{{msgFragment, 0x02, msgAudio, 1}, {msgFragment, 0x02}}, // two firsts
		{{msgFragment, 0x06, msgAudio, 1}},                      // reserved bit
		{{msgFragment, 0x02, msgFragment, 1}},                   // orig_type 1
		{{}},
	}
	for i, frames := range cases {
		var r reassembler
		var err error
		for _, f := range frames {
			if _, _, _, err = r.feed(f); err != nil {
				break
			}
		}
		if err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestInitMessagesAreExactBytes(t *testing.T) {
	id, _ := newIdentity()
	raw, err := encodeClientInit(id, suiteChaChaPoly)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Type != "client/init" {
		t.Fatalf("client/init: %v %q", err, raw)
	}

	si, err := parseServerInit([]byte(`{"type":"server/init","payload":{"server_id":"` + id.id() + `","version":1}}`))
	if err != nil || si.ServerID != id.id() {
		t.Fatalf("server/init: %v", err)
	}
	if _, err := parseServerInit([]byte(`{"type":"server/init","payload":{"server_id":"` + id.id() + `","version":2}}`)); err == nil {
		t.Fatal("version 2 accepted")
	}
	if _, err := parseServerInit([]byte(`{"type":"server/hello","payload":{"name":"legacy"}}`)); err == nil {
		t.Fatal("an unencrypted server's hello passed for server/init")
	}
}
