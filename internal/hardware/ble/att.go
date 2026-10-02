package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ATT opcodes.
const (
	attErrorRsp          = 0x01
	attMTUReq            = 0x02
	attMTURsp            = 0x03
	attFindInfoReq       = 0x04
	attFindInfoRsp       = 0x05
	attReadByTypeReq     = 0x08
	attReadByTypeRsp     = 0x09
	attReadReq           = 0x0A
	attReadRsp           = 0x0B
	attReadBlobReq       = 0x0C
	attReadBlobRsp       = 0x0D
	attReadByGroupReq    = 0x10
	attReadByGroupRsp    = 0x11
	attWriteReq          = 0x12
	attWriteRsp          = 0x13
	attPrepareWriteReq   = 0x16
	attPrepareWriteRsp   = 0x17
	attExecuteWriteReq   = 0x18
	attExecuteWriteRsp   = 0x19
	attNotification      = 0x1B
	attIndication        = 0x1D
	attConfirmation      = 0x1E
	attWriteCmd          = 0x52
	attCommandFlag       = 0x40
	attDefaultMTU        = 23
	attTimeout           = 30 * time.Second
	errAttributeNotFound = 0x0A
	errAttributeNotLong  = 0x0B
	errRequestNotSupport = 0x06
	errInvalidHandle     = 0x01
)

// MTU is the ATT MTU this side asks for. The controller's own buffers are far smaller; L2CAP carries
// the difference in fragments.
const MTU = 247

// ATTError is an Error Response from the peer: the request it refused, the handle, and why.
type ATTError struct {
	Request uint8
	Handle  uint16
	Code    uint8
}

func (e *ATTError) Error() string {
	return fmt.Sprintf("ble: att request 0x%02x on handle 0x%04x failed: 0x%02x", e.Request, e.Handle, e.Code)
}

// att is the GATT client side of one link: one request in flight at a time, as ATT requires.
type att struct {
	c *Conn

	req  sync.Mutex
	mu   sync.Mutex
	mtu  int
	wait chan []byte

	// Notify receives notifications and indications. It runs on the reader and must not block.
	notify func(handle uint16, data []byte)
}

func newATT(c *Conn) *att { return &att{c: c, mtu: attDefaultMTU} }

// OnNotify sets where notifications and indications go.
func (c *Conn) OnNotify(f func(handle uint16, data []byte)) {
	c.att.mu.Lock()
	c.att.notify = f
	c.att.mu.Unlock()
}

// MTU is the ATT MTU agreed on this link.
func (c *Conn) MTU() int {
	c.att.mu.Lock()
	defer c.att.mu.Unlock()
	return c.att.mtu
}

func (a *att) closed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wait != nil {
		close(a.wait)
		a.wait = nil
	}
}

// request sends one ATT request and waits for its response or Error Response.
func (a *att) request(ctx context.Context, pdu []byte, want uint8) ([]byte, error) {
	a.req.Lock()
	defer a.req.Unlock()

	wait := make(chan []byte, 1)
	a.mu.Lock()
	a.wait = wait
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.wait == wait {
			a.wait = nil
		}
		a.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, attTimeout)
	defer cancel()
	if err := a.c.send(ctx, cidATT, pdu); err != nil {
		return nil, err
	}

	select {
	case rsp, ok := <-wait:
		if !ok {
			return nil, ErrClosed
		}
		if rsp[0] == attErrorRsp {
			if len(rsp) < 5 {
				return nil, errors.New("ble: short att error response")
			}
			return nil, &ATTError{Request: rsp[1], Handle: binary.LittleEndian.Uint16(rsp[2:]), Code: rsp[4]}
		}
		if rsp[0] != want {
			return nil, fmt.Errorf("ble: att response 0x%02x to request 0x%02x", rsp[0], pdu[0])
		}
		return rsp[1:], nil
	case <-ctx.Done():
		// A transaction that times out leaves the bearer unusable until the link is re-established.
		go func() { _ = a.c.Disconnect() }()
		return nil, fmt.Errorf("ble: att request 0x%02x: %w", pdu[0], ctx.Err())
	case <-a.c.Done():
		return nil, ErrClosed
	}
}

