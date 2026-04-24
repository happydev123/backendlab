package egress

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		addr string
		want category
	}{
		{"8.8.8.8", catPublic},
		{"93.184.216.34", catPublic},
		{"2606:4700:4700::1111", catPublic},
		{"0.0.0.0", catUnspecified},
		{"0.1.2.3", catUnspecified},
		{"::", catUnspecified},
		{"127.0.0.1", catLoopback},
		{"127.255.255.254", catLoopback},
		{"::1", catLoopback},
		{"::ffff:127.0.0.1", catLoopback},
		{"::ffff:7f00:1", catLoopback},
		{"169.254.169.254", catMetadata},
		{"::ffff:169.254.169.254", catMetadata},
		{"fd00:ec2::254", catMetadata},
		{"169.254.1.1", catLinkLocal},
		{"fe80::1", catLinkLocal},
		{"febf::1", catLinkLocal},
		{"10.0.0.1", catPrivate},
		{"172.16.0.1", catPrivate},
		{"172.31.255.255", catPrivate},
		{"172.32.0.1", catPublic},
		{"192.168.1.1", catPrivate},
		{"::ffff:10.1.2.3", catPrivate},
		{"100.64.1.1", catCGNAT},
		{"100.127.255.255", catCGNAT},
		{"100.128.0.1", catPublic},
		{"fd00::1", catULA},
		{"fc00::1", catULA},
		{"224.0.0.1", catMulticast},
		{"239.255.255.250", catMulticast},
		{"ff02::1", catMulticast},
		{"255.255.255.255", catBroadcast},
		{"240.0.0.1", catReserved},
		{"198.18.0.1", catReserved},
		{"192.0.0.8", catReserved},
		{"192.0.2.1", catDocument},
		{"2001:db8::1", catDocument},
		{"fec0::1", catSiteLocal},
		{"::127.0.0.1", catIPv4Compat},
		{"::8.8.8.8", catIPv4Compat},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", catTeredo},
		{"64:ff9b:1::a00:1", catNAT64Local},
		{"::ffff:0:a00:1", catReserved},
		{"100::1", catReserved},
		{"fe80::1%eth0", catZoned},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.addr)
			if got := classify(ip); got != tt.want {
				t.Fatalf("classify(%s) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
	if got := classify(netip.Addr{}); got != catInvalid {
		t.Fatalf("classify(zero) = %q", got)
	}
}

func TestEmbeddedIPv4(t *testing.T) {
	tests := []struct {
		addr string
		want string // empty: nothing embedded
	}{
		{"64:ff9b::a00:1", "10.0.0.1"},
		{"64:ff9b::808:808", "8.8.8.8"},
		{"64:ff9b::7f00:1", "127.0.0.1"},
		{"2002:a9fe:a9fe::1", "169.254.169.254"},
		{"2002:0808:0808::", "8.8.8.8"},
		{"2606:4700::1", ""},
		{"::ffff:10.0.0.1", ""},
		{"10.0.0.1", ""},
	}
	for _, tt := range tests {
		got, ok := embeddedIPv4(netip.MustParseAddr(tt.addr))
		if tt.want == "" {
			if ok {
				t.Errorf("embeddedIPv4(%s) = %s, want none", tt.addr, got)
			}
			continue
		}
		if !ok || got != netip.MustParseAddr(tt.want) {
			t.Errorf("embeddedIPv4(%s) = %s, %v; want %s", tt.addr, got, ok, tt.want)
		}
	}
}

func TestHardDenied(t *testing.T) {
	for _, c := range []category{catUnspecified, catMetadata, catMulticast, catBroadcast, catZoned, catInvalid} {
		if !c.hardDenied() {
			t.Errorf("%q should be hard-denied", c)
		}
	}
	for _, c := range []category{catPrivate, catLoopback, catCGNAT, catULA, catLinkLocal, catPublic} {
		if c.hardDenied() {
			t.Errorf("%q should not be hard-denied", c)
		}
	}
}
