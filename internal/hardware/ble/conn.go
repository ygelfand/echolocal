package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// L2CAP fixed channels on an LE link.
const (
	cidATT       = 0x0004
	cidSignaling = 0x0005
	cidSMP       = 0x0006
)

// Connection parameters, the controller's units: 1.25 ms for intervals, 10 ms for the supervision
// timeout, 0.625 ms for the initiator's scan.
const (
	initScanInterval   = 0x0060
	initScanWindow     = 0x0030
	connIntervalMin    = 24 // 30 ms
	connIntervalMax    = 40 // 50 ms
	supervisionTimeout = 400
)

const (
	// statusUnknownConnection is a cancelled connection attempt.
	statusUnknownConnection = 0x02
	// statusConnectionFailed is a connection that never came up, which is what a timeout is reported as.
	statusConnectionFailed = 0x3E
	// reasonRemoteUser is a disconnection the local user asked for.
	reasonRemoteUser = 0x13
	// reasonLocalHost is how the controller reports a disconnection this side asked for.
	reasonLocalHost = 0x16
)

// ErrClosed is an operation on a link that has gone.
var ErrClosed = errors.New("ble: connection closed")

// links tracks the connections the controller holds.
type links struct {
	mu       sync.Mutex
	byHandle map[uint16]*Conn
	dialing  *dial

	// dial is one connection attempt at a time: the controller initiates only one.
	dial sync.Mutex
}

type dial struct {
	done chan []byte
}

func (l *links) init() { l.byHandle = map[uint16]*Conn{} }

// Conn is one LE link on which this side is central and GATT client.
type Conn struct {
	r           *Radio
	Handle      uint16
	Address     [6]byte
	AddressType uint8

	ctx    context.Context
	cancel context.CancelFunc
	reason uint8
	closed sync.Once

	// smu keeps the fragments of one L2CAP frame together on the link.
	smu sync.Mutex
	rx  []byte

	att *att
	sec security
}

// Addr is the peer's address as a big-endian integer.
func (c *Conn) Addr() uint64 { return addrUint(c.Address) }

// Done is closed when the link goes.
func (c *Conn) Done() <-chan struct{} { return c.ctx.Done() }

// Reason is the HCI reason the link went, once Done is closed.
func (c *Conn) Reason() uint8 { return c.reason }

// Connect opens a link to a device and waits until it is up, or ctx ends. addrType is 0 for a
// public address and 1 for a random one.
func (r *Radio) Connect(ctx context.Context, address [6]byte, addrType uint8) (*Conn, error) {
	r.link.dial.Lock()
	defer r.link.dial.Unlock()

	d := &dial{done: make(chan []byte, 1)}
	r.link.mu.Lock()
	r.link.dialing = d
	r.link.mu.Unlock()
	defer func() {
		r.link.mu.Lock()
		if r.link.dialing == d {
			r.link.dialing = nil
		}
		r.link.mu.Unlock()
	}()

	params := make([]byte, 25)
	binary.LittleEndian.PutUint16(params[0:], initScanInterval)
	binary.LittleEndian.PutUint16(params[2:], initScanWindow)
	params[5] = addrType
	for i := range 6 {
		params[6+i] = address[5-i]
	}
	binary.LittleEndian.PutUint16(params[13:], connIntervalMin)
	binary.LittleEndian.PutUint16(params[15:], connIntervalMax)
	binary.LittleEndian.PutUint16(params[19:], supervisionTimeout)

	// This controller accepts a connection while it scans, but the link then fails to establish
	// (0x3E): the scan takes the radio in the windows the first connection events need. Scanning
	// pauses while the link comes up, and resumes once packets have crossed it both ways, which is
	// what established means. The MTU exchange is that crossing.
	if r.Scanning() {
		if _, err := r.Command(cmdLEScanEnable, []byte{0x00, 0x00}); err != nil {
			return nil, err
		}
		defer func() {
			if _, err := r.Command(cmdLEScanEnable, []byte{0x01, 0x00}); err != nil {
				slog.Warn("ble scan did not resume after connecting", "err", err)
			}
		}()
	}
	_, err := r.Command(cmdLECreateConnection, params)
	if err != nil {
		return nil, fmt.Errorf("ble: connecting: %w", err)
	}

	var event []byte
	select {
	case event = <-d.done:
	case <-ctx.Done():
		// Cancelling races the link coming up: whichever the controller did, it reports.
		if _, err := r.Command(cmdLECreateCancel, nil); err != nil {
			slog.Debug("ble connection cancel failed", "err", err)
		}
		select {
		case event = <-d.done:
		case <-time.After(2 * time.Second):
			return nil, StatusError(statusConnectionFailed)
		}
	}

	if status := event[1]; status != 0 {
		if status == statusUnknownConnection {
			return nil, StatusError(statusConnectionFailed)
		}
		return nil, StatusError(status)
	}

	c := r.link.add(r, event, addrType)
	if _, err := c.ExchangeMTU(ctx); err != nil {
		_ = c.Disconnect()
		if c.ctx.Err() != nil && c.Reason() != 0 {
			return nil, StatusError(c.Reason())
		}
		return nil, fmt.Errorf("ble: exchanging mtu: %w", err)
	}
	slog.Info("ble connected", "address", fmt.Sprintf("%012X", c.Addr()), "handle", c.Handle, "mtu", c.MTU())
	return c, nil
}

