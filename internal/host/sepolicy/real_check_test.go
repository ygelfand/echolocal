package sepolicy

import (
	"os"
	"testing"
)

func TestRealPolicy(t *testing.T) {
	path := os.Getenv("SEPOLICY")
	if path == "" {
		t.Skip("set SEPOLICY")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	c := &cursor{b: raw}
	c.u32()
	c.bytes(c.u32())
	vers := c.u32()
	c.u32()
	c.u32()
	c.u32()
	c.ebitmap()
	c.ebitmap()
	t.Logf("vers=%d after maps at %d", vers, c.at)
	for _, sym := range []string{"commons", "classes", "roles"} {
		nprim := c.u32()
		nel := c.u32()
		t.Logf("%s nprim=%d nel=%d at %d err=%v", sym, nprim, nel, c.at, c.err)
		switch sym {
		case "commons":
			c.each(nel, c.common)
		case "classes":
			c.each(nel, func() { c.class(vers) })
		case "roles":
			c.each(nel, func() { c.role(vers) })
		}
		t.Logf("  -> at %d err=%v", c.at, c.err)
	}
	t.Logf("types nprim=%d nel=%d", c.u32(), c.u32())

	got, err := scan(raw, map[string]bool{"init": true, "su": true, "ledcontroller": true})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	t.Logf("permissive map at %d..%d, bits %v, types %v", got.at, got.end, got.bits, got.types)

	out, err := Permissive(raw, "init")
	if err != nil {
		t.Fatalf("Permissive: %v", err)
	}
	t.Logf("policy %d -> %d bytes", len(raw), len(out))
	if err := os.WriteFile(path+".permissive", out, 0o644); err != nil {
		t.Fatal(err)
	}
}
