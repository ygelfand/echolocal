package sendspin

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// conn is one server's socket: the cleartext preamble, then the encrypted channel. Reads come from one
// goroutine; writes are serialized here because time sync and the message loop both send.
type conn struct {
	ws *websocket.Conn
	id *identity

	writeMu sync.Mutex
	t       *transport
	reasm   reassembler

	// What the preamble learned, carried over into any re-handshake.
	serverID  string
	serverPub []byte
	suite     string
}

// handshakeTimeout bounds each message of the preamble, as the spec suggests. writeTimeout bounds
// every frame after it: a server that stops reading must not stop this room.
const (
	handshakeTimeout = 30 * time.Second
	writeTimeout     = 10 * time.Second
)

// errPreamble is any failure before the channel is up. The spec has these close the socket without
// an application-level word, which is what the caller does with it.
var errPreamble = errors.New("handshake failed")

func newConn(ws *websocket.Conn, id *identity) *conn {
	return &conn{ws: ws, id: id, suite: suiteChaChaPoly}
}

// preamble runs the room's half of the cleartext exchange: client/init out, server/init in, Noise
// message 1 in, message 2 out. lookup is built once the server has named itself, so a record bound to
// another server can be told apart from one the room does not hold.
func (c *conn) preamble(lookup func(serverID string) pskLookup) (handshakeResult, error) {
	c.ws.SetReadLimit(64 << 10)

	clientInitRaw, err := encodeClientInit(c.id, c.suite)
	if err != nil {
		return handshakeResult{}, err
	}
	if err := c.writeText(clientInitRaw); err != nil {
		return handshakeResult{}, fmt.Errorf("%w: sending client/init: %w", errPreamble, err)
	}

	serverInitRaw, err := c.readText()
	if err != nil {
		return handshakeResult{}, fmt.Errorf("%w: reading server/init: %w", errPreamble, err)
	}
	si, err := parseServerInit(serverInitRaw)
	if err != nil {
		return handshakeResult{}, fmt.Errorf("%w: %w", errPreamble, err)
	}
	serverPub, _ := parseID(si.ServerID)

	msg1Raw, err := c.readText()
	if err != nil {
		return handshakeResult{}, fmt.Errorf("%w: reading noise message 1: %w", errPreamble, err)
	}
	msg1, err := parseNoiseHandshake(msg1Raw)
	if err != nil {
		return handshakeResult{}, fmt.Errorf("%w: %w", errPreamble, err)
	}

	// The prologue is the exact bytes of both init messages, so tampering with either fails here.
	prologue := append(append([]byte(nil), clientInitRaw...), serverInitRaw...)
	res, err := respond(c.suite, c.id, serverPub, prologue, msg1, lookup(si.ServerID), true)
	if err != nil {
		return handshakeResult{}, fmt.Errorf("%w: %w", errPreamble, err)
	}
	msg2Raw, err := encodeNoiseHandshake(res.message2)
	if err != nil {
		return handshakeResult{}, err
	}
	if err := c.writeText(msg2Raw); err != nil {
		return handshakeResult{}, fmt.Errorf("%w: sending noise message 2: %w", errPreamble, err)
	}

	c.serverID, c.serverPub = si.ServerID, serverPub
	c.install(res.transport)
	return res, nil
}

// rehandshake answers a Noise message 1 that arrived inside the channel. Message 2 leaves under the old
// keys; only then do the new ones take over. Nothing else may be sent in between, which holding the
// write lock throughout guarantees.
func (c *conn) rehandshake(msg1Raw []byte, lookup pskLookup) (handshakeResult, error) {
	msg1, err := parseNoiseHandshake(msg1Raw)
	if err != nil {
		return handshakeResult{}, err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	res, err := respond(c.suite, c.id, c.serverPub, c.t.hash, msg1, lookup, false)
	if err != nil {
		return handshakeResult{}, err
	}
	msg2Raw, err := encodeNoiseHandshake(res.message2)
	if err != nil {
		return handshakeResult{}, err
	}
	if err := c.writeFrameLocked(msgJSON, msg2Raw); err != nil {
		return handshakeResult{}, fmt.Errorf("sending noise message 2: %w", err)
	}
	c.t = res.transport
	c.reasm.reset()
	return res, nil
}

// install switches the socket to transport mode.
func (c *conn) install(t *transport) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.t = t
	c.reasm.reset()
	c.ws.SetReadLimit(maxNoiseMessage + 1024)
	_ = c.ws.SetReadDeadline(time.Time{})
}

func (c *conn) readText() ([]byte, error) {
	if err := c.ws.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return nil, err
	}
	t, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if t != websocket.TextMessage {
		return nil, fmt.Errorf("expected a text frame, got type %d", t)
	}
	return data, nil
}

func (c *conn) writeText(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.SetWriteDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.TextMessage, data)
}

// writeFrame encrypts and sends one message. Nothing the room says is big enough to fragment, so a
// payload that would need it is a bug here rather than a case to handle.
func (c *conn) writeFrame(msgType byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeFrameLocked(msgType, payload)
}

func (c *conn) writeFrameLocked(msgType byte, payload []byte) error {
	if c.t == nil {
		return errors.New("channel is not up")
	}
	frame := make([]byte, 0, 1+len(payload))
	frame = append(frame, msgType)
	frame = append(frame, payload...)
	ct, err := c.t.encrypt(frame)
	if err != nil {
		return err
	}
	if err := c.ws.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.BinaryMessage, ct)
}

// writeJSON sends one control message.
func (c *conn) writeJSON(msgType string, payload any) error {
	raw, err := encodeEnvelope(msgType, payload)
	if err != nil {
		return err
	}
	return c.writeFrame(msgJSON, raw)
}

// readFrame returns the next whole message. A text frame once the channel is up, a frame that does not
// decrypt, or a broken fragment sequence ends the connection: the caller closes on any error.
func (c *conn) readFrame() (byte, []byte, error) {
	for {
		wsType, data, err := c.ws.ReadMessage()
		if err != nil {
			return 0, nil, err
		}
		if wsType != websocket.BinaryMessage {
			return 0, nil, errors.New("text frame in transport mode")
		}
		pt, err := c.t.decrypt(data)
		if err != nil {
			return 0, nil, err
		}
		msgType, payload, done, err := c.reasm.feed(pt)
		if err != nil {
			return 0, nil, err
		}
		if done {
			return msgType, payload, nil
		}
	}
}

func (c *conn) close() error { return c.ws.Close() }