// receive takes one ATT PDU off the link, on the reader.
func (a *att) receive(p []byte) {
	if len(p) == 0 {
		return
	}
	op := p[0]
	switch {
	case op == attNotification || op == attIndication:
		if len(p) < 3 {
			return
		}
		if op == attIndication {
			a.c.reply(cidATT, []byte{attConfirmation})
		}
		a.mu.Lock()
		notify := a.notify
		a.mu.Unlock()
		if notify != nil {
			notify(binary.LittleEndian.Uint16(p[1:]), append([]byte(nil), p[3:]...))
		}

	case op == attMTUReq:
		// The peer asks as server too. Both directions share one MTU: the smaller of the two.
		if len(p) >= 3 {
			a.setMTU(int(binary.LittleEndian.Uint16(p[1:])))
			rsp := []byte{attMTURsp, 0, 0}
			binary.LittleEndian.PutUint16(rsp[1:], MTU)
			a.c.reply(cidATT, rsp)
		}

	case op == attErrorRsp || op&1 == 1 || op == attConfirmation:
		a.mu.Lock()
		if a.wait != nil {
			a.wait <- append([]byte(nil), p...)
			a.wait = nil
		}
		a.mu.Unlock()

	case op&attCommandFlag != 0:
		// Commands to this side's server, which has nothing in it. Commands get no answer.

	default:
		// A request to this side's GATT server, which is empty.
		code := uint8(errRequestNotSupport)
		switch op {
		case attFindInfoReq, attReadByTypeReq, attReadByGroupReq, 0x06:
			code = errAttributeNotFound
		case attReadReq, attReadBlobReq, attWriteReq, attPrepareWriteReq:
			code = errInvalidHandle
		}
		var handle uint16
		if len(p) >= 3 {
			handle = binary.LittleEndian.Uint16(p[1:])
		}
		rsp := []byte{attErrorRsp, op, 0, 0, code}
		binary.LittleEndian.PutUint16(rsp[2:], handle)
		a.c.reply(cidATT, rsp)
	}
}

func (a *att) setMTU(peer int) {
	a.mu.Lock()
	a.mtu = max(attDefaultMTU, min(MTU, peer))
	a.mu.Unlock()
}

// ExchangeMTU agrees the ATT MTU with the peer, and reports it.
func (c *Conn) ExchangeMTU(ctx context.Context) (int, error) {
	req := []byte{attMTUReq, 0, 0}
	binary.LittleEndian.PutUint16(req[1:], MTU)
	rsp, err := c.att.request(ctx, req, attMTURsp)
	if err != nil {
		var ae *ATTError
		if errors.As(err, &ae) && ae.Code == errRequestNotSupport {
			return c.MTU(), nil // the peer only does the default
		}
		return 0, err
	}
	if len(rsp) < 2 {
		return 0, errors.New("ble: short mtu response")
	}
	c.att.setMTU(int(binary.LittleEndian.Uint16(rsp)))
	return c.MTU(), nil
}

// Read reads an attribute's whole value: a characteristic value or a descriptor.
func (c *Conn) Read(ctx context.Context, handle uint16) ([]byte, error) {
	req := []byte{attReadReq, 0, 0}
	binary.LittleEndian.PutUint16(req[1:], handle)
	value, err := c.att.request(ctx, req, attReadRsp)
	if err != nil {
		return nil, err
	}
	value = append([]byte(nil), value...)

	// A response that fills the MTU may be the start of a longer value.
	for len(value) > 0 && len(value)%(c.MTU()-1) == 0 {
		blob := []byte{attReadBlobReq, 0, 0, 0, 0}
		binary.LittleEndian.PutUint16(blob[1:], handle)
		binary.LittleEndian.PutUint16(blob[3:], uint16(len(value)))
		more, err := c.att.request(ctx, blob, attReadBlobRsp)
		var ae *ATTError
		if errors.As(err, &ae) && (ae.Code == errAttributeNotLong || ae.Code == 0x07) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(more) == 0 {
			break
		}
		value = append(value, more...)
		if len(more) < c.MTU()-1 {
			break
		}
	}
	return value, nil
}

// Write writes an attribute. With response, a value longer than one PDU is written as a queued long
// write; without, it has to fit.
func (c *Conn) Write(ctx context.Context, handle uint16, data []byte, response bool) error {
	room := c.MTU() - 3
	if !response {
		if len(data) > room {
			return fmt.Errorf("ble: %d bytes do not fit a write without response (%d)", len(data), room)
		}
		pdu := []byte{attWriteCmd, 0, 0}
		binary.LittleEndian.PutUint16(pdu[1:], handle)
		return c.send(ctx, cidATT, append(pdu, data...))
	}

	if len(data) <= room {
		pdu := []byte{attWriteReq, 0, 0}
		binary.LittleEndian.PutUint16(pdu[1:], handle)
		_, err := c.att.request(ctx, append(pdu, data...), attWriteRsp)
		return err
	}

	part := c.MTU() - 5
	for offset := 0; offset < len(data); offset += part {
		end := min(offset+part, len(data))
		pdu := []byte{attPrepareWriteReq, 0, 0, 0, 0}
		binary.LittleEndian.PutUint16(pdu[1:], handle)
		binary.LittleEndian.PutUint16(pdu[3:], uint16(offset))
		if _, err := c.att.request(ctx, append(pdu, data[offset:end]...), attPrepareWriteRsp); err != nil {
			_, _ = c.att.request(ctx, []byte{attExecuteWriteReq, 0x00}, attExecuteWriteRsp)
			return err
		}
	}
	_, err := c.att.request(ctx, []byte{attExecuteWriteReq, 0x01}, attExecuteWriteRsp)
	return err
}
