package lease

import (
	"encoding/binary"
	"net"
	"testing"
)

// message builds a DHCP message carrying the given options, the way dhcpcd stores one.
func message(opts ...[]byte) []byte {
	b := make([]byte, headerLen)
	b = binary.BigEndian.AppendUint32(b, magic)
	for _, o := range opts {
		b = append(b, o...)
	}
	return append(b, optEnd)
}

func TestParseNTP(t *testing.T) {
	msg := message(
		[]byte{53, 1, 5},
		[]byte{6, 4, 192, 168, 4, 5},
		[]byte{optPad},
		[]byte{optNTP, 8, 192, 168, 15, 3, 10, 0, 0, 1},
		[]byte{3, 4, 192, 168, 15, 1},
	)
	got, err := ParseNTP(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := []net.IP{net.IPv4(192, 168, 15, 3), net.IPv4(10, 0, 0, 1)}
	if len(got) != 2 || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) {
		t.Fatalf("got %v, want %v", got, want)
	}

	none, err := ParseNTP(message([]byte{6, 4, 192, 168, 4, 5}))
	if err != nil || len(none) != 0 {
		t.Fatalf("no option: %v %v", none, err)
	}
}

func TestParseNTPRejectsJunk(t *testing.T) {
	if _, err := ParseNTP([]byte("short")); err == nil {
		t.Fatal("a short message parsed")
	}
	noCookie := make([]byte, optionsMin+4)
	if _, err := ParseNTP(noCookie); err == nil {
		t.Fatal("a message without the cookie parsed")
	}
	// A length running past the end is clipped rather than read out of bounds.
	trunc := message([]byte{optNTP, 12, 1, 2, 3, 4})
	trunc = trunc[:len(trunc)-1]
	if got, err := ParseNTP(trunc); err != nil || len(got) != 1 {
		t.Fatalf("truncated option: %v %v", got, err)
	}
}

func TestRequestNTP(t *testing.T) {
	conf := "ipv6ra_accept_nopublic\ninterface wlan0\noption subnet_mask, routers, domain_name_servers, domain_name, domain_search\ninterface eth0\noption subnet_mask, routers, domain_name_servers, domain_name, domain_search\n"
	out, changed := RequestNTP(conf)
	if !changed {
		t.Fatal("nothing changed")
	}
	want := "ipv6ra_accept_nopublic\ninterface wlan0\noption subnet_mask, routers, domain_name_servers, domain_name, domain_search, ntp_servers\ninterface eth0\noption subnet_mask, routers, domain_name_servers, domain_name, domain_search, ntp_servers\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if again, changed := RequestNTP(out); changed || again != out {
		t.Fatal("a second pass changed the file")
	}
}
