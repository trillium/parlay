package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// flatten collapses all newline forms to single spaces so a message is one line.
func flatten(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// validAgentID enforces the kebab-slug shape used everywhere in Parlay, and
// rejects anything that could escape the runtime dir as a path component.
func validAgentID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	prevDash := true // leading dash not allowed (prevDash starts true)
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			prevDash = false
		case ch == '-':
			if prevDash {
				return false // no leading or double dash
			}
			prevDash = true
		default:
			return false
		}
	}
	return !prevDash // no trailing dash
}

// urlValue percent-encodes a query value. Agent ids are kebab-slugs and after is
// a UUID, so this is defensive; it keeps the request well-formed regardless.
func urlValue(s string) string {
	// Minimal, allocation-light encoding for the characters that actually appear
	// (alnum, '-'). Anything unexpected is escaped so the URL never breaks.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// sleepCtx sleeps for d unless ctx is cancelled first. Returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultRuntimeDir is $TMPDIR/parlay (falls back to /tmp/parlay).
func defaultRuntimeDir() string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, "parlay")
}

func splitAgents(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sortStrings(s []string) {
	// Tiny insertion sort avoids importing sort for one call in a footprint-lean
	// binary; the registry is a handful of agents at most.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		// Response already partly written; nothing actionable but log it.
		log.Printf("control response encode failed: %v", err)
	}
}
