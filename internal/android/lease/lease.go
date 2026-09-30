// Package lease reads what the DHCP server told this device, from the lease dhcpcd keeps.
//
// dhcpcd's hook promotes a few options to system properties: the address, the routers, the
// nameservers. Anything else it was told stays in the lease file, which is the DHCP message as it
// arrived. The time servers are there, if the server offered them and dhcpcd asked.
package lease

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// Path is where dhcpcd keeps the wifi lease on this device.
const Path = "/data/misc/dhcp/dhcpcd-wlan0.lease"

// The DHCP message layout: the fixed BOOTP header, the magic cookie that says options follow, and
// the option codes read here.
const (
	headerLen  = 236
	magic      = 0x63825363
	optPad     = 0
	optNTP     = 42
	optEnd     = 255
	optionsMin = headerLen + 4
)

// NTPServers is the time servers the lease carries, in the order the server listed them. No option is
// an empty list, not an error: most servers do not offer one.
func NTPServers(path string) ([]net.IP, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseNTP(b)
}

// ParseNTP reads option 42 out of a raw DHCP message.
func ParseNTP(msg []byte) ([]net.IP, error) {
	if len(msg) < optionsMin {
		return nil, fmt.Errorf("lease: %d bytes is too short for a DHCP message", len(msg))
	}
	if binary.BigEndian.Uint32(msg[headerLen:optionsMin]) != magic {
		return nil, errors.New("lease: no DHCP options cookie")
	}

	var out []net.IP
	for i := optionsMin; i < len(msg); {
		code := msg[i]
		switch code {
		case optEnd:
			return out, nil
		case optPad:
			i++
			continue
		}
		if i+1 >= len(msg) {
			break
		}
		n := int(msg[i+1])
		val := msg[i+2 : min(i+2+n, len(msg))]
		if code == optNTP {
			for j := 0; j+4 <= len(val); j += 4 {
				out = append(out, net.IPv4(val[j], val[j+1], val[j+2], val[j+3]))
			}
		}
		i += 2 + n
	}
	return out, nil
}

// ConfPath is dhcpcd's configuration, which says what it asks the server for.
const ConfPath = "/system/etc/dhcpcd/dhcpcd.conf"

// RequestNTP adds the time servers to every option line that asks for nameservers, so the lease
// carries them. It reports whether anything changed; a file that already asks is left as it is.
func RequestNTP(conf string) (string, bool) {
	lines := strings.Split(conf, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "option ") || !strings.Contains(trimmed, "domain_name_servers") {
			continue
		}
		if strings.Contains(trimmed, "ntp_servers") {
			continue
		}
		lines[i] = strings.TrimRight(line, " \t") + ", ntp_servers"
		changed = true
	}
	return strings.Join(lines, "\n"), changed
}
