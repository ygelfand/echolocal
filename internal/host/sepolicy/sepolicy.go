// Package sepolicy edits the binary SELinux policy that init loads before it starts anything.
package sepolicy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
)

const (
	magic = 0xf97cff8c
	ident = "SE Linux"

	// An ebitmap node carries this many bits, and the kernel rejects a policy that says otherwise.
	mapSize = 64

	verValidateTrans   = 19
	verPolicyCaps      = 22
	verPermissive      = 23
	verBoundary        = 24
	verObjectDefaults  = 27
	verDefaultType     = 28
	verConstraintNames = 29

	// The one constraint expression that carries operands after its header.
	cexprNames = 5

	// Above this the file is being read as something it is not.
	maxTypes = 1 << 20
)

// Permissive returns the policy with the named types marked permissive, which makes the kernel log
// what it would have denied them and allow it. It adds no rule and changes nothing else.
func Permissive(policy []byte, names ...string) ([]byte, error) {
	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[name] = true
	}

	found, err := scan(policy, want)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		value, ok := found.types[name]
		if !ok {
			return nil, fmt.Errorf("the policy declares no type named %q", name)
		}
		found.bits[value] = true
	}

	out := slices.Concat(policy[:found.at], ebitmap(found.bits), policy[found.end:])

	// The splice is only correct if the rest of the stream still parses from its new position.
	again, err := scan(out, want)
	if err != nil {
		return nil, fmt.Errorf("the patched policy does not read back: %w", err)
	}
	for _, name := range names {
		if value, ok := again.types[name]; !ok || !again.bits[value] {
			return nil, fmt.Errorf("%q is not permissive in the patched policy", name)
		}
	}
	return out, nil
}

// found is where the permissive bitmap lives, what it holds, and the value of each type asked about.
type found struct {
	at, end int
	bits    map[uint32]bool
	types   map[string]uint32
}

// scan reads the policy as far as the type table, which is the fourth of eight symbol tables. Nothing
// before it is needed, but a policy is a serial stream, so it all has to be walked to get there.
func scan(policy []byte, want map[string]bool) (*found, error) {
	c := &cursor{b: policy}

	if c.u32() != magic {
		return nil, errors.New("not an selinux policy")
	}
	if name := c.bytes(c.u32()); string(name) != ident {
		return nil, fmt.Errorf("policy identifier is %q, want %q", name, ident)
	}

	vers := c.u32()
	c.u32() // config
	syms := c.u32()
	c.u32() // ocons
	if c.err != nil {
		return nil, c.err
	}
	if vers < verPermissive {
		return nil, fmt.Errorf("policy version %d has no permissive map", vers)
	}
	if syms < 4 {
		return nil, fmt.Errorf("policy declares %d symbol tables, want at least 4", syms)
	}

	if vers >= verPolicyCaps {
		c.ebitmap()
	}

	out := &found{at: c.at, types: map[string]uint32{}}
	out.bits = c.ebitmap()
	out.end = c.at

	c.u32() // commons
	c.each(c.u32(), c.common)

	c.u32() // classes
	c.each(c.u32(), func() { c.class(vers) })

	c.u32() // roles
	c.each(c.u32(), func() { c.role(vers) })

	types := c.u32()
	if c.err != nil {
		return nil, c.err
	}
	if types == 0 || types > maxTypes {
		return nil, fmt.Errorf("policy declares %d types", types)
	}

	c.each(c.u32(), func() {
		name, value := c.typ(vers)
		// A wrong record stride shows up here long before it runs off the end of the file.
		if value == 0 || value > types {
			c.err = fmt.Errorf("type %q has value %d, outside 1..%d", name, value, types)
			return
		}
		if want[string(name)] {
			out.types[string(name)] = value
		}
	})
	if c.err != nil {
		return nil, c.err
	}
	return out, nil
}

