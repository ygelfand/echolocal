package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// GATT declarations.
const (
	uuidPrimaryService = 0x2800
	uuidCharacteristic = 0x2803
)

// UUID is a 128-bit UUID in the order it is written, most significant byte first.
type UUID [16]byte

// base is the Bluetooth base UUID that 16- and 32-bit UUIDs sit in.
var base = UUID{0, 0, 0, 0, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5f, 0x9b, 0x34, 0xfb}

// Short places a 16- or 32-bit UUID in the base UUID.
func Short(v uint32) UUID {
	u := base
	binary.BigEndian.PutUint32(u[:4], v)
	return u
}

// Halves is the UUID as two 64-bit words, high first, as ESPHome carries it.
func (u UUID) Halves() (high, low uint64) {
	return binary.BigEndian.Uint64(u[:8]), binary.BigEndian.Uint64(u[8:])
}

func (u UUID) String() string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// uuidFrom reads a UUID as ATT carries it: two or sixteen bytes, least significant first.
func uuidFrom(b []byte) (UUID, bool) {
	switch len(b) {
	case 2:
		return Short(uint32(binary.LittleEndian.Uint16(b))), true
	case 16:
		var u UUID
		for i := range 16 {
			u[i] = b[15-i]
		}
		return u, true
	}
	return UUID{}, false
}

// Service is a primary service and what it contains.
type Service struct {
	UUID            UUID
	Handle          uint16
	End             uint16
	Characteristics []Characteristic
}

// Characteristic is one characteristic. Handle is its value's handle, which is what reads, writes
// and notifications name.
type Characteristic struct {
	UUID        UUID
	Declaration uint16
	Handle      uint16
	Properties  uint8
	Descriptors []Descriptor
}

// Descriptor is one characteristic descriptor.
type Descriptor struct {
	UUID   UUID
	Handle uint16
}

// Discover walks the peer's attribute table: services, then each service's characteristics, then
// the descriptors between one characteristic's value and the next declaration.
func (c *Conn) Discover(ctx context.Context) ([]Service, error) {
	services, err := c.services(ctx)
	if err != nil {
		return nil, fmt.Errorf("ble: discovering services: %w", err)
	}
	for i := range services {
		s := &services[i]
		if s.Characteristics, err = c.characteristics(ctx, s.Handle, s.End); err != nil {
			return nil, fmt.Errorf("ble: discovering characteristics of %s: %w", s.UUID, err)
		}
		for j := range s.Characteristics {
			ch := &s.Characteristics[j]
			end := s.End
			if j+1 < len(s.Characteristics) {
				end = s.Characteristics[j+1].Declaration - 1
			}
			if ch.Handle >= end {
				continue
			}
			if ch.Descriptors, err = c.descriptors(ctx, ch.Handle+1, end); err != nil {
				return nil, fmt.Errorf("ble: discovering descriptors of %s: %w", ch.UUID, err)
			}
		}
	}
	return services, nil
}

// notFound reports the Error Response that ends every ATT search.
func notFound(err error) bool {
	var ae *ATTError
	return errors.As(err, &ae) && ae.Code == errAttributeNotFound
}

func (c *Conn) services(ctx context.Context) ([]Service, error) {
	var out []Service
	start := uint16(1)
	for {
		req := []byte{attReadByGroupReq, 0, 0, 0xff, 0xff, 0, 0}
		binary.LittleEndian.PutUint16(req[1:], start)
		binary.LittleEndian.PutUint16(req[5:], uuidPrimaryService)
		rsp, err := c.att.request(ctx, req, attReadByGroupRsp)
		if notFound(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if len(rsp) < 1 || rsp[0] < 6 {
			return nil, errors.New("malformed read by group type response")
		}
		size := int(rsp[0])
		var last uint16
		for at := 1; at+size <= len(rsp); at += size {
			entry := rsp[at : at+size]
			u, ok := uuidFrom(entry[4:])
			if !ok {
				return nil, fmt.Errorf("service uuid of %d bytes", len(entry)-4)
			}
			s := Service{UUID: u, Handle: binary.LittleEndian.Uint16(entry), End: binary.LittleEndian.Uint16(entry[2:])}
			out = append(out, s)
			last = s.End
		}
		if last == 0xffff || last < start {
			return out, nil
		}
		start = last + 1
	}
}

func (c *Conn) characteristics(ctx context.Context, start, end uint16) ([]Characteristic, error) {
	var out []Characteristic
	for start <= end {
		req := []byte{attReadByTypeReq, 0, 0, 0, 0, 0, 0}
		binary.LittleEndian.PutUint16(req[1:], start)
		binary.LittleEndian.PutUint16(req[3:], end)
		binary.LittleEndian.PutUint16(req[5:], uuidCharacteristic)
		rsp, err := c.att.request(ctx, req, attReadByTypeRsp)
		if notFound(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if len(rsp) < 1 || rsp[0] < 7 {
			return nil, errors.New("malformed read by type response")
		}
		size := int(rsp[0])
		var last uint16
		for at := 1; at+size <= len(rsp); at += size {
			entry := rsp[at : at+size]
			u, ok := uuidFrom(entry[5:])
			if !ok {
				return nil, fmt.Errorf("characteristic uuid of %d bytes", len(entry)-5)
			}
			ch := Characteristic{
				UUID:        u,
				Declaration: binary.LittleEndian.Uint16(entry),
				Properties:  entry[2],
				Handle:      binary.LittleEndian.Uint16(entry[3:]),
			}
			out = append(out, ch)
			last = ch.Declaration
		}
		if last == 0xffff || last < start {
			return out, nil
		}
		start = last + 1
	}
	return out, nil
}

func (c *Conn) descriptors(ctx context.Context, start, end uint16) ([]Descriptor, error) {
	var out []Descriptor
	for start <= end {
		req := []byte{attFindInfoReq, 0, 0, 0, 0}
		binary.LittleEndian.PutUint16(req[1:], start)
		binary.LittleEndian.PutUint16(req[3:], end)
		rsp, err := c.att.request(ctx, req, attFindInfoRsp)
		if notFound(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if len(rsp) < 1 {
			return nil, errors.New("malformed find information response")
		}
		size := 4
		if rsp[0] == 0x02 {
			size = 18
		}
		var last uint16
		for at := 1; at+size <= len(rsp); at += size {
			entry := rsp[at : at+size]
			u, _ := uuidFrom(entry[2:])
			d := Descriptor{UUID: u, Handle: binary.LittleEndian.Uint16(entry)}
			out = append(out, d)
			last = d.Handle
		}
		if last == 0xffff || last < start {
			return out, nil
		}
		start = last + 1
	}
	return out, nil
}
