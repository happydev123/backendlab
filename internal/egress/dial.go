package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// maxResolvedAddrs bounds how many resolved addresses are considered for
// one dial. Addresses past the bound are neither checked nor dialed.
const maxResolvedAddrs = 32

// DialContext returns a dial function for http.Transport.DialContext that
// enforces the policy at connection time:
//
//  1. the host and port are checked against egress.allow_hosts;
//  2. a host name is resolved once, and every resolved address must pass
//     CheckIP, otherwise nothing is dialed (one denied address taints the
//     whole answer, so a mixed answer cannot be used to reach an internal
//     address on a later attempt);
//  3. the connection is made to the checked IP literal, so the address
//     that was checked is the address that is dialed, even if DNS answers
//     differently a moment later.
//
// Addresses are tried in resolver order until one connects. d may be nil;
// its Resolver field is not used.
func (p *Policy) DialContext(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if d == nil {
		d = &net.Dialer{}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ipNet, err := lookupNetwork(network)
		if err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, denied("", netip.Addr{}, fmt.Sprintf("invalid dial address: %v", err))
		}
		ip, name, err := p.checkHost(host, port)
		if err != nil {
			return nil, err
		}
		portNum, _ := strconv.ParseUint(port, 10, 16) // checkHost validated it

		var targets []netip.Addr
		if ip.IsValid() {
			targets = []netip.Addr{ip.Unmap()}
		} else {
			targets, err = p.resolve(ctx, ipNet, name)
			if err != nil {
				return nil, err
			}
		}

		var errs []error
		for _, t := range targets {
			c, err := d.DialContext(ctx, network, netip.AddrPortFrom(t, uint16(portNum)).String())
			if err == nil {
				return c, nil
			}
			errs = append(errs, err)
			if ctx.Err() != nil {
				break
			}
		}
		return nil, errors.Join(errs...)
	}
}

// resolve looks up name and checks every address. It returns the
// unmapped addresses in resolver order, with duplicates removed.
func (p *Policy) resolve(ctx context.Context, network, name string) ([]netip.Addr, error) {
	addrs, err := p.resolver.LookupNetIP(ctx, network, name)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", name, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", name)
	}
	if len(addrs) > maxResolvedAddrs {
		addrs = addrs[:maxResolvedAddrs]
	}
	seen := make(map[netip.Addr]bool, len(addrs))
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if err := p.CheckIP(a); err != nil {
			var de *DeniedError
			if errors.As(err, &de) {
				return nil, &DeniedError{Host: name, Addr: de.Addr, Reason: de.Reason}
			}
			return nil, err
		}
		a = a.Unmap()
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// lookupNetwork maps a dial network to the resolver network. Only TCP is
// permitted; HTTP transports never dial anything else.
func lookupNetwork(network string) (string, error) {
	switch network {
	case "tcp":
		return "ip", nil
	case "tcp4":
		return "ip4", nil
	case "tcp6":
		return "ip6", nil
	}
	return "", denied("", netip.Addr{}, fmt.Sprintf("network %q is not allowed", network))
}
