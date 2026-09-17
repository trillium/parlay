package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Per-caller identity for the relay control socket (phase-2 voice identity,
// task-9nldh).
//
// The socket is filesystem-authenticated only: any local process that can
// write to it could register ANY agent id, hijacking another agent's channel
// (re-register is idempotent, so a hostile re-register is silent). There is
// no socket-credential primitive in the stdlib (SO_PEERCRED is Linux-only via
// x/sys, getpeereid is uid-only on darwin), and this binary is deliberately
// dependency-free — so identity is a bearer owner token instead:
//
//   - The first /register for an agent mints a random owner token and returns
//     it in the response ("token"). Only its SHA-256 is kept server-side.
//   - Re-registering or unregistering that agent requires the same token
//     (Authorization: Bearer header or {"token":...} body field). A wrong or
//     missing token is 409/403 and is audit-logged, so a takeover attempt is
//     loud, not silent.
//   - Ownership survives relay restarts via owners.json (0600) in the runtime
//     dir; a successful /unregister releases it, so a retired id is
//     claimable again. Internal registrations (startup -agents,
//     resumeFromSpools) never mint or check ownership — the relay itself is
//     not a caller.
//
// New ids stay claimable by any local process: the socket file is the trust
// boundary for enrollment, the token is the boundary against impersonating a
// LIVE channel. The legitimate owner persists its token next to the spool
// (parlay-monitor.sh saves <runtime>/<agent>.token) and replays it on every
// enroll.

// ownersFileName is the persisted agent→owner-token-hash map (0600).
const ownersFileName = "owners.json"

// claimResult is what claimOwner decided for a /register caller.
type claimResult int

const (
	// claimConflict: the agent is owned by a different caller. Deny (409).
	claimConflict claimResult = iota
	// claimVerified: the caller presented the owner token. Proceed.
	claimVerified
	// claimMinted: nobody owned the agent; newToken holds the raw owner
	// token the caller must persist. Proceed.
	claimMinted
)

// mintOwnerToken generates a random 256-bit owner token. It returns the raw
// token (handed to the caller exactly once) and its SHA-256 hex (the only
// form ever stored or compared).
func mintOwnerToken() (raw, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

// ownerHash hashes a presented token for comparison against stored owners.
func ownerHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// tokenFingerprint is the log/audit-safe form of an owner: the first 8 hex
// chars of its hash. Enough to correlate a caller's lines, useless for
// forging. The raw token is NEVER logged.
func tokenFingerprint(hash string) string {
	if len(hash) < 8 {
		return "none"
	}
	return hash[:8]
}

// bearerToken extracts the caller's owner token: an
// "Authorization: Bearer <token>" header wins, falling back to a
// {"token":...} field in an already-decoded JSON body.
func bearerToken(header, bodyToken string) string {
	if h := strings.TrimSpace(header); h != "" {
		if rest, ok := strings.CutPrefix(h, "Bearer "); ok {
			if t := strings.TrimSpace(rest); t != "" {
				return t
			}
		}
	}
	return strings.TrimSpace(bodyToken)
}

// ownersPath is the persisted ownership map in the relay's runtime dir.
func (r *relay) ownersPath() string {
	return filepath.Join(r.runtimeDir, ownersFileName)
}

// loadOwners reads the persisted ownership map. Best-effort: a missing file
// is a fresh relay, and a corrupt one starts unowned (logged) rather than
// refusing to start — ownership is a hardening layer, not the registry.
func (r *relay) loadOwners() {
	data, err := os.ReadFile(r.ownersPath())
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("owners: read %s: %v — starting unowned", r.ownersPath(), err)
		}
		return
	}
	var owners map[string]string
	if err := json.Unmarshal(data, &owners); err != nil {
		log.Printf("owners: parse %s: %v — starting unowned", r.ownersPath(), err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners == nil {
		r.owners = make(map[string]string)
	}
	for agent, hash := range owners {
		if validAgentID(agent) && hash != "" {
			r.owners[agent] = hash
		}
	}
}

// saveOwnersLocked persists the ownership map (0600: hashes, not raw
// tokens, but still no need for world-read). Caller must hold r.mu.
// Best-effort: a failed write is logged, the in-memory map stays canonical.
func (r *relay) saveOwnersLocked() {
	data, err := json.Marshal(r.owners)
	if err != nil {
		log.Printf("owners: marshal: %v", err)
		return
	}
	if err := os.WriteFile(r.ownersPath(), data, 0o600); err != nil {
		log.Printf("owners: write %s: %v", r.ownersPath(), err)
	}
}

// claimOwner binds agent to the calling token for a /register request.
// Unknown agents mint a fresh owner (returned raw, to hand the caller once).
// A known agent requires its owner token; anything else is a conflict.
func (r *relay) claimOwner(agent, token string) (claimResult, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners == nil {
		r.owners = make(map[string]string)
	}
	owner, owned := r.owners[agent]
	if !owned {
		raw, hash, err := mintOwnerToken()
		if err != nil {
			return claimConflict, "", err
		}
		r.owners[agent] = hash
		r.saveOwnersLocked()
		return claimMinted, raw, nil
	}
	if token != "" && ownerHash(token) == owner {
		return claimVerified, "", nil
	}
	return claimConflict, "", nil
}

// checkOwner reports whether token owns agent. Unknown agents are unowned
// (true): there is nothing to protect yet.
func (r *relay) checkOwner(agent, token string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, owned := r.owners[agent]
	if !owned {
		return true
	}
	return token != "" && ownerHash(token) == owner
}

// releaseOwner drops agent's ownership after a successful /unregister, so a
// retired id is claimable again on its next /register.
func (r *relay) releaseOwner(agent string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.owners[agent]; ok {
		delete(r.owners, agent)
		r.saveOwnersLocked()
	}
}
