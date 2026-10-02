package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ygelfand/echolocal/internal/hardware/ble"
)

func TestMacBytes(t *testing.T) {
	got := macBytes(0x685edd16f8e2)
	want := [6]byte{0x68, 0x5e, 0xdd, 0x16, 0xf8, 0xe2}
	if got != want {
		t.Errorf("macBytes = %x, want %x", got, want)
	}
}

func TestServiceMessage(t *testing.T) {
	s := ble.Service{
		UUID:   ble.Short(0x180a),
		Handle: 10,
		Characteristics: []ble.Characteristic{{
			UUID: ble.Short(0x2a29), Handle: 12, Properties: 0x12,
			Descriptors: []ble.Descriptor{{UUID: ble.Short(0x2902), Handle: 13}},
		}},
	}
	m := service(s)
	if m.GetHandle() != 10 || len(m.GetUuid()) != 2 || m.GetUuid()[0] != 0x0000180a00001000 || m.GetUuid()[1] != 0x800000805f9b34fb {
		t.Fatalf("service = %v", m)
	}
	c := m.GetCharacteristics()[0]
	if c.GetHandle() != 12 || c.GetProperties() != 0x12 || c.GetDescriptors()[0].GetHandle() != 13 {
		t.Errorf("characteristic = %v", c)
	}
}

func TestGATTError(t *testing.T) {
	if got := gattError(&ble.ATTError{Code: 0x02}); got != 0x02 {
		t.Errorf("att error = %#x, want the device's own code", got)
	}
	if got := gattError(ble.ErrClosed); got != errNoConnection {
		t.Errorf("closed = %#x", got)
	}
	if got := gattError(errors.New("timeout")); got != errGATT {
		t.Errorf("other = %#x", got)
	}
}

func TestBondRejected(t *testing.T) {
	if !bondRejected(fmt.Errorf("encrypting: %w", ble.StatusError(statusKeyMissing))) {
		t.Error("key missing should pair again")
	}
	for _, err := range []error{context.DeadlineExceeded, ble.ErrClosed, ble.StatusError(0x08), errors.New("hci")} {
		if bondRejected(err) {
			t.Errorf("%v should keep the bond", err)
		}
	}
}

func TestFreeSlots(t *testing.T) {
	g := newGATT(nil)
	g.slots[1] = &slot{}
	r := g.freeResponse()
	if r.GetFree() != connectionSlots-1 || r.GetLimit() != connectionSlots || len(r.GetAllocated()) != 1 {
		t.Errorf("free = %v", r)
	}
}

func TestBonds(t *testing.T) {
	b := &bonds{path: t.TempDir() + "/bonds.json"}
	if _, ok := b.get(1); ok {
		t.Fatal("bond before pairing")
	}
	k := ble.Keys{LTK: [16]byte{1, 2, 3}, EDIV: 7, Size: 16, Secure: true}
	if err := b.put(0x847293478240, k); err != nil {
		t.Fatal(err)
	}
	got, ok := b.get(0x847293478240)
	if !ok || got != k {
		t.Fatalf("bond = %+v %v", got, ok)
	}
	if err := b.remove(0x847293478240); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.get(0x847293478240); ok {
		t.Error("bond after removing it")
	}
}

func TestWorkKeepsOrder(t *testing.T) {
	ops := make(chan func(), opsQueued)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { work(ops, done); close(finished) }()

	var got []int
	for i := range 100 {
		ops <- func() { got = append(got, i) }
	}
	close(done)
	<-finished
	for i, v := range got {
		if v != i {
			t.Fatalf("operation %d ran as %d", v, i)
		}
	}
	if len(got) != 100 {
		t.Errorf("ran %d of 100", len(got))
	}
}
