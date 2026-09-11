package sepolicy

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// builder writes the parts of a policy stream that scan walks, so a test can put a type table behind
// the records that have to be stepped over to reach it.
type builder struct{ b []byte }

func (w *builder) u32(v ...uint32) {
	for _, one := range v {
		w.b = binary.LittleEndian.AppendUint32(w.b, one)
	}
}

func (w *builder) key(s string) { w.b = append(w.b, s...) }

func (w *builder) bitmap(bits ...uint32) {
	nodes := map[uint32]uint64{}
	var high uint32
	for _, bit := range bits {
		nodes[bit/mapSize*mapSize] |= 1 << (bit % mapSize)
		high = max(high, bit/mapSize*mapSize+mapSize)
	}
	w.u32(mapSize, high, uint32(len(nodes)))
	for start := uint32(0); start < high; start += mapSize {
		if node, ok := nodes[start]; ok {
			w.u32(start)
			w.b = binary.LittleEndian.AppendUint64(w.b, node)
		}
	}
}

// constraint is one expression carrying a name set, which is the only shape with operands to skip.
func (w *builder) constraint() {
	w.u32(1, 1) // permissions, one expression
	w.u32(cexprNames, 0, 0)
	w.bitmap(3)
	w.bitmap(3)
	w.bitmap()
	w.u32(0) // flags
}

type namedType struct {
	name  string
	value uint32
}

// policy builds a stream with one common, one class, one role and the given types.
func policy(permissive []uint32, types []namedType, tail string) []byte {
	w := &builder{}
	w.u32(magic, uint32(len(ident)))
	w.key(ident)
	w.u32(30, 1, 8, 7)
	w.bitmap(0, 1)
	w.bitmap(permissive...)

	w.u32(1, 1) // commons
	w.u32(6, 1, 1, 1)
	w.key("socket")
	w.u32(3, 1)
	w.key("map")

	w.u32(1, 1) // classes
	w.u32(4, 6, 1, 1, 1, 1)
	w.key("file")
	w.key("socket")
	w.u32(4, 1)
	w.key("read")
	w.constraint()
	w.u32(1) // one validatetrans
	w.constraint()
	w.u32(0, 0, 0) // default user, role, range
	w.u32(0)       // default type

	w.u32(1, 1) // roles
	w.u32(8, 1, 0)
	w.key("object_r")
	w.bitmap()
	w.bitmap(1, 2)

	w.u32(uint32(len(types)), uint32(len(types)))
	for _, t := range types {
		w.u32(uint32(len(t.name)), t.value, 0, 0)
		w.key(t.name)
	}

	w.key(tail)
	return w.b
}

var types = []namedType{{"init", 3}, {"su", 1}, {"ledcontroller", 2}}

func TestPermissive(t *testing.T) {
	const tail = "everything after the type table"
	in := policy([]uint32{1}, types, tail)

	out, err := Permissive(in, "init")
	if err != nil {
		t.Fatalf("Permissive: %v", err)
	}

	got, err := scan(out, map[string]bool{"init": true, "su": true})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !got.bits[3] {
		t.Errorf("init is not permissive: bits %v", got.bits)
	}
	if !got.bits[1] {
		t.Errorf("su stopped being permissive: bits %v", got.bits)
	}
	if !bytes.HasSuffix(out, []byte(tail)) {
		t.Error("the bytes after the type table did not survive the splice")
	}
}

// A policy that already names the type permissive comes back unchanged, so an install can be run twice.
func TestPermissiveIsIdempotent(t *testing.T) {
	in := policy([]uint32{3}, types, "tail")

	out, err := Permissive(in, "init")
	if err != nil {
		t.Fatalf("Permissive: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Errorf("policy changed: %d bytes in, %d out", len(in), len(out))
	}
}

func TestPermissiveErrors(t *testing.T) {
	good := policy(nil, types, "tail")

	for _, tc := range []struct {
		name   string
		policy []byte
		typ    string
		want   string
	}{
		{"not a policy", []byte("this is not a policy at all"), "init", "not an selinux policy"},
		{"unknown type", good, "nosuchtype", `no type named "nosuchtype"`},
		{"truncated", good[:len(good)/2], "init", "unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Permissive(tc.policy, tc.typ)
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error is %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The type table is the first place a wrong record size shows up, and it has to be caught there rather
// than wherever the stream happens to run out.
func TestPermissiveRejectsImpossibleTypeValue(t *testing.T) {
	_, err := Permissive(policy(nil, []namedType{{"init", 9}, {"su", 1}}, "tail"), "init")
	if err == nil || !strings.Contains(err.Error(), "outside 1..") {
		t.Fatalf("error is %v, want one about the type value", err)
	}
}
