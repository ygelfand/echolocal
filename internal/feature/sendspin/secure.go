package sendspin

// The cryptographic half of the protocol: identities, pre-shared keys, the Noise KKpsk2 handshake the
// server initiates and this room answers, and the framing of what travels inside the channel.
//
// The room is always the Noise responder, whichever side opened the socket. What this file does not
// do is talk to a socket: conn.go feeds it the bytes and sends what it produces.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flynn/noise"
	"golang.org/x/crypto/curve25519"
)

const (
	// keySize is the size of a Curve25519 key and of a PSK.
	keySize = 32

	// idLength is a 32-byte value in unpadded base64url: identities, psk_ids and PSKs are all this long
	// on the wire.
	idLength = 43

	// coreVersion is the version of the cleartext init exchange. Nothing else is accepted.
	coreVersion = 1

	// The two suites the spec defines. Servers must speak both, so we pick and say which.
	suiteChaChaPoly = "25519_ChaChaPoly_SHA256"
	suiteAESGCM     = "25519_AESGCM_SHA256"

	// The largest Noise transport message, and the plaintext that fits in one once the AEAD tag is
	// taken off. The message type byte counts against the plaintext.
	maxNoiseMessage = 65535
	aeadTagSize     = 16
	maxPlaintext    = maxNoiseMessage - aeadTagSize
)

// Message type bytes: the first byte of every decrypted frame.
const (
	msgJSON byte = 0

	// msgFragment is the spec's fragmentation: [1][flags][orig_type][data] to open, [1][flags][data]
	// to continue, with bit 1 of flags set on the first frame and bit 0 on the last.
	msgFragment byte = 1

	// msgFragmentMore and msgFragmentEnd are the pre-spec fragmentation that Music Assistant speaks
	// today: [2][orig_type][data] to open, [2][data] to continue, [3][data] to close. Both are read;
	// neither is ever sent, because nothing this room says needs more than one frame.
	msgFragmentMore byte = 2
	msgFragmentEnd  byte = 3

	// msgAudio is a player audio chunk on slot 0.
	msgAudio byte = 4
)

// maxReassembled bounds a fragmented message. The spec sets no limit; a peer streaming fragments for
// ever must not be able to take the device's memory with it.
const maxReassembled = 4 << 20

// b64 is unpadded base64url, the encoding of every 32-byte value the protocol puts in JSON.
var b64 = base64.RawURLEncoding

// The labels the spec hashes with. Exact bytes: no separator, no terminator.
const (
	pskIDLabel    = "sendspin-psk-id-v1"
	sentinelLabel = "sendspin-sentinel-psk-v1"
)

// identity is the room's static Curve25519 keypair. Its public key is the client_id: rotate it and
// every server sees a new device, so it is generated once and kept (see trust.go).
type identity struct {
	priv [keySize]byte
	pub  [keySize]byte
}

func newIdentity() (*identity, error) {
	var priv [keySize]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return nil, fmt.Errorf("generating an identity: %w", err)
	}
	return identityFrom(priv[:])
}

// identityFrom rebuilds an identity from its stored private key.
func identityFrom(priv []byte) (*identity, error) {
	if len(priv) != keySize {
		return nil, fmt.Errorf("identity key is %d bytes, want %d", len(priv), keySize)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("deriving the public key: %w", err)
	}
	id := &identity{}
	copy(id.priv[:], priv)
	copy(id.pub[:], pub)
	return id, nil
}

// id is the wire form of the public key: the client_id.
func (id *identity) id() string { return b64.EncodeToString(id.pub[:]) }

func (id *identity) keypair() noise.DHKey {
	return noise.DHKey{Private: append([]byte(nil), id.priv[:]...), Public: append([]byte(nil), id.pub[:]...)}
}

// parseID decodes a wire identity into the raw public key.
func parseID(s string) ([]byte, error) {
	if len(s) != idLength {
		return nil, fmt.Errorf("identity is %d characters, want %d", len(s), idLength)
	}
	raw, err := b64.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("identity is not base64url: %w", err)
	}
	if len(raw) != keySize {
		return nil, fmt.Errorf("identity decodes to %d bytes, want %d", len(raw), keySize)
	}
	return raw, nil
}