// connected hands an LE Connection Complete to the attempt waiting for it. p starts at the subevent:
// status, handle, role, peer address type, peer address.
func (l *links) connected(p []byte) {
	if len(p) < 12 {
		return
	}
	l.mu.Lock()
	d := l.dialing
	l.dialing = nil
	l.mu.Unlock()

	if d != nil {
		d.done <- append([]byte(nil), p...)
		return
	}
	// Nobody asked for this link any more: the attempt timed out as the peer answered.
	if p[1] == 0 {
		handle := append([]byte(nil), p[2:4]...)
		go func() { _, _ = Get().Command(cmdDisconnect, append(handle, reasonRemoteUser)) }()
	}
}

func (l *links) add(r *Radio, p []byte, addrType uint8) *Conn {
	c := &Conn{r: r, Handle: binary.LittleEndian.Uint16(p[2:]) & 0x0fff, AddressType: addrType}
	for i := range 6 {
		c.Address[i] = p[11-i]
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.att = newATT(c)
	c.sec.encrypted = make(chan uint8, 1)

	l.mu.Lock()
	l.byHandle[c.Handle] = c
	l.mu.Unlock()
	return c
}

func (l *links) get(handle uint16) *Conn {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byHandle[handle]
}

// disconnected ends a link the controller reports gone.
func (l *links) disconnected(handle uint16, reason uint8) {
	l.mu.Lock()
	c := l.byHandle[handle]
	delete(l.byHandle, handle)
	l.mu.Unlock()
	if c != nil {
		c.close(reason)
	}
}

// closeAll disconnects every link before the controller closes.
func (l *links) closeAll(r *Radio) {
	l.mu.Lock()
	conns := make([]*Conn, 0, len(l.byHandle))
	for _, c := range l.byHandle {
		conns = append(conns, c)
	}
	l.mu.Unlock()

	for _, c := range conns {
		_ = c.Disconnect()
	}
	// Whatever did not confirm is gone with the controller anyway.
	for _, c := range conns {
		l.disconnected(c.Handle, reasonLocalHost)
	}
}

func (c *Conn) close(reason uint8) {
	c.closed.Do(func() {
		c.reason = reason
		c.cancel()
		c.att.closed()
		slog.Info("ble disconnected", "address", fmt.Sprintf("%012X", c.Addr()), "reason", fmt.Sprintf("0x%02x", reason))
	})
}

// Disconnect ends the link and waits for the controller to confirm it.
func (c *Conn) Disconnect() error {
	select {
	case <-c.Done():
		return nil
	default:
	}
	params := make([]byte, 3)
	binary.LittleEndian.PutUint16(params, c.Handle)
	params[2] = reasonRemoteUser
	if _, err := c.r.Command(cmdDisconnect, params); err != nil {
		return err
	}
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
	}
	return nil
}

// send writes one L2CAP frame, giving up if the link goes first.
func (c *Conn) send(ctx context.Context, cid uint16, payload []byte) error {
	if c.ctx.Err() != nil {
		return ErrClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()

	c.smu.Lock()
	defer c.smu.Unlock()
	if err := c.r.sendL2CAP(ctx, c.Handle, cid, payload); err != nil {
		if c.ctx.Err() != nil {
			return ErrClosed
		}
		return err
	}
	return nil
}

// reply sends from the reader's goroutine, which cannot wait for buffers it is the one to free.
func (c *Conn) reply(cid uint16, payload []byte) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.send(ctx, cid, payload); err != nil && !errors.Is(err, ErrClosed) {
			slog.Debug("ble reply not sent", "cid", cid, "err", err)
		}
	}()
}

// data reassembles L2CAP frames from ACL packets, on the reader.
func (l *links) data(r *Radio, pkt []byte) {
	if len(pkt) < 5 {
		return
	}
	field := binary.LittleEndian.Uint16(pkt[1:])
	c := l.get(field & 0x0fff)
	if c == nil {
		return
	}
	payload := pkt[5:]

	if field&0x3000 != pbContinuation {
		c.rx = append(c.rx[:0], payload...)
	} else {
		if len(c.rx) == 0 {
			return // a continuation of nothing
		}
		c.rx = append(c.rx, payload...)
	}

	for len(c.rx) >= 4 {
		size := int(binary.LittleEndian.Uint16(c.rx))
		if len(c.rx) < 4+size {
			return
		}
		cid := binary.LittleEndian.Uint16(c.rx[2:])
		frame := append([]byte(nil), c.rx[4:4+size]...)
		c.rx = c.rx[4+size:]
		c.frame(cid, frame)
	}
}

func (c *Conn) frame(cid uint16, p []byte) {
	switch cid {
	case cidATT:
		c.att.receive(p)
	case cidSignaling:
		c.signal(p)
	case cidSMP:
		c.smp(p)
	}
}

// signal answers the LE signaling channel: code, identifier, length, data.
func (c *Conn) signal(p []byte) {
	if len(p) < 4 {
		return
	}
	code, id := p[0], p[1]
	switch code {
	case 0x12: // Connection Parameter Update Request
		if len(p) < 12 {
			return
		}
		c.reply(cidSignaling, []byte{0x13, id, 0x02, 0x00, 0x00, 0x00})
		update := make([]byte, 14)
		binary.LittleEndian.PutUint16(update, c.Handle)
		copy(update[2:10], p[4:12])
		go func() { _, _ = c.r.Command(cmdLEConnectionUpdate, update) }()
	case 0x01, 0x13, 0x15, 0x16, 0x18:
		// Responses and credits, to requests this side never makes.
	default:
		// Command Reject: not understood.
		c.reply(cidSignaling, []byte{0x01, id, 0x02, 0x00, 0x00, 0x00})
	}
}
