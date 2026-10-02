package ble

import (
	"bytes"
	"context"
	"encoding/binary"
	"slices"
	"testing"
	"time"
)

// peripheral is a controller with one device behind it: it answers the commands a connection
// needs, and serves a small attribute table over ATT, cut into ACL packets of the controller's size.
type peripheral struct {
	t       *testing.T
	r       *Radio
	in      chan []byte
	rx      []byte
	mtu     int
	written map[uint16][]byte
	queued  []byte
	attrs   []attr
	notify  chan struct{}

	// smp and encrypt, when set, play the device's side of pairing.
	smp     func(pdu []byte)
	encrypt func(params []byte) byte
	// afterEncrypt is what the device says once the link is encrypted, on the controller's goroutine
	// as everything it says has to be.
	afterEncrypt func()
}

type attr struct {
	handle uint16
	typ    []byte // as ATT carries it
	value  []byte
}

const acceptedSize = 27

var custom = UUID{0x12, 0x34, 0x56, 0x78, 0x12, 0x34, 0x56, 0x78, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0, 0}

func le(u UUID) []byte {
	b := make([]byte, 16)
	for i := range 16 {
		b[i] = u[15-i]
	}
	return b
}

func u16(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }

func newPeripheral(t *testing.T) *peripheral {
	charUUID := custom
	charUUID[15] = 1
	long := bytes.Repeat([]byte("0123456789"), 13) // 130 bytes: several blobs at an MTU of 64

	p := &peripheral{
		t: t, in: make(chan []byte, 256), mtu: 64, written: map[uint16][]byte{}, notify: make(chan struct{}, 1),
		attrs: []attr{
			{1, u16(0x2800), u16(0x1800)},
			{2, u16(0x2803), append([]byte{0x02}, append(u16(3), u16(0x2a00)...)...)},
			{3, u16(0x2a00), []byte("Fake")},
			{4, u16(0x2800), le(custom)},
			{5, u16(0x2803), append([]byte{0x12}, append(u16(6), le(charUUID)...)...)},
			{6, le(charUUID), long},
			{7, u16(0x2902), []byte{0, 0}},
			{8, u16(0x2803), append([]byte{0x08}, append(u16(9), u16(0x2a01)...)...)},
			{9, u16(0x2a01), nil},
		},
	}
	r := &Radio{fd: -1, open: true, sink: func(b []byte) error { p.in <- append([]byte(nil), b...); return nil }}
	r.acl.init()
	r.link.init()
	r.acl.reset(acceptedSize, 3)
	p.r = r
	go p.run()
	return p
}

// run is the controller: everything it says reaches the host through dispatch, as the reader would.
func (p *peripheral) run() {
	for pkt := range p.in {
		switch pkt[0] {
		case h4Command:
			p.command(binary.LittleEndian.Uint16(pkt[1:]), pkt[4:])
		case h4ACL:
			field := binary.LittleEndian.Uint16(pkt[1:])
			if len(pkt)-5 > acceptedSize {
				p.t.Errorf("acl packet of %d bytes, controller takes %d", len(pkt)-5, acceptedSize)
			}
			// Hand the buffer back, as the controller does once the packet is on air.
			done := []byte{h4Event, evtNumCompletedPackets, 5, 1}
			done = append(done, u16(field&0x0fff)...)
			done = append(done, u16(1)...)
			p.r.dispatch(done)

			if field&0x3000 == pbContinuation {
				p.rx = append(p.rx, pkt[5:]...)
			} else {
				p.rx = append(p.rx[:0], pkt[5:]...)
			}
			if len(p.rx) >= 4 && len(p.rx) >= 4+int(binary.LittleEndian.Uint16(p.rx)) {
				switch cid := binary.LittleEndian.Uint16(p.rx[2:]); {
				case cid == cidATT:
					p.att(p.rx[4:])
				case cid == cidSMP && p.smp != nil:
					p.smp(p.rx[4:])
				}
				p.rx = nil
			}
		}
	}
}

func (p *peripheral) event(code byte, params ...byte) {
	p.r.dispatch(append([]byte{h4Event, code, byte(len(params))}, params...))
}

