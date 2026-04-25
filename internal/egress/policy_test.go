package egress

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
)

func mustPolicy(t *testing.T, cfg config.Egress) *Policy {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return p
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Egress
		want string
	}{
		{"bad cidr", config.Egress{AllowCIDRs: []string{"10.0.0.0/33"}}, "allow_cidrs"},
		{"mapped cidr", config.Egress{AllowCIDRs: []string{"::ffff:10.0.0.0/104"}}, "dotted form"},
		{"bad host", config.Egress{AllowHosts: []string{"exa mple.com"}}, "allow_hosts"},
		{"wildcard ip", config.Egress{AllowHosts: []string{"*.10.0.0.1"}}, "allow_hosts"},
		{"bad port", config.Egress{AllowHosts: []string{"api.example.com:99999"}}, "port"},
		{"non-ascii host", config.Egress{AllowHosts: []string{"bücher.example"}}, "allow_hosts"},
		{"numeric host", config.Egress{AllowHosts: []string{"2130706433"}}, "numeric"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestNewMasksCIDRs(t *testing.T) {
	p := mustPolicy(t, config.Egress{AllowCIDRs: []string{"10.1.2.3/16"}})
	if got := p.allowCIDRs[0].String(); got != "10.1.0.0/16" {
		t.Fatalf("prefix %s", got)
	}
}

func TestCheckIP(t *testing.T) {
	strict := config.Egress{}
	loop := config.Egress{AllowLoopback: true}
	internal := config.Egress{AllowCIDRs: []string{"10.20.0.0/16", "fd12:3456::/32", "169.254.0.0/16", "0.0.0.0/0"}}
	nat64 := config.Egress{AllowCIDRs: []string{"64:ff9b::/96"}}
	tests := []struct {
		name  string
		cfg   config.Egress
		addr  string
		allow bool
	}{
		{"public v4", strict, "93.184.216.34", true},
		{"public v6", strict, "2606:4700:4700::1111", true},
		{"loopback", strict, "127.0.0.1", false},
		{"loopback range", strict, "127.8.9.10", false},
		{"loopback v6", strict, "::1", false},
		{"mapped loopback", strict, "::ffff:127.0.0.1", false},
		{"compat loopback", strict, "::127.0.0.1", false},
		{"unspecified", strict, "0.0.0.0", false},
		{"unspecified v6", strict, "::", false},
		{"metadata", strict, "169.254.169.254", false},
		{"mapped metadata", strict, "::ffff:169.254.169.254", false},
		{"link-local", strict, "169.254.10.10", false},
		{"link-local v6", strict, "fe80::1", false},
		{"private 10", strict, "10.0.0.1", false},
		{"private 172", strict, "172.16.5.4", false},
		{"private 192", strict, "192.168.0.1", false},
		{"mapped private", strict, "::ffff:192.168.0.1", false},
		{"cgnat", strict, "100.64.1.1", false},
		{"ula", strict, "fd00::1", false},
		{"multicast", strict, "224.0.0.251", false},
		{"multicast v6", strict, "ff02::fb", false},
		{"broadcast", strict, "255.255.255.255", false},
		{"nat64 private", strict, "64:ff9b::a00:1", false},
		{"nat64 metadata", strict, "64:ff9b::a9fe:a9fe", false},
		{"nat64 public", strict, "64:ff9b::808:808", true},
		{"6to4 private", strict, "2002:c0a8:0001::1", false},
		{"6to4 public", strict, "2002:0808:0808::1", true},
		{"teredo", strict, "2001:0:4136:e378:8000:63bf:3fff:fdd2", false},
		{"zoned", strict, "fe80::1%eth0", false},

		{"loopback allowed", loop, "127.0.0.1", true},
		{"loopback v6 allowed", loop, "::1", true},
		{"mapped loopback allowed", loop, "::ffff:127.0.0.1", true},
		{"loopback does not open private", loop, "10.0.0.1", false},
		{"loopback does not open unspecified", loop, "0.0.0.0", false},
		{"nat64 loopback allowed", loop, "64:ff9b::7f00:1", true},

		{"cidr private", internal, "10.20.3.4", true},
		{"cidr mapped private", internal, "::ffff:10.20.3.4", true},
		{"cidr outside", internal, "10.21.0.1", true}, // inside 0.0.0.0/0
		{"cidr ula", internal, "fd12:3456::1", true},
		{"cidr ula outside", internal, "fd99::1", false},
		{"cidr link-local", internal, "169.254.1.1", true},
		{"cidr never opens metadata", internal, "169.254.169.254", false},
		{"cidr never opens unspecified", internal, "0.0.0.0", false},
		{"cidr never opens broadcast", internal, "255.255.255.255", false},
		{"cidr never opens multicast", internal, "239.1.1.1", false},
		{"cidr does not open loopback", internal, "127.0.0.1", false},
		{"cidr nat64 prefix", nat64, "64:ff9b::a00:1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mustPolicy(t, tt.cfg).CheckIP(netip.MustParseAddr(tt.addr))
			if tt.allow {
				if err != nil {
					t.Fatalf("CheckIP(%s) = %v, want allowed", tt.addr, err)
				}
				return
			}
			if !errors.Is(err, ErrDestinationDenied) {
				t.Fatalf("CheckIP(%s) = %v, want ErrDestinationDenied", tt.addr, err)
			}
			var de *DeniedError
			if !errors.As(err, &de) || de.Reason == "" {
				t.Fatalf("CheckIP(%s) error %T lacks a reason", tt.addr, err)
			}
		})
	}
	if err := mustPolicy(t, strict).CheckIP(netip.Addr{}); !errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("zero address: %v", err)
	}
}

func TestDeniedErrorMessages(t *testing.T) {
	ip := netip.MustParseAddr("10.0.0.7")
	tests := []struct {
		err  *DeniedError
		want string
	}{
		{&DeniedError{Addr: ip, Reason: "private address"},
			"destination denied by egress policy: 10.0.0.7 (private address)"},
		{&DeniedError{Host: "api.example.com", Addr: ip, Reason: "private address"},
			`destination denied by egress policy: host "api.example.com" resolves to 10.0.0.7 (private address)`},
		{&DeniedError{Host: "10.0.0.7", Addr: ip, Reason: "private address"},
			"destination denied by egress policy: 10.0.0.7 (private address)"},
		{&DeniedError{Host: "evil.example:443", Reason: "not listed in egress.allow_hosts"},
			`destination denied by egress policy: host "evil.example:443": not listed in egress.allow_hosts`},
		{&DeniedError{Reason: "no URL"}, "destination denied by egress policy: no URL"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q\nwant      %q", got, tt.want)
		}
		if !errors.Is(tt.err, ErrDestinationDenied) {
			t.Errorf("%v does not wrap ErrDestinationDenied", tt.err)
		}
	}
}
