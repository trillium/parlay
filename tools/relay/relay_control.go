package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
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
		agent, token, err := decodeAgentBody(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// Per-caller identity: a live channel is bound to the token that
		// claimed it. A re-register without (or with the wrong) token is a
		// takeover attempt — 409 plus an audit line, never a silent
		// re-registration under a stranger.
		result, newToken, err := r.claimOwner(agent, token)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cannot mint owner token"})
			return
		}
		if result == claimConflict {
			r.audit(auditRegisterDenied, callerFP(token), agent)
			writeJSON(w, http.StatusConflict, map[string]any{"error": "agent " + strconv.Quote(agent) + " is owned by another caller (present its owner token)"})
			return
		}
		spool, err := r.register(agent)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		resp := map[string]any{"ok": true, "agent": agent, "spool": spool}
		if result == claimMinted {
			// Handed out exactly once: persist it as
			// <runtime>/<agent>.token and replay it on every enroll.
			resp["token"] = newToken
		}
		r.audit(auditRegister, callerFP(effectiveToken(token, newToken)), agent)
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/unregister", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		agent, token, err := decodeAgentBody(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// Only the owning caller may retire a live channel. Unknown ids
		// stay token-free (idempotent no-op, found:false) — there is
		// nothing to protect yet.
		if !r.checkOwner(agent, token) {
			r.audit(auditUnregisterDenied, callerFP(token), agent)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "agent " + strconv.Quote(agent) + " is owned by another caller (present its owner token)"})
			return
		}
		found := r.unregister(agent)
		if found {
			r.releaseOwner(agent)
		}
		r.audit(auditUnregister, callerFP(token), agent)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agent": agent, "found": found})
	})

	mux.HandleFunc("/audit", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": r.readAudit(auditLimit(req.URL.Query().Get("limit")))})
	})

	return mux
}

// callerFP is the audit-safe form of a presented raw token: fingerprint its
// hash, or "none" when the caller sent nothing.
func callerFP(rawToken string) string {
	if strings.TrimSpace(rawToken) == "" {
		return "none"
	}
	return tokenFingerprint(ownerHash(rawToken))
}

// effectiveToken resolves which raw token identifies a /register caller for
// the audit line: the presented one, or the just-minted one on first claim.
func effectiveToken(presented, minted string) string {
	if strings.TrimSpace(presented) != "" {
		return presented
	}
	return minted
}

// decodeAgentBody extracts and validates the {"agent":"<id>"} field, plus the
// caller's optional owner token — an "Authorization: Bearer <token>" header
// first, then a {"token":...} body field. Returns agent, token ("" when the
// caller sent none), error.
func decodeAgentBody(req *http.Request) (string, string, error) {
	var body struct {
		Agent string `json:"agent"`
		Token string `json:"token"`
	}
	dec := json.NewDecoder(io.LimitReader(req.Body, 4096))
	if err := dec.Decode(&body); err != nil {
		return "", "", fmt.Errorf("bad JSON body: %w", err)
	}
	agent := strings.TrimSpace(body.Agent)
	if agent == "" {
		return "", "", errors.New("agent id is required")
	}
	if !validAgentID(agent) {
		return "", "", fmt.Errorf("invalid agent id %q (want kebab-slug)", agent)
	}
	return agent, bearerToken(req.Header.Get("Authorization"), body.Token), nil
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
