package pair

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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

func TestLoadOrCreateGeneratesPersistsAndLocksDown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 43 {
		t.Fatalf("token too short: %d chars", len(a))
	}
	info, err := os.Stat(filepath.Join(dir, TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	b, _ := LoadOrCreate(dir)
	if a != b {
		t.Fatal("token must be stable across restarts")
	}
}

func TestLoadOrCreateReplacesJunkAndTightensMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, TokenFile)
	os.WriteFile(p, []byte("short\n"), 0o644)
	tok, _ := LoadOrCreate(dir)
	if tok == "short" || len(tok) < minTokenLen {
		t.Fatalf("junk token kept: %q", tok)
	}
	good := strings.Repeat("g", 30)
	os.WriteFile(p, []byte(good), 0o644)
	got, _ := LoadOrCreate(dir)
	if got != good {
		t.Fatalf("valid token not reused: %q", got)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode not tightened: %v", info.Mode().Perm())
	}
}

func TestReachableHost(t *testing.T) {
	both := ifaces("127.0.0.1", "192.168.1.20", "100.101.102.103")
	cases := []struct{ exposure, listen, want string }{
		{"loopback", "127.0.0.1:4242", ""},
		{"tailnet", "100.64.0.9:4242", "100.64.0.9"},
		{"lan", "0.0.0.0:4242", "100.101.102.103"},
		{"custom", "10.0.0.5:4242", "10.0.0.5"},
	}
	for _, c := range cases {
		if got := ReachableHost(c.exposure, c.listen, both); got != c.want {
			t.Errorf("%s %s: got %q want %q", c.exposure, c.listen, got, c.want)
		}
	}
	if got := ReachableHost("lan", "0.0.0.0:4242", ifaces("127.0.0.1", "8.8.8.8", "192.168.1.20")); got != "192.168.1.20" {
		t.Errorf("lan-only: got %q", got)
	}
	if got := ReachableHost("lan", "0.0.0.0:4242", ifaces("127.0.0.1")); got != "" {
		t.Errorf("no reachable addr: got %q", got)
	}
}

func TestSlideURLKeepsTokenInFragment(t *testing.T) {
	u := SlideURL("100.1.2.3", "4242", "tok")
	if u != "http://100.1.2.3:4242/slide#t=tok" {
		t.Fatal(u)
	}
	if strings.Contains(strings.SplitN(u, "#", 2)[0], "tok") {
		t.Fatal("token leaked outside the fragment")
	}
}

func TestBannerNeverPrintsTokenOffTerminal(t *testing.T) {
	const tok = "SECRETTOKENSECRETTOKEN"
	var buf bytes.Buffer
	Banner(&buf, "tailnet", "100.101.102.103:4242", tok, false, ifaces())
	if strings.Contains(buf.String(), tok) || !strings.Contains(buf.String(), "100.101.102.103:4242") {
		t.Fatalf("non-TTY banner wrong:\n%s", buf.String())
	}
	buf.Reset()
	Banner(&buf, "loopback", "127.0.0.1:4242", tok, true, ifaces())
	if strings.Contains(buf.String(), tok) || !strings.Contains(buf.String(), "-exposure tailnet") {
		t.Fatalf("loopback banner wrong:\n%s", buf.String())
	}
	buf.Reset()
	Banner(&buf, "tailnet", "100.101.102.103:4242", tok, true, ifaces())
	out := buf.String()
	if !strings.Contains(out, "http://100.101.102.103:4242/slide#t="+tok) || !strings.Contains(out, "\x1b[47;30m") {
		t.Fatalf("TTY banner missing URL/QR:\n%s", out)
	}
	if strings.Contains(out, "WARNING") {
		t.Fatal("tailnet should not warn")
	}
	buf.Reset()
	Banner(&buf, "lan", "0.0.0.0:4242", tok, true, ifaces("192.168.1.20"))
	if !strings.Contains(buf.String(), "WARNING") {
		t.Fatal("lan pairing must warn")
	}
}
