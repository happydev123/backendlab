package egress

import "net/netip"

// category names why an address is not a public unicast destination. The
// names appear in error messages.
type category string

const (
	catPublic      category = ""
	catUnspecified category = "unspecified address"
	catLoopback    category = "loopback address"
	catMetadata    category = "cloud metadata address"
	catLinkLocal   category = "link-local address"
	catPrivate     category = "private address"
	catCGNAT       category = "shared address space (CGNAT)"
	catULA         category = "unique local IPv6 address"
	catMulticast   category = "multicast address"
	catBroadcast   category = "broadcast address"
	catReserved    category = "reserved address"
	catDocument    category = "documentation address"
	catSiteLocal   category = "deprecated site-local IPv6 address"
	catIPv4Compat  category = "deprecated IPv4-compatible IPv6 address"
	catTeredo      category = "Teredo tunnel address"
	catNAT64Local  category = "local-use NAT64 address"
	catZoned       category = "address with an IPv6 zone"
	catInvalid     category = "invalid address"
	catEmbeddedBad category = "NAT64 or 6to4 address"
)

// hardDenied categories are refused even when the address falls inside an
// operator's allow_cidrs: none of them is a usable HTTP upstream, and the
// metadata address is the classic target of server-side request forgery.
func (c category) hardDenied() bool {
	switch c {
	case catUnspecified, catMetadata, catMulticast, catBroadcast, catInvalid, catZoned:
		return true
	}
	return false
}

type rangeEntry struct {
	prefix netip.Prefix
	cat    category
}

// specialRanges returns the non-public ranges checked by classify, most
// specific first. It is a function rather than a package variable so the
// table cannot be modified at run time.
func specialRanges() []rangeEntry {
	p := netip.MustParsePrefix
	return []rangeEntry{
		// Single addresses first so they win over their enclosing ranges.
		{p("169.254.169.254/32"), catMetadata}, // AWS, GCP, Azure, OpenStack
		{p("fd00:ec2::254/128"), catMetadata},  // AWS IMDS over IPv6
		{p("255.255.255.255/32"), catBroadcast},

		// IPv4 (RFC 6890 special-purpose registry).
		{p("0.0.0.0/8"), catUnspecified}, // "this network"; 0.0.0.0 reaches the local host on many systems
		{p("10.0.0.0/8"), catPrivate},
		{p("100.64.0.0/10"), catCGNAT},
		{p("127.0.0.0/8"), catLoopback},
		{p("169.254.0.0/16"), catLinkLocal},
		{p("172.16.0.0/12"), catPrivate},
		{p("192.0.0.0/24"), catReserved}, // IETF protocol assignments
		{p("192.0.2.0/24"), catDocument},
		{p("192.88.99.0/24"), catReserved}, // deprecated 6to4 relay anycast
		{p("192.168.0.0/16"), catPrivate},
		{p("198.18.0.0/15"), catReserved}, // benchmarking
		{p("198.51.100.0/24"), catDocument},
		{p("203.0.113.0/24"), catDocument},
		{p("224.0.0.0/4"), catMulticast},
		{p("240.0.0.0/4"), catReserved},

		// IPv6.
		{p("::/128"), catUnspecified},
		{p("::1/128"), catLoopback},
		{p("::/96"), catIPv4Compat},
		{p("::ffff:0:0:0/96"), catReserved}, // SIIT IPv4-translated (RFC 2765)
		{p("64:ff9b:1::/48"), catNAT64Local},
		{p("100::/64"), catReserved}, // discard-only
		{p("2001::/32"), catTeredo},
		{p("2001:db8::/32"), catDocument},
		{p("fc00::/7"), catULA},
		{p("fe80::/10"), catLinkLocal},
		{p("fec0::/10"), catSiteLocal},
		{p("ff00::/8"), catMulticast},
	}
}

// classify reports the category of ip. IPv4-mapped IPv6 addresses
// (::ffff:a.b.c.d) are unmapped first, so they are judged as the IPv4
// address they reach.
func classify(ip netip.Addr) category {
	if !ip.IsValid() {
		return catInvalid
	}
	if ip.Zone() != "" {
		return catZoned
	}
	ip = ip.Unmap()
	for _, r := range specialRanges() {
		if r.prefix.Contains(ip) {
			return r.cat
		}
	}
	return catPublic
}

// embeddedIPv4 returns the IPv4 address carried inside an IPv6 address by
// a translation mechanism that would deliver traffic to it:
//
//   - the NAT64 well-known prefix 64:ff9b::/96 (RFC 6052), last 32 bits;
//   - 6to4 2002::/16 (RFC 3056), bits 16 to 47.
//
// A NAT64 gateway forwards 64:ff9b::a.b.c.d to a.b.c.d, so the embedded
// address must pass the same checks as if it were dialed directly.
func embeddedIPv4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() || ip.Is4In6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case netip.MustParsePrefix("64:ff9b::/96").Contains(ip):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case netip.MustParsePrefix("2002::/16").Contains(ip):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}
