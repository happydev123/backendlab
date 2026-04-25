package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// hostPattern is one parsed egress.allow_hosts entry.
type hostPattern struct {
	// host is a normalized DNS name (without the "*." of a wildcard) or
	// empty when ip is set.
	host string
	// wildcard matches any name with at least one label in front of host.
	wildcard bool
	// ip is set for IP literal entries, which match only that address.
	ip netip.Addr
	// port restricts the entry to one port; empty means any port.
	port string
}

// parseHostPattern parses "name", "*.suffix", "name:port", "1.2.3.4",
// "[2001:db8::1]:8443" and similar entries.
func parseHostPattern(s string) (hostPattern, error) {
	host, port := s, ""
	if h, pt, err := net.SplitHostPort(s); err == nil {
		host, port = h, pt
		if err := validPort(port); err != nil {
			return hostPattern{}, fmt.Errorf("%q: %v", s, err)
		}
	} else if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		host = s[1 : len(s)-1]
	}
	var hp hostPattern
	hp.port = port
	if rest, ok := strings.CutPrefix(host, "*."); ok {
		hp.wildcard = true
		host = rest
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if hp.wildcard {
			return hostPattern{}, fmt.Errorf("%q: a wildcard cannot apply to an IP address", s)
		}
		if ip.Zone() != "" {
			return hostPattern{}, fmt.Errorf("%q: zones are not allowed", s)
		}
		hp.ip = ip.Unmap()
		return hp, nil
	}
	name, err := normalizeHost(host)
	if err != nil {
		return hostPattern{}, fmt.Errorf("%q: %v", s, err)
	}
	hp.host = name
	return hp, nil
}

func (hp hostPattern) matches(name string, ip netip.Addr, port string) bool {
	if hp.port != "" && hp.port != port {
		return false
	}
	switch {
	case hp.ip.IsValid():
		return ip.IsValid() && ip.Unmap() == hp.ip
	case ip.IsValid():
		return false // DNS patterns never match IP literals
	case hp.wildcard:
		return strings.HasSuffix(name, "."+hp.host)
	}
	return name == hp.host
}

func validPort(port string) error {
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return fmt.Errorf("port %q is not a number between 1 and 65535", port)
	}
	return nil
}

// normalizeHost lowercases a DNS name, removes one trailing dot (the
// absolute form "example.com." names the same host) and rejects names
// that are not plain ASCII hostnames. Names whose last label is numeric
// are rejected because resolvers disagree on them: some system resolvers
// read "2130706433", "0x7f.1" or "127.1" as IPv4 addresses.
func normalizeHost(h string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "" {
		return "", errors.New("empty host name")
	}
	if len(h) > 253 {
		return "", errors.New("host name longer than 253 characters")
	}
	labels := strings.Split(h, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "", errors.New("host name has an empty or overlong label")
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return "", errors.New("host name contains characters outside a-z, 0-9, '-' and '_' (use the punycode form for international names)")
			}
		}
	}
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		return "", errors.New("numeric host name that is not a canonical IP address")
	}
	return h, nil
}

// checkHost validates host (a URL host without brackets, or a dial host)
// and port against the allowlist. It returns the IP literal when host is
// one, otherwise the normalized name. Address checks for names happen at
// dial time, after resolution.
func (p *Policy) checkHost(host, port string) (netip.Addr, string, error) {
	if err := validPort(port); err != nil {
		return netip.Addr{}, "", denied(host, netip.Addr{}, err.Error())
	}
	var name string
	ip, err := netip.ParseAddr(host)
	if err != nil {
		name, err = normalizeHost(host)
		if err != nil {
			return netip.Addr{}, "", denied(host, netip.Addr{}, err.Error())
		}
		host = name
	} else {
		host = ip.String()
	}
	if len(p.hosts) > 0 {
		allowed := false
		for _, hp := range p.hosts {
			if hp.matches(name, ip, port) {
				allowed = true
				break
			}
		}
		if !allowed {
			return netip.Addr{}, "", denied(net.JoinHostPort(host, port), netip.Addr{}, "not listed in egress.allow_hosts")
		}
	}
	if ip.IsValid() {
		if err := p.CheckIP(ip); err != nil {
			return netip.Addr{}, "", err
		}
	}
	return ip, name, nil
}

// CheckURL reports whether u may be requested. The scheme must be http or
// https, the URL must not carry userinfo, and the host must match
// egress.allow_hosts (when it is set). An IP literal host is matched only
// by an identical IP entry and must also pass CheckIP. Host names are
// compared case-insensitively and without a trailing dot; the port
// defaults to 80 or 443 by scheme.
func (p *Policy) CheckURL(u *url.URL) error {
	if u == nil {
		return denied("", netip.Addr{}, "no URL")
	}
	host := u.Hostname()
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return denied(host, netip.Addr{}, fmt.Sprintf("scheme %q is not allowed; use http or https", u.Scheme))
	}
	if u.Opaque != "" || host == "" {
		return denied(host, netip.Addr{}, "URL has no host")
	}
	if u.User != nil {
		return denied(host, netip.Addr{}, "URL contains userinfo; credentials belong in target headers")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	_, _, err := p.checkHost(host, port)
	return err
}

// CheckRedirect applies CheckURL to a redirect target. It fits inside an
// http.Client CheckRedirect function, which also enforces the hop limit:
//
//	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
//		if len(via) >= 5 {
//			return http.ErrUseLastResponse
//		}
//		return policy.CheckRedirect(req)
//	}
func (p *Policy) CheckRedirect(req *http.Request) error {
	if req == nil {
		return denied("", netip.Addr{}, "no redirect request")
	}
	return p.CheckURL(req.URL)
}