func (p *peripheral) command(opcode uint16, params []byte) {
	switch opcode {
	case cmdLECreateConnection:
		p.event(evtCommandStatus, 0, 1, byte(opcode), byte(opcode>>8))
		// Connection Complete: status, handle 0x0040, central, then the address as asked.
		complete := []byte{leConnectionComplete, 0, 0x40, 0x00, 0x00, params[5]}
		complete = append(complete, params[6:12]...)
		complete = append(complete, 0x28, 0, 0, 0, 0x90, 0x01, 0)
		p.event(evtLEMeta, complete...)
	case cmdLEEnableEncryption:
		p.event(evtCommandStatus, 0, 1, byte(opcode), byte(opcode>>8))
		status := byte(0x06) // PIN or key missing
		if p.encrypt != nil {
			status = p.encrypt(params)
		}
		p.event(evtEncryptionChange, status, params[0], params[1], 1)
		if f := p.afterEncrypt; f != nil {
			p.afterEncrypt = nil
			f()
		}
	case cmdDisconnect:
		p.event(evtCommandStatus, 0, 1, byte(opcode), byte(opcode>>8))
		p.event(evtDisconnectionComplete, 0, params[0], params[1], 0x16)
	default:
		p.event(evtCommandComplete, 1, byte(opcode), byte(opcode>>8), 0)
	}
}

// sendOn is the peripheral talking on any fixed channel.
func (p *peripheral) sendOn(cid uint16, pdu []byte) {
	frame := append(u16(uint16(len(pdu))), u16(cid)...)
	frame = append(frame, pdu...)
	pkt := append([]byte{h4ACL}, u16(0x0040|pbFirstFlushable)...)
	pkt = append(pkt, u16(uint16(len(frame)))...)
	p.r.dispatch(append(pkt, frame...))
}

// send is the peripheral talking: one L2CAP frame, cut into ACL packets marked as the controller does.
func (p *peripheral) send(pdu []byte) {
	frame := append(u16(uint16(len(pdu))), u16(cidATT)...)
	frame = append(frame, pdu...)
	flags := uint16(pbFirstFlushable)
	for len(frame) > 0 {
		n := min(acceptedSize, len(frame))
		pkt := append([]byte{h4ACL}, u16(0x0040|flags)...)
		pkt = append(pkt, u16(uint16(n))...)
		p.r.dispatch(append(pkt, frame[:n]...))
		frame = frame[n:]
		flags = pbContinuation
	}
}

func (p *peripheral) fail(op byte, handle uint16, code byte) {
	p.send(append([]byte{attErrorRsp, op}, append(u16(handle), code)...))
}

func (p *peripheral) find(handle uint16) *attr {
	for i := range p.attrs {
		if p.attrs[i].handle == handle {
			return &p.attrs[i]
		}
	}
	return nil
}

