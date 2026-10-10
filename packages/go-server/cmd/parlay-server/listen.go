package main

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Exposure is the one explicit choice of who can reach the server. It is the
// answer to "does anything off this machine need to connect", made once:
//
//	loopback  127.0.0.1 only (default) — this machine's clients
//	lan       0.0.0.0 — every interface; the LAN and any tailnet can connect
//	tailnet   this machine's Tailscale address only (100.64.0.0/10)
//
// An explicit -addr / PARLAY_SERVER_ADDR still wins and reports as "custom".
// The server has no authentication, so lan and tailnet widen who can reach
// it; the origin guard (internal/guard) still decides which BROWSER origins
// may act.
const defaultPort = "4242"

func resolveListen(exposure, addr string, addrExplicit bool, ifaceAddrs func() ([]netip.Addr, error)) (listen, name string, err error) {
	if addrExplicit {
		return addr, classifyAddr(addr), nil
	}
	switch strings.ToLower(strings.TrimSpace(exposure)) {
	case "", "loopback":
		return "127.0.0.1:" + defaultPort, "loopback", nil
	case "lan":
		return "0.0.0.0:" + defaultPort, "lan", nil
	case "tailnet":
		addrs, err := ifaceAddrs()
		if err != nil {
			return "", "", fmt.Errorf("exposure tailnet: cannot list interfaces: %w", err)
		}
		tsRange := netip.MustParsePrefix("100.64.0.0/10")
		for _, a := range addrs {
			if tsRange.Contains(a) {
				return net.JoinHostPort(a.String(), defaultPort), "tailnet", nil
			}
		}
		return "", "", fmt.Errorf("exposure tailnet: no Tailscale address (100.64.0.0/10) on this machine — is Tailscale up? use exposure lan or an explicit -addr")
	}
	return "", "", fmt.Errorf("unknown exposure %q (want loopback, lan or tailnet)", exposure)
}

// classifyAddr names an explicit listen address in the exposure vocabulary.
func classifyAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "custom"
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return "loopback"
	case "", "0.0.0.0", "::":
		return "lan"
	}
	return "custom"
}

func systemIfaceAddrs() ([]netip.Addr, error) {
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, a := range ifs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr())
		}
	}
	return out, nil
}
