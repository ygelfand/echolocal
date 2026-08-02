package metrics

import (
	"net"
	"slices"
	"strings"
)

// Addresses is every address something else on the network could reach the device at. Loopback and
// link-local are left out: neither is an answer to "where is it".
//
// Not a Reader: this comes from the network stack rather than from a file under Root, so there is
// nothing for a test to point somewhere else.
func Addresses() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	var ips []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if ok && ipnet.IP.IsGlobalUnicast() && !ipnet.IP.IsLinkLocalUnicast() {
			ips = append(ips, ipnet.IP)
		}
	}
	return ips
}

// AddressKey is a set of addresses as one comparable string, so a change is one comparison.
func AddressKey(ips []net.IP) string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}