// psk is a pre-shared key mixed into the handshake. Long-term, pairing and Sentinel keys share the type;
// the category a key is held under, not its value, says what a match means.
type psk [keySize]byte

func newPSK() (psk, error) {
	var p psk
	if _, err := rand.Read(p[:]); err != nil {
		return psk{}, fmt.Errorf("generating a key: %w", err)
	}
	return p, nil
}

// id is what the server names the key by in Noise message 1, so the room can pick the right one before
// it has to mix it in.
func (p psk) id() string {
	h := sha256.New()
	h.Write([]byte(pskIDLabel))
	h.Write(p[:])
	return b64.EncodeToString(h.Sum(nil))
}

func (p psk) String() string { return b64.EncodeToString(p[:]) }

func parsePSK(s string) (psk, error) {
	raw, err := parseID(s)
	if err != nil {
		return psk{}, err
	}
	var p psk
	copy(p[:], raw)
	return p, nil
}

// sentinelPSK is the published constant used when no other key applies. Its value is public, so it
// authenticates nothing: it is what an unpaired connection runs on.
func sentinelPSK() psk { return psk(sha256.Sum256([]byte(sentinelLabel))) }

// pskCategory is what a key is held as, which decides what the server may do on a connection keyed
// with it.
type pskCategory int

const (
	categorySentinel pskCategory = iota
	categoryPairing
	categoryLongTerm
)

func (c pskCategory) String() string {
	switch c {
	case categorySentinel:
		return "sentinel"
	case categoryPairing:
		return "pairing"
	case categoryLongTerm:
		return "long-term"
	}
	return fmt.Sprintf("category(%d)", int(c))
}

// wire is the two-letter form the server may put in message 1.
func (c pskCategory) wire() string {
	switch c {
	case categoryPairing:
		return "pr"
	case categoryLongTerm:
		return "lt"
	}
	return "sn"
}

// envelope is the {type, payload} wrapper on every JSON message.
type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func encodeEnvelope(msgType string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", msgType, err)
	}
	return json.Marshal(envelope{Type: msgType, Payload: raw})
}

func decodeEnvelope(raw []byte, want string, payload any) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("malformed message: %w", err)
	}
	if env.Type != want {
		return fmt.Errorf("expected %s, got %q", want, env.Type)
	}
	if payload == nil {
		return nil
	}
	if err := json.Unmarshal(env.Payload, payload); err != nil {
		return fmt.Errorf("malformed %s: %w", want, err)
	}
	return nil
}

// The cleartext preamble, sent as text frames. The handshake hashes the exact bytes of both, so what
// is encoded here is what goes on the wire, byte for byte.
type clientInit struct {
	ClientID string `json:"client_id"`
	Version  int    `json:"version"`
	Suite    string `json:"suite"`
}

type serverInit struct {
	ServerID string `json:"server_id"`
	Version  int    `json:"version"`
}

func encodeClientInit(id *identity, suite string) ([]byte, error) {
	return encodeEnvelope("client/init", clientInit{ClientID: id.id(), Version: coreVersion, Suite: suite})
}

func parseServerInit(raw []byte) (serverInit, error) {
	var si serverInit
	if err := decodeEnvelope(raw, "server/init", &si); err != nil {
		return serverInit{}, err
	}
	if si.Version != coreVersion {
		return serverInit{}, fmt.Errorf("server/init version %d, want %d", si.Version, coreVersion)
	}
	if _, err := parseID(si.ServerID); err != nil {
		return serverInit{}, fmt.Errorf("server/init server_id: %w", err)
	}
	return si, nil
}

// noiseHandshake is the envelope both Noise messages travel in.
type noiseHandshake struct {
	Data string `json:"data"`
}

func encodeNoiseHandshake(msg []byte) ([]byte, error) {
	return encodeEnvelope("noise/handshake", noiseHandshake{Data: b64.EncodeToString(msg)})
}

