// Package pair owns first-run phone pairing: a server-generated bearer token
// persisted under the state dir, the address a phone can actually reach, and
// the start-up banner (URL + QR) that carries both to the phone in one scan.
package pair

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"parlay/go-server/internal/qr"
)

// TokenFile is the token's name under the state dir.
const TokenFile = "pair-token"

const minTokenLen = 16

// LoadOrCreate returns the persisted pairing token, minting a fresh 256-bit
// one (0600) on first run. A file that is missing or too short to be a real
// token is replaced, never trusted.
func LoadOrCreate(stateDir string) (string, error) {
	path := filepath.Join(stateDir, TokenFile)
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= minTokenLen {
			_ = os.Chmod(path, 0o600)
			return t, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("pair: generate token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", fmt.Errorf("pair: create state dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("pair: write token: %w", err)
	}
	return tok, nil
}

var tailscaleRange = netip.MustParsePrefix("100.64.0.0/10")

// ReachableHost picks the address a phone should dial for a server bound to
// listen under the named exposure. "" means no off-machine address exists
// (loopback). Order for lan/wildcard binds: Tailscale first (reachable from
// anywhere on the tailnet), then a private LAN address.
func ReachableHost(exposure, listen string, ifaceAddrs func() ([]netip.Addr, error)) string {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || exposure == "loopback" {
		return ""
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return host
	}
	addrs, err := ifaceAddrs()
	if err != nil {
		return ""
	}
	var lan string
	for _, a := range addrs {
		a = a.Unmap()
		switch {
		case !a.Is4():
		case tailscaleRange.Contains(a):
			return a.String()
		case a.IsPrivate() && lan == "":
			lan = a.String()
		}
	}
	return lan
}

// SlideURL is the page the phone opens. The token rides in the fragment,
// which browsers never send to the server and the guard never logs.
func SlideURL(host, port, token string) string {
	u := "http://" + net.JoinHostPort(host, port) + "/slide"
	if token != "" {
		u += "#t=" + token
	}
	return u
}

// Banner writes the start-up pairing guidance. The scannable code and
// token-bearing URL are secrets, so they are printed only when showSecret is
// true (stderr is a terminal a person is watching) — never into a log file.
func Banner(w io.Writer, exposure, listen, token string, showSecret bool, ifaceAddrs func() ([]netip.Addr, error)) {
	_, port, _ := net.SplitHostPort(listen)
	host := ReachableHost(exposure, listen, ifaceAddrs)
	switch {
	case host == "":
		fmt.Fprintf(w, "phone pairing: server is loopback-only (%s). Restart with -exposure tailnet (or lan) so a phone can reach it.\n", listen)
		return
	case !showSecret:
		fmt.Fprintf(w, "phone pairing: reachable at http://%s (exposure: %s). The pairing QR is shown only on an interactive terminal; run the server from one to scan it.\n",
			net.JoinHostPort(host, port), exposure)
		return
	}
	url := SlideURL(host, port, token)
	fmt.Fprintf(w, "\nScan with your phone to pair (exposure: %s):\n\n", exposure)
	if code, err := qr.Encode(url); err == nil {
		fmt.Fprint(w, code.Terminal())
	}
	fmt.Fprintf(w, "\n  %s\n\n", url)
	if exposure != "tailnet" {
		fmt.Fprintln(w, "  WARNING: exposure is not tailnet — anyone on this network can reach the server; the token is the only gate on /slide. Prefer -exposure tailnet.")
	}
}