func (p *peripheral) att(pdu []byte) {
	op := pdu[0]
	switch op {
	case attMTUReq:
		p.send(append([]byte{attMTURsp}, u16(uint16(p.mtu))...))
	case attReadByGroupReq, attReadByTypeReq:
		start, end := binary.LittleEndian.Uint16(pdu[1:]), binary.LittleEndian.Uint16(pdu[3:])
		typ := pdu[5:]
		var out []byte
		size := 0
		for i, a := range p.attrs {
			if a.handle < start || a.handle > end || !bytes.Equal(a.typ, typ) {
				continue
			}
			entry := u16(a.handle)
			if op == attReadByGroupReq {
				groupEnd := uint16(0xffff)
				for _, b := range p.attrs[i+1:] {
					if bytes.Equal(b.typ, typ) {
						groupEnd = b.handle - 1
						break
					}
				}
				entry = append(entry, u16(groupEnd)...)
			}
			entry = append(entry, a.value...)
			if size == 0 {
				size = len(entry)
			}
			// One response carries entries of one size, as many as fit.
			if len(entry) != size || 2+len(out)+len(entry) > p.mtu {
				break
			}
			out = append(out, entry...)
		}
		if len(out) == 0 {
			p.fail(op, start, errAttributeNotFound)
			return
		}
		p.send(append([]byte{op + 1, byte(size)}, out...))
	case attFindInfoReq:
		start, end := binary.LittleEndian.Uint16(pdu[1:]), binary.LittleEndian.Uint16(pdu[3:])
		var out []byte
		for _, a := range p.attrs {
			if a.handle >= start && a.handle <= end && len(a.typ) == 2 {
				out = append(out, append(u16(a.handle), a.typ...)...)
			}
		}
		if len(out) == 0 {
			p.fail(op, start, errAttributeNotFound)
			return
		}
		p.send(append([]byte{attFindInfoRsp, 0x01}, out...))
	case attReadReq, attReadBlobReq:
		a := p.find(binary.LittleEndian.Uint16(pdu[1:]))
		offset := 0
		if op == attReadBlobReq {
			offset = int(binary.LittleEndian.Uint16(pdu[3:]))
		}
		value := a.value[offset:]
		p.send(append([]byte{op + 1}, value[:min(len(value), p.mtu-1)]...))
	case attWriteReq:
		handle := binary.LittleEndian.Uint16(pdu[1:])
		p.written[handle] = append([]byte(nil), pdu[3:]...)
		p.send([]byte{attWriteRsp})
		if handle == 7 {
			p.notify <- struct{}{}
		}
	case attPrepareWriteReq:
		p.queued = append(p.queued, pdu[5:]...)
		p.send(append([]byte{attPrepareWriteRsp}, pdu[1:]...))
	case attExecuteWriteReq:
		if pdu[1] == 0x01 {
			p.written[9] = p.queued
		}
		p.queued = nil
		p.send([]byte{attExecuteWriteRsp})
	}
}

func TestGATTClient(t *testing.T) {
	p := newPeripheral(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	address := [6]byte{0x68, 0x5e, 0xdd, 0x16, 0xf8, 0xe2}
	c, err := p.r.Connect(ctx, address, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != address || c.Handle != 0x40 {
		t.Fatalf("connected to %012x handle %d", c.Addr(), c.Handle)
	}

	mtu, err := c.ExchangeMTU(ctx)
	if err != nil || mtu != 64 {
		t.Fatalf("mtu = %d, %v; want 64", mtu, err)
	}

	services, err := c.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("services = %+v", services)
	}
	if services[0].UUID != Short(0x1800) || services[0].End != 3 || services[1].UUID != custom || services[1].End != 0xffff {
		t.Errorf("services = %+v", services)
	}
	chars := services[1].Characteristics
	if len(chars) != 2 || chars[0].Handle != 6 || chars[0].Properties != 0x12 || chars[1].Handle != 9 {
		t.Fatalf("characteristics = %+v", chars)
	}
	if d := chars[0].Descriptors; len(d) != 1 || d[0].Handle != 7 || d[0].UUID != Short(0x2902) {
		t.Errorf("descriptors = %+v", d)
	}
	if high, low := Short(0x2902).Halves(); high != 0x0000290200001000 || low != 0x800000805f9b34fb {
		t.Errorf("halves = %016x %016x", high, low)
	}

	value, err := c.Read(ctx, 6)
	if err != nil || !bytes.Equal(value, p.find(6).value) {
		t.Fatalf("read = %q, %v", value, err)
	}

	long := bytes.Repeat([]byte{0xab}, 150)
	if err := c.Write(ctx, 9, long, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.written[9], long) {
		t.Errorf("long write = %x", p.written[9])
	}

	got := make(chan []byte, 1)
	c.OnNotify(func(handle uint16, data []byte) {
		if handle == 6 {
			got <- data
		}
	})
	if err := c.Write(ctx, 7, []byte{0x01, 0x00}, true); err != nil {
		t.Fatal(err)
	}
	<-p.notify
	p.send(append([]byte{attNotification}, append(u16(6), "tick"...)...))
	select {
	case data := <-got:
		if string(data) != "tick" {
			t.Errorf("notification = %q", data)
		}
	case <-ctx.Done():
		t.Fatal("no notification")
	}

	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("link still up")
	}
	if _, err := c.Read(ctx, 3); err == nil {
		t.Error("read on a closed link succeeded")
	}
	if !slices.Equal([]int{p.r.acl.free}, []int{3}) {
		t.Errorf("free buffers = %d, want all 3 back", p.r.acl.free)
	}
}