func parseNoiseHandshake(raw []byte) ([]byte, error) {
	var nh noiseHandshake
	if err := decodeEnvelope(raw, "noise/handshake", &nh); err != nil {
		return nil, err
	}
	msg, err := b64.DecodeString(nh.Data)
	if err != nil {
		return nil, fmt.Errorf("noise/handshake data is not base64url: %w", err)
	}
	return msg, nil
}

// message1 is what the server encrypts into Noise message 1: which key it means to mix in. The
// category is newer than Music Assistant's library and may be absent.
type message1 struct {
	PSKID    string `json:"psk_id"`
	Category string `json:"psk_category,omitempty"`
}

// transport is an established channel: one cipher state per direction and the handshake hash, which
// seeds any re-handshake.
type transport struct {
	send  *noise.CipherState
	recv  *noise.CipherState
	hash  []byte
	suite string
}

func (t *transport) encrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext) > maxPlaintext {
		return nil, fmt.Errorf("frame of %d bytes exceeds %d", len(plaintext), maxPlaintext)
	}
	ct, err := t.send.Encrypt(nil, nil, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypting: %w", err)
	}
	return ct, nil
}

func (t *transport) decrypt(ciphertext []byte) ([]byte, error) {
	pt, err := t.recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	return pt, nil
}

func cipherSuite(suite string) (noise.CipherSuite, error) {
	switch suite {
	case suiteChaChaPoly:
		return noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256), nil
	case suiteAESGCM:
		return noise.NewCipherSuite(noise.DH25519, noise.CipherAESGCM, noise.HashSHA256), nil
	}
	return nil, fmt.Errorf("unknown cipher suite %q", suite)
}

func handshakeState(suite string, id *identity, peerPub, prologue []byte, key psk, initiator bool) (*noise.HandshakeState, error) {
	cs, err := cipherSuite(suite)
	if err != nil {
		return nil, err
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           cs,
		Random:                rand.Reader,
		Pattern:               noise.HandshakeKK,
		Initiator:             initiator,
		Prologue:              prologue,
		StaticKeypair:         id.keypair(),
		PeerStatic:            peerPub,
		PresharedKey:          append([]byte(nil), key[:]...),
		PresharedKeyPlacement: 2,
	})
	if err != nil {
		return nil, fmt.Errorf("noise: %w", err)
	}
	return hs, nil
}

// pskLookup finds the key the server named, and what it is held as. errPSKMiss is a key the room does
// not hold; any other error is a key it holds but must not use here, such as one bound to a different
// server, which fails the handshake outright.
type pskLookup func(pskID string) (psk, pskCategory, error)

var errPSKMiss = errors.New("key not held")

// handshakeResult is what the room learns from answering Noise message 1.
type handshakeResult struct {
	message2  []byte
	transport *transport
	category  pskCategory

	// fellBack is a miss the room completed on the Sentinel anyway: the server named a key the room no
	// longer holds, which the server learns from the handshake itself and can offer re-pairing for.
	fellBack bool
}

// errNoPSK is a miss where falling back is not allowed: a re-handshake, which the spec has fail.
var errNoPSK = errors.New("no key matches what the server named")

