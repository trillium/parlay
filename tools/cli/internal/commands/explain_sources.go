// Helper seams for parlay explain: timestamp parsing, newest-first ordering of
// command records, and the server-URL comparison the relay preflight uses.
// Kept beside (not inside) explain.go so each file stays narrow enough to read
// in one screen.
package commands

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// sortCommandsNewestFirst orders records by start time, newest first. A record
// whose timestamp does not parse sorts last rather than being dropped: it is
// still evidence that the commands registry answered.
func sortCommandsNewestFirst(list []wire.CommandInvocation) {
	at := func(c wire.CommandInvocation) (time.Time, bool) {
		if t, ok := parseStamp(c.StartedAt); ok {
			return t, true
		}
		return parseStamp(c.UpdatedAt)
	}
	sort.SliceStable(list, func(i, j int) bool {
		ti, oki := at(list[i])
		tj, okj := at(list[j])
		if !oki || !okj {
			return oki && !okj
		}
		return ti.After(tj)
	})
}

// parseStamp parses one server timestamp. Both RFC3339 and RFC3339Nano are
// accepted because the server encodes with the standard library's time.Time
// marshaller, whose precision depends on the value.
func parseStamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// sameServerURL compares two server URLs the way the relay preflight does
// (tools/monitor/parlay-monitor.sh): host equality with localhost ≡ 127.0.0.1,
// the scheme's default port implied, and a trailing slash ignored. comparable
// is false when either side does not parse — an unanswerable question must not
// become a mismatch claim, which is the same rule the preflight states.
func sameServerURL(a, b string) (equal, comparable bool) {
	na, oka := normServerURL(a)
	nb, okb := normServerURL(b)
	if !oka || !okb {
		return false, false
	}
	return na == nb, true
}

func normServerURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + host + ":" + port, true
}
