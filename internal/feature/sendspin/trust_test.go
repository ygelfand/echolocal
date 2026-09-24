package sendspin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The spec's reference vector for a version-0 token.
func TestTokenMatchesSpec(t *testing.T) {
	payload := make([]byte, 64)
	for i := range 32 {
		payload[i] = byte(i)
		payload[32+i] = byte(0xe0 + i)
	}
	want := "SP:0AAAQEAYEAUDAOCAJBIFQYDIOB4IBCEQTCQKRMFYYDENBWHA5DYP6BYPC4PSOLZXH5DU6V97M5XXO74HR6LZ7J5PW674PT6X37T6757Y"
	if got := encodeToken(0, payload); got != want {
		t.Fatalf("token\n got %s\nwant %s", got, want)
	}
}

func TestTrustIsMadeOnceAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")

	first, err := loadTrust(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o, want 600", info.Mode().Perm())
	}
	if !strings.HasPrefix(first.token(), "SP:0") {
		t.Fatalf("token %q", first.token())
	}

	again, err := loadTrust(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.clientID() != first.clientID() || again.token() != first.token() {
		t.Fatal("reloading changed the identity")
	}

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTrust(path); err == nil {
		t.Fatal("a corrupt file was replaced with a new identity")
	}
}

func TestTrustLookupBindsRecordsToServers(t *testing.T) {
	tr, err := loadTrust(filepath.Join(t.TempDir(), "sendspin.json"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := newPSK()
	if err := tr.remember("server-a", key, ""); err != nil {
		t.Fatal(err)
	}

	if p, cat, err := tr.lookup("server-a")(key.id()); err != nil || cat != categoryLongTerm || p != key {
		t.Fatalf("own record: %v %v", err, cat)
	}
	if _, _, err := tr.lookup("server-b")(key.id()); err == nil || err == errPSKMiss {
		t.Fatalf("another server naming our record: %v", err)
	}
	if _, cat, err := tr.lookup("server-b")(tr.pairingPSK().id()); err != nil || cat != categoryPairing {
		t.Fatalf("pairing key: %v %v", err, cat)
	}
	if _, cat, err := tr.lookup("server-b")(sentinelPSK().id()); err != nil || cat != categorySentinel {
		t.Fatalf("sentinel: %v %v", err, cat)
	}
	unknown, _ := newPSK()
	if _, _, err := tr.lookup("server-b")(unknown.id()); err != errPSKMiss {
		t.Fatalf("unknown key: %v", err)
	}

	if err := tr.forget("server-a"); err != nil {
		t.Fatal(err)
	}
	if tr.paired("server-a") {
		t.Fatal("forgotten record still there")
	}
}

func TestTrustEvictsTheLeastRecentlyUsed(t *testing.T) {
	tr, err := loadTrust(filepath.Join(t.TempDir(), "sendspin.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxRecords {
		key, _ := newPSK()
		if err := tr.remember(string(rune('a'+i)), key, ""); err != nil {
			t.Fatal(err)
		}
		// Distinct timestamps, oldest first.
		tr.f.Records[i].LastUsed = time.Unix(int64(i), 0)
	}
	tr.touch("a") // now the newest

	key, _ := newPSK()
	if err := tr.remember("new", key, "b"); err != nil {
		t.Fatal(err)
	}
	if tr.count() != maxRecords {
		t.Fatalf("%d records, want %d", tr.count(), maxRecords)
	}
	// b is in use and a was touched, so c is the oldest that may go.
	for _, keep := range []string{"a", "b", "new"} {
		if !tr.paired(keep) {
			t.Fatalf("%s was evicted", keep)
		}
	}
	if tr.paired("c") {
		t.Fatal("c should have been evicted")
	}
}
