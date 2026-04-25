// Package egress restricts the outbound destinations of server-mode runs.
// A Policy checks URLs against a host allowlist, checks IP addresses
// against the ranges that must not be reachable from a shared server
// (loopback, private, link-local, cloud metadata and similar), and
// provides a dial function that resolves a host once, checks every
// resolved address, and connects only to an address it checked.
//
// The CLI does not use this package: a local user may target anything
// their machine can reach. Application checks complement, but do not
// replace, network-level egress controls; see docs/egress.md.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/happydev123/backendlab/internal/config"
)

// ErrDestinationDenied is wrapped by every policy refusal. Callers test
// for it with errors.Is to tell a refusal from a transport failure.
var ErrDestinationDenied = errors.New("destination denied by egress policy")

// DeniedError describes one refusal. Its message names the host and, when
// relevant, the address and the reason. It never includes URL paths,
// query strings or userinfo, so it is safe to log and to store in reports.
type DeniedError struct {
	// Host is the normalized host name or IP literal that was refused.
	Host string
	// Addr is the address that failed the check, if any. For a host name
	// it is the resolved address that caused the refusal.
	Addr netip.Addr
	// Reason is a short explanation such as "private address".
	Reason string
}

func (e *DeniedError) Error() string {
	switch {
	case e.Host == "" && e.Addr.IsValid():
		return fmt.Sprintf("%v: %s (%s)", ErrDestinationDenied, e.Addr, e.Reason)
	case e.Addr.IsValid() && e.Host != e.Addr.String():
		return fmt.Sprintf("%v: host %q resolves to %s (%s)", ErrDestinationDenied, e.Host, e.Addr, e.Reason)
	case e.Addr.IsValid():
		return fmt.Sprintf("%v: %s (%s)", ErrDestinationDenied, e.Addr, e.Reason)
	case e.Host != "":
		return fmt.Sprintf("%v: host %q: %s", ErrDestinationDenied, e.Host, e.Reason)
	}
	return fmt.Sprintf("%v: %s", ErrDestinationDenied, e.Reason)
}

// Unwrap returns ErrDestinationDenied.
func (e *DeniedError) Unwrap() error { return ErrDestinationDenied }

func denied(host string, addr netip.Addr, reason string) error {
	return &DeniedError{Host: host, Addr: addr, Reason: reason}
}

// resolver is the lookup used by DialContext. *net.Resolver satisfies it;
// tests substitute a fixed table.
type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Policy is an immutable set of egress rules, safe for concurrent use.
type Policy struct {
	hosts         []hostPattern
	allowCIDRs    []netip.Prefix
	allowLoopback bool
	resolver      resolver
}

// New builds a policy from server configuration. An empty AllowHosts list
// places no restriction on host names; address checks still apply to
// every destination.
func New(cfg config.Egress) (*Policy, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &Policy{allowLoopback: cfg.AllowLoopback, resolver: net.DefaultResolver}
	for _, h := range cfg.AllowHosts {
		hp, err := parseHostPattern(h)
		if err != nil {
			return nil, fmt.Errorf("egress.allow_hosts: %w", err)
		}
		p.hosts = append(p.hosts, hp)
	}
	for _, c := range cfg.AllowCIDRs {
		prefix, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("egress.allow_cidrs: %q: %v", c, err)
		}
		if prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("egress.allow_cidrs: %q: write IPv4 ranges in dotted form", c)
		}
		if prefix.Addr().Zone() != "" {
			return nil, fmt.Errorf("egress.allow_cidrs: %q: zones are not allowed", c)
		}
		p.allowCIDRs = append(p.allowCIDRs, prefix.Masked())
	}
	return p, nil
}

// CheckIP reports whether ip may be dialed. Public unicast addresses are
// allowed. Non-public addresses are refused unless:
//
//   - the address is loopback and AllowLoopback is set, or
//   - the address lies inside one of AllowCIDRs.
//
// Unspecified, multicast, broadcast and cloud metadata addresses are
// refused regardless of AllowCIDRs. IPv4-mapped IPv6 addresses are judged
// as the IPv4 address they map to. NAT64 (64:ff9b::/96) and 6to4
// (2002::/16) addresses are refused when the IPv4 address they embed
// would be refused, unless the IPv6 address itself is in AllowCIDRs.
func (p *Policy) CheckIP(ip netip.Addr) error {
	cat := classify(ip)
	if cat.hardDenied() {
		return denied("", ip, string(cat))
	}
	ip = ip.Unmap()
	if cat == catLoopback {
		if p.allowLoopback {
			return nil
		}
		return denied("", ip, string(cat)+"; allow_loopback is off")
	}
	if p.inAllowedCIDR(ip) {
		return nil
	}
	if cat != catPublic {
		return denied("", ip, string(cat))
	}
	if v4, ok := embeddedIPv4(ip); ok {
		if err := p.CheckIP(v4); err != nil {
			return denied("", ip, fmt.Sprintf("%s embedding denied %s", catEmbeddedBad, v4))
		}
	}
	return nil
}

func (p *Policy) inAllowedCIDR(ip netip.Addr) bool {
	for _, c := range p.allowCIDRs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}