// ebitmap serialises a set of bits the way the kernel reads one back.
func ebitmap(bits map[uint32]bool) []byte {
	nodes := map[uint32]uint64{}
	var high uint32
	for bit, set := range bits {
		if !set {
			continue
		}
		nodes[bit/mapSize*mapSize] |= 1 << (bit % mapSize)
		high = max(high, bit/mapSize*mapSize+mapSize)
	}

	out := binary.LittleEndian.AppendUint32(nil, mapSize)
	out = binary.LittleEndian.AppendUint32(out, high)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(nodes)))
	for _, start := range slices.Sorted(maps.Keys(nodes)) {
		out = binary.LittleEndian.AppendUint32(out, start)
		out = binary.LittleEndian.AppendUint64(out, nodes[start])
	}
	return out
}

// cursor walks the stream, holding the first error it hit so each read need not be checked.
type cursor struct {
	b   []byte
	at  int
	err error
}

func (c *cursor) u32() uint32 {
	if c.err != nil {
		return 0
	}
	if c.at+4 > len(c.b) {
		c.err = io.ErrUnexpectedEOF
		return 0
	}
	v := binary.LittleEndian.Uint32(c.b[c.at:])
	c.at += 4
	return v
}

func (c *cursor) bytes(n uint32) []byte {
	if c.err != nil {
		return nil
	}
	if uint64(n) > uint64(len(c.b)-c.at) {
		c.err = io.ErrUnexpectedEOF
		return nil
	}
	b := c.b[c.at : c.at+int(n)]
	c.at += int(n)
	return b
}

func (c *cursor) skip(n uint32) { c.bytes(n) }

// each runs f n times. Every caller consumes at least one word, so a length read out of nonsense runs
// off the end of the file rather than spinning.
func (c *cursor) each(n uint32, f func()) {
	for i := uint32(0); i < n && c.err == nil; i++ {
		f()
	}
}

func (c *cursor) ebitmap() map[uint32]bool {
	c.u32() // mapsize
	c.u32() // highbit
	bits := map[uint32]bool{}
	c.each(c.u32(), func() {
		start := c.u32()
		node := binary.LittleEndian.Uint64(c.bytes(8))
		for i := range uint32(mapSize) {
			if node&(1<<i) != 0 {
				bits[start+i] = true
			}
		}
	})
	return bits
}

func (c *cursor) perm() {
	n := c.u32()
	c.u32() // value
	c.skip(n)
}

func (c *cursor) common() {
	n := c.u32()
	c.u32() // value
	c.u32() // nprim
	nel := c.u32()
	c.skip(n)
	c.each(nel, c.perm)
}

func (c *cursor) class(vers uint32) {
	n := c.u32()
	common := c.u32()
	c.u32() // value
	c.u32() // nprim
	nel := c.u32()
	cons := c.u32()
	c.skip(n)
	c.skip(common)
	c.each(nel, c.perm)

	c.constraints(vers, cons)
	if vers >= verValidateTrans {
		c.constraints(vers, c.u32())
	}
	if vers >= verObjectDefaults {
		c.skip(12) // default user, role and range
	}
	if vers >= verDefaultType {
		c.skip(4)
	}
}

func (c *cursor) constraints(vers, n uint32) {
	c.each(n, func() {
		c.u32() // permissions
		c.each(c.u32(), func() {
			kind := c.u32()
			c.u32() // attr
			c.u32() // op
			if kind != cexprNames {
				return
			}
			c.ebitmap()
			if vers >= verConstraintNames {
				c.ebitmap() // types
				c.ebitmap() // negative set
				c.u32()     // flags
			}
		})
	})
}

func (c *cursor) role(vers uint32) {
	n := c.u32()
	c.u32() // value
	if vers >= verBoundary {
		c.u32() // bounds
	}
	c.skip(n)
	c.ebitmap() // dominates
	c.ebitmap() // types
}

func (c *cursor) typ(vers uint32) ([]byte, uint32) {
	n := c.u32()
	value := c.u32()
	if vers >= verBoundary {
		c.u32() // properties
		c.u32() // bounds
	} else {
		c.u32() // primary
	}
	return c.bytes(n), value
}
