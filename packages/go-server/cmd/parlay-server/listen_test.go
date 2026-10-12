package main

import (
	"errors"
	"net/netip"
	"testing"
)

func ifaces(addrs ...string) func() ([]netip.Addr, error) {
	return func() ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range addrs {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestResolveListen(t *testing.T) {
	cases := []struct {
		name, exposure, addr string
		explicit             bool
		ifs                  func() ([]netip.Addr, error)
		wantAddr, wantName   string
		wantErr              bool
	}{
		{"default is loopback", "", "", false, ifaces(), "127.0.0.1:4242", "loopback", false},
		{"lan", "lan", "", false, ifaces(), "0.0.0.0:4242", "lan", false},
		{"tailnet picks the tailscale address", "tailnet", "", false, ifaces("192.168.1.5", "100.101.102.103"), "100.101.102.103:4242", "tailnet", false},
		{"tailnet without tailscale fails loudly", "tailnet", "", false, ifaces("192.168.1.5"), "", "", true},
		{"tailnet interface error fails", "tailnet", "", false, func() ([]netip.Addr, error) { return nil, errors.New("boom") }, "", "", true},
		{"explicit addr wins over exposure", "lan", "127.0.0.1:9999", true, ifaces(), "127.0.0.1:9999", "loopback", false},
		{"explicit wildcard addr reports lan", "", "0.0.0.0:4242", true, ifaces(), "0.0.0.0:4242", "lan", false},
		{"explicit other host is custom", "", "10.0.0.2:4242", true, ifaces(), "10.0.0.2:4242", "custom", false},
		{"unknown exposure is refused", "wan", "", false, ifaces(), "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, name, err := resolveListen(c.exposure, c.addr, c.explicit, c.ifs)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if addr != c.wantAddr || name != c.wantName {
				t.Fatalf("got (%q, %q), want (%q, %q)", addr, name, c.wantAddr, c.wantName)
			}
		})
	}
}
