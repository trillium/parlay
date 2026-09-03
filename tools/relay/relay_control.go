package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// controlMux is the HTTP handler served over the Unix control socket.
func (r *relay) controlMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/agents", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"agents":  r.agentIDs(),
			"server":  r.server,
			"runtime": r.runtimeDir,
		})
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		agent, err := decodeAgentBody(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		spool, err := r.register(agent)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agent": agent, "spool": spool})
	})

	mux.HandleFunc("/unregister", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		agent, err := decodeAgentBody(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		found := r.unregister(agent)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agent": agent, "found": found})
	})

	return mux
}

// decodeAgentBody extracts and validates the {"agent":"<id>"} field.
func decodeAgentBody(req *http.Request) (string, error) {
	var body struct {
		Agent string `json:"agent"`
	}
	dec := json.NewDecoder(io.LimitReader(req.Body, 4096))
	if err := dec.Decode(&body); err != nil {
		return "", fmt.Errorf("bad JSON body: %w", err)
	}
	agent := strings.TrimSpace(body.Agent)
	if agent == "" {
		return "", errors.New("agent id is required")
	}
	if !validAgentID(agent) {
		return "", fmt.Errorf("invalid agent id %q (want kebab-slug)", agent)
	}
	return agent, nil
}

// listenControl binds the Unix domain control socket, removing any stale socket
// left by a previous crashed relay first.
func listenControl(path string) (net.Listener, error) {
	// A leftover socket file from an unclean exit would make Listen fail with
	// EADDRINUSE even though nothing is listening; remove it first. If a live
	// relay is already bound, the subsequent Listen still fails and we surface it.
	if _, err := os.Stat(path); err == nil {
		if probeAlive(path) {
			return nil, fmt.Errorf("another relay is already listening on %s", path)
		}
		_ = os.Remove(path)
	}
	return net.Listen("unix", path)
}

// probeAlive returns true if something is already accepting on the Unix socket.
func probeAlive(path string) bool {
	c, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