// respond answers Noise message 1. The key is mixed in at the end of message 2, so message 1 can be read
// without it — but flynn/noise fixes the key when the state is made. So message 1 is read once with a
// throwaway state to learn which key the server means, then again with a state built on that key, which
// is the one that writes message 2.
func respond(suite string, id *identity, peerPub, prologue, message1Bytes []byte, lookup pskLookup, allowFallback bool) (handshakeResult, error) {
	probe, err := handshakeState(suite, id, peerPub, prologue, sentinelPSK(), false)
	if err != nil {
		return handshakeResult{}, err
	}
	payload, _, _, err := probe.ReadMessage(nil, message1Bytes)
	if err != nil {
		return handshakeResult{}, fmt.Errorf("reading noise message 1: %w", err)
	}
	var m1 message1
	if err := json.Unmarshal(payload, &m1); err != nil {
		return handshakeResult{}, fmt.Errorf("malformed noise message 1: %w", err)
	}

	key, category, err := lookup(m1.PSKID)
	// A key held under another category than the server declared is a miss too: the two sides would
	// not agree on what the connection is for.
	if err == nil && m1.Category != "" && m1.Category != category.wire() {
		err = errPSKMiss
	}
	fellBack := false
	switch {
	case err == nil:
	case errors.Is(err, errPSKMiss) && allowFallback:
		key, category, fellBack = sentinelPSK(), categorySentinel, m1.PSKID != sentinelPSK().id()
	case errors.Is(err, errPSKMiss):
		return handshakeResult{}, errNoPSK
	default:
		return handshakeResult{}, err
	}

	hs, err := handshakeState(suite, id, peerPub, prologue, key, false)
	if err != nil {
		return handshakeResult{}, err
	}
	if _, _, _, err := hs.ReadMessage(nil, message1Bytes); err != nil {
		return handshakeResult{}, fmt.Errorf("re-reading noise message 1: %w", err)
	}
	message2, csInit, csResp, err := hs.WriteMessage(nil, []byte("{}"))
	if err != nil {
		return handshakeResult{}, fmt.Errorf("writing noise message 2: %w", err)
	}
	if csInit == nil || csResp == nil {
		return handshakeResult{}, errors.New("handshake did not complete")
	}

	// Split hands back initiator-to-responder first: that is what we receive with.
	return handshakeResult{
		message2:  message2,
		transport: &transport{send: csResp, recv: csInit, hash: append([]byte(nil), hs.ChannelBinding()...), suite: suite},
		category:  category,
		fellBack:  fellBack,
	}, nil
}

// reassembler puts a fragmented message back together. One message is in flight at a time, in either
// of the two encodings described above.
type reassembler struct {
	inFlight bool
	origType byte
	buf      []byte
}

// feed takes one decrypted frame. done is false while a fragmented message is still arriving.
func (r *reassembler) feed(frame []byte) (msgType byte, payload []byte, done bool, err error) {
	if len(frame) == 0 {
		return 0, nil, false, errors.New("empty frame")
	}
	t, data := frame[0], frame[1:]

	switch t {
	case msgFragment:
		if len(data) == 0 {
			return 0, nil, false, errors.New("fragment without flags")
		}
		flags, data := data[0], data[1:]
		if flags&^0x03 != 0 {
			return 0, nil, false, fmt.Errorf("fragment flags %#x have reserved bits set", flags)
		}
		first, last := flags&0x02 != 0, flags&0x01 != 0
		if first == r.inFlight {
			return 0, nil, false, errors.New("fragment out of sequence")
		}
		if first {
			if len(data) == 0 {
				return 0, nil, false, errors.New("first fragment without orig_type")
			}
			if data[0] == msgFragment {
				return 0, nil, false, errors.New("fragment of a fragment")
			}
			r.inFlight, r.origType, data = true, data[0], data[1:]
		}
		if err := r.grow(data); err != nil {
			return 0, nil, false, err
		}
		if !last {
			return 0, nil, false, nil
		}
		return r.finish()

	case msgFragmentMore:
		if !r.inFlight {
			if len(data) == 0 {
				return 0, nil, false, errors.New("first fragment without orig_type")
			}
			r.inFlight, r.origType, data = true, data[0], data[1:]
		}
		return 0, nil, false, r.grow(data)

	case msgFragmentEnd:
		if !r.inFlight {
			return 0, nil, false, errors.New("last fragment with nothing in flight")
		}
		if err := r.grow(data); err != nil {
			return 0, nil, false, err
		}
		return r.finish()

	default:
		if r.inFlight {
			return 0, nil, false, fmt.Errorf("frame type %d in the middle of a fragmented message", t)
		}
		return t, data, true, nil
	}
}

func (r *reassembler) grow(data []byte) error {
	if len(r.buf)+len(data) > maxReassembled {
		r.reset()
		return fmt.Errorf("fragmented message exceeds %d bytes", maxReassembled)
	}
	r.buf = append(r.buf, data...)
	return nil
}

func (r *reassembler) finish() (byte, []byte, bool, error) {
	t, payload := r.origType, r.buf
	r.reset()
	return t, payload, true, nil
}

func (r *reassembler) reset() {
	r.inFlight = false
	r.origType = 0
	r.buf = nil
}
