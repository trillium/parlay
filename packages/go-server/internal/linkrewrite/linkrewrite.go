// Package linkrewrite rewrites `http://localhost:<port>` / `http://127.0.0.1:<port>`
// links in served chat text to a configured reachable host, at SERVE time
// only — the durable message log is never mutated. Ported from the TS
// prototype (packages/server/src/link-rewrite.ts) for the Go server that is
// actually installed as the launchd-managed parlay-server (see
// packages/go-server/deploy/README.md).
//
// Config source: the PARLAY_PUBLIC_HOST env var.
//   - unset / empty  -> NO rewrite (identical to legacy behavior; pure opt-in)
//   - "auto"         -> resolve once from `tailscale status --json`
//     (Self.DNSName, preferring the short node name e.g. "macbook"), cached;
//     fails open to NO rewrite if tailscale is unavailable.
//   - "<host-or-ip>" -> used literally. A bare hostname ("macbook"), an FQDN
//     ("macbook.example-tailnet.ts.net"), or an IP work identically — only
//     the host swaps.
//
// Fail-open is absolute: any error in resolution or rewriting returns the
// original text unchanged. This transform must never break message serving.
package linkrewrite

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

func defaultGetenv(key string) string { return os.Getenv(key) }

// localhostLink matches `http://localhost:<port>` or `http://127.0.0.1:<port>`
// and captures the port. The host alternatives are anchored: the required
// `:` immediately after the host means `localhost.evil.com` never matches,
// and `127.0.0.1` is spelled with literal dots so no other host collides.
// Only the host is swapped; `:<port>` and everything after (path, query,
// hash) are left byte-for-byte intact because the match ends at the port
// digits.
var localhostLink = regexp.MustCompile(`http://(?:localhost|127\.0\.0\.1):(\d+)`)

var (
	once        sync.Once
	cachedHost  string // "" means no rewrite
	hasResolved bool
)

// resolvePublicHost resolves the configured host to a concrete host string,
// or "" for no-rewrite. Reads the env var and (for "auto") shells out to
// tailscale only once per process — env and the tailnet identity are
// startup-stable for a long-running server.
func resolvePublicHost() string {
	once.Do(func() {
		raw := strings.TrimSpace(getenv("PARLAY_PUBLIC_HOST"))
		if raw == "" {
			cachedHost = ""
		} else if strings.EqualFold(raw, "auto") {
			cachedHost = resolveTailscaleHost()
		} else {
			cachedHost = raw
		}
		hasResolved = true
	})
	return cachedHost
}

// getenv is a var (not a direct os.Getenv call) so tests can stub it without
// mutating real process environment across parallel tests.
var getenv = defaultGetenv

// tailscaleSelf mirrors the fields of `tailscale status --json` this package
// reads.
type tailscaleStatus struct {
	Self struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
}

// resolveTailscaleHost resolves this machine's short Tailscale node name
// (e.g. "macbook") for "auto" mode, via `tailscale status --json`'s
// Self.DNSName (e.g. "macbook.example-tailnet.ts.net."). Returns "" on any
// failure (binary missing, non-zero exit, unparseable output, empty name) so
// "auto" fails open to no-rewrite. Never panics.
func resolveTailscaleHost() string {
	out, err := runTailscaleStatus()
	if err != nil || len(out) == 0 {
		return ""
	}
	var status tailscaleStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return ""
	}
	fqdn := strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
	if fqdn == "" {
		return ""
	}
	shortName := strings.SplitN(fqdn, ".", 2)[0]
	if shortName == "" {
		return fqdn
	}
	return shortName
}

// runTailscaleStatus is a var so tests can stub the subprocess call.
var runTailscaleStatus = defaultRunTailscaleStatus

func defaultRunTailscaleStatus() ([]byte, error) {
	cmd := exec.Command("tailscale", "status", "--json")
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, exec.ErrNotFound
	}
}

// Rewrite rewrites localhost/127.0.0.1 links in text to the configured
// public host. Returns text unchanged when no host is configured, when text
// has no localhost links, or when anything goes wrong (fail-open).
func Rewrite(text string) string {
	if text == "" {
		return text
	}
	host := resolvePublicHost()
	if host == "" {
		return text
	}
	if !strings.Contains(text, "http://") {
		return text
	}
	return localhostLink.ReplaceAllString(text, "http://"+host+":$1")
}

// ResetCacheForTest clears the resolution cache so a test can flip the env
// var (via SetGetenvForTest) and re-resolve. Not part of the runtime serving
// path.
func ResetCacheForTest() {
	once = sync.Once{}
	cachedHost = ""
	hasResolved = false
}

// SetGetenvForTest overrides the env lookup used by resolvePublicHost, and
// returns a restore func. Test-only.
func SetGetenvForTest(fn func(string) string) (restore func()) {
	prev := getenv
	getenv = fn
	return func() { getenv = prev }
}

// SetTailscaleStatusForTest overrides the tailscale subprocess call used by
// "auto" mode, and returns a restore func. Test-only.
func SetTailscaleStatusForTest(fn func() ([]byte, error)) (restore func()) {
	prev := runTailscaleStatus
	runTailscaleStatus = fn
	return func() { runTailscaleStatus = prev }
}
