package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Per-caller identity (task-9nldh) ─────────────────────────────────────────
// The control socket used to let any local process register ANY agent id. Now
// the first /register for an id mints an owner token, and only that token's
// holder may re-register or unregister the live channel.

// controlServer spins the real controlMux over HTTP for control-plane tests.
// The relay's upstream is a fake that idles (no scripts), so poll loops park
// harmlessly; every test unregisters what it registers.
func controlServer(t *testing.T, r *relay) (base string, cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(r.controlMux())
	return srv.URL, srv.Close
}

// postControl POSTs a JSON body to a control path and returns status + decoded body.
func postControl(t *testing.T, base, path string, body map[string]any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("POST %s: non-JSON response %q", path, data)
	}
	return resp.StatusCode, out
}

// getAudit fetches GET /audit and returns the entry list.
func getAudit(t *testing.T, base, query string) []map[string]any {
	t.Helper()
	resp, err := http.Get(base + "/audit" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET /audit: %v", err)
	}
	return out.Entries
}

// newIdentityRelay is a relay against a temp dir with an idling upstream.
func newIdentityRelay(t *testing.T) (*relay, string) {
	t.Helper()
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	r := &relay{
		server:     strings.TrimRight(srv.URL, "/"),
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	return r, dir
}

// TestRegisterMintsOwnerToken: the first /register for an id returns a fresh
// owner token, and a re-register presenting it is idempotent (same spool).
func TestRegisterMintsOwnerToken(t *testing.T) {
	r, _ := newIdentityRelay(t)
	base, cleanup := controlServer(t, r)
	defer cleanup()

	code, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil)
	if code != http.StatusOK {
		t.Fatalf("first register: status=%d body=%v", code, out)
	}
	token, _ := out["token"].(string)
	if len(token) != 64 {
		t.Fatalf("first register must mint a 64-hex owner token, got %v", out["token"])
	}
	spool, _ := out["spool"].(string)
	if spool == "" {
		t.Fatal("first register returned no spool")
	}
	defer postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": token}, nil)

	// Same caller, same token: idempotent, same spool, no token re-issue.
	code, out2 := postControl(t, base, "/register", map[string]any{"agent": "voice-a", "token": token}, nil)
	if code != http.StatusOK {
		t.Fatalf("re-register with token: status=%d body=%v", code, out2)
	}
	if out2["spool"] != spool {
		t.Errorf("re-register spool = %v, want %v", out2["spool"], spool)
	}
	if _, ok := out2["token"]; ok {
		t.Errorf("verified re-register must not re-issue a token")
	}
}

// TestRegisterTakeoverIsDenied: once owned, a re-register without the token
// (or with the wrong one) is 409 — a stranger can no longer silently claim a
// live channel. The denial is audit-logged.
func TestRegisterTakeoverIsDenied(t *testing.T) {
	r, dir := newIdentityRelay(t)
	base, cleanup := controlServer(t, r)
	defer cleanup()

	_, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil)
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("first register minted no token")
	}
	defer postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": token}, nil)

	for name, body := range map[string]map[string]any{
		"no token":    {"agent": "voice-a"},
		"wrong token": {"agent": "voice-a", "token": strings.Repeat("0", 64)},
	} {
		if code, out := postControl(t, base, "/register", body, nil); code != http.StatusConflict {
			t.Errorf("%s: status=%d body=%v, want 409", name, code, out)
		}
	}

	// The two denials are in the audit trail as register-denied for voice-a.
	denied := 0
	for _, e := range getAudit(t, base, "") {
		if e["action"] == auditRegisterDenied && e["agent"] == "voice-a" {
			denied++
			for _, k := range []string{"ts", "actor", "action", "agent"} {
				if e[k] == nil || e[k] == "" {
					t.Errorf("audit entry missing %q: %v", k, e)
				}
			}
		}
	}
	if denied != 2 {
		t.Errorf("register-denied entries = %d, want 2", denied)
	}

	// The raw owner token must never appear in the audit file.
	if data, err := os.ReadFile(filepath.Join(dir, auditFileName)); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(data), token) {
		t.Error("audit.log contains the raw owner token")
	}
}

// TestBearerHeaderAuthenticates: Authorization: Bearer works exactly like the
// {"token":...} body field.
func TestBearerHeaderAuthenticates(t *testing.T) {
	r, _ := newIdentityRelay(t)
	base, cleanup := controlServer(t, r)
	defer cleanup()

	_, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil)
	token, _ := out["token"].(string)
	defer postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": token}, nil)

	h := map[string]string{"Authorization": "Bearer " + token}
	if code, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, h); code != http.StatusOK {
		t.Errorf("bearer re-register: status=%d body=%v, want 200", code, out)
	}
	if code, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, h); code == http.StatusOK {
		_ = out
	}
	// Wrong bearer is still a conflict.
	hBad := map[string]string{"Authorization": "Bearer " + strings.Repeat("f", 64)}
	if code, _ := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, hBad); code != http.StatusConflict {
		t.Errorf("wrong bearer: status=%d, want 409", code)
	}
}

// TestUnregisterRequiresOwner: retiring a live channel needs its token (403
// otherwise, audit-logged); unknown ids stay token-free (found:false); a
// successful unregister releases ownership so the id is claimable again.
func TestUnregisterRequiresOwner(t *testing.T) {
	r, _ := newIdentityRelay(t)
	base, cleanup := controlServer(t, r)
	defer cleanup()

	_, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil)
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("first register minted no token")
	}

	// Stranger cannot retire the channel.
	if code, out := postControl(t, base, "/unregister", map[string]any{"agent": "voice-a"}, nil); code != http.StatusForbidden {
		t.Errorf("unregister without token: status=%d body=%v, want 403", code, out)
	}
	if code, _ := postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": strings.Repeat("1", 64)}, nil); code != http.StatusForbidden {
		t.Errorf("unregister with wrong token: status=%d, want 403", code)
	}

	// Unknown id: still the idempotent no-op, no token needed.
	if code, out := postControl(t, base, "/unregister", map[string]any{"agent": "never-here"}, nil); code != http.StatusOK || out["found"] != false {
		t.Errorf("unregister unknown: status=%d body=%v, want 200 found:false", code, out)
	}

	// Owner retires it.
	if code, out := postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": token}, nil); code != http.StatusOK || out["found"] != true {
		t.Errorf("owner unregister: status=%d body=%v, want 200 found:true", code, out)
	}

	// Ownership released: a fresh token-less claim mints again.
	code, out2 := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil)
	if code != http.StatusOK {
		t.Fatalf("re-claim after unregister: status=%d body=%v", code, out2)
	}
	token2, _ := out2["token"].(string)
	if token2 == "" || token2 == token {
		t.Errorf("re-claim must mint a fresh token, got %q (old %q)", token2, token)
	}
	defer postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": token2}, nil)

	// Denial + success both audited.
	seen := map[string]bool{}
	for _, e := range getAudit(t, base, "") {
		if e["agent"] == "voice-a" {
			seen[e["action"].(string)] = true
		}
	}
	for _, want := range []string{auditRegister, auditUnregisterDenied, auditUnregister} {
		if !seen[want] {
			t.Errorf("audit trail missing %q (have %v)", want, seen)
		}
	}
}

// TestOwnershipSurvivesRestart: owners.json (0600) carries the binding across
// relay restarts sharing a runtime dir — a stranger's token-less claim after
// a restart is still 409, the owner's token still verifies.
func TestOwnershipSurvivesRestart(t *testing.T) {
	r1, dir := newIdentityRelay(t)
	base1, cleanup1 := controlServer(t, r1)
	_, out := postControl(t, base1, "/register", map[string]any{"agent": "voice-a"}, nil)
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("first register minted no token")
	}
	cleanup1()
	// Stop r1's loop without unregistering (which would release ownership):
	// the restart scenario is a crash/redeploy, not a retirement.
	r1.mu.Lock()
	for _, l := range r1.loops {
		l.cancel()
	}
	r1.mu.Unlock()

	if fi, err := os.Stat(filepath.Join(dir, ownersFileName)); err != nil {
		t.Fatalf("owners.json missing: %v", err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("owners.json mode = %o, want 600", fi.Mode().Perm())
	}

	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()
	r2 := &relay{
		server:     strings.TrimRight(srv.URL, "/"),
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	r2.loadOwners()
	base2, cleanup2 := controlServer(t, r2)
	defer cleanup2()

	if code, _ := postControl(t, base2, "/register", map[string]any{"agent": "voice-a"}, nil); code != http.StatusConflict {
		t.Errorf("post-restart stranger claim: status=%d, want 409", code)
	}
	code, _ := postControl(t, base2, "/register", map[string]any{"agent": "voice-a", "token": token}, nil)
	if code != http.StatusOK {
		t.Errorf("post-restart owner claim: status=%d, want 200", code)
	}
	defer postControl(t, base2, "/unregister", map[string]any{"agent": "voice-a", "token": token}, nil)
}

// TestToken Primitives: mint is 256-bit hex, hashes verify, fingerprints
// never leak the raw token.
func TestOwnerTokenPrimitives(t *testing.T) {
	raw, hash, err := mintOwnerToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 64 {
		t.Errorf("raw token len = %d, want 64", len(raw))
	}
	if ownerHash(raw) != hash {
		t.Error("ownerHash(raw) != stored hash")
	}
	fp := tokenFingerprint(hash)
	if len(fp) != 8 || !strings.HasPrefix(hash, fp) {
		t.Errorf("fingerprint = %q, want first 8 of hash", fp)
	}
	if strings.Contains(fp, raw) || fp == raw {
		t.Error("fingerprint leaks the raw token")
	}
	if tokenFingerprint("") != "none" {
		t.Errorf("empty fingerprint = %q, want none", tokenFingerprint(""))
	}
	raw2, _, err := mintOwnerToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == raw2 {
		t.Error("two mints produced the same token")
	}
}

// TestAuditReadback: GET /audit tails the trail (who/what/target/when on
// every line), ?limit=N bounds it, and an empty runtime dir is [] not 404.
func TestAuditReadback(t *testing.T) {
	r, _ := newIdentityRelay(t)
	base, cleanup := controlServer(t, r)
	defer cleanup()

	if entries := getAudit(t, base, ""); len(entries) != 0 {
		t.Fatalf("fresh relay audit = %d entries, want 0", len(entries))
	}

	_, out := postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil)
	token, _ := out["token"].(string)
	defer postControl(t, base, "/unregister", map[string]any{"agent": "voice-a", "token": token}, nil)
	postControl(t, base, "/register", map[string]any{"agent": "voice-a"}, nil) // denied

	entries := getAudit(t, base, "")
	if len(entries) < 2 {
		t.Fatalf("audit entries = %d, want >= 2 (register + denied)", len(entries))
	}
	first := entries[0]
	if first["action"] != auditRegister || first["agent"] != "voice-a" {
		t.Errorf("first entry = %v, want register/voice-a", first)
	}
	if first["actor"] == nil || first["actor"] == "" {
		t.Errorf("entry missing actor (who): %v", first)
	}
	if first["ts"] == nil || first["ts"] == "" {
		t.Errorf("entry missing ts (when): %v", first)
	}
	last := entries[len(entries)-1]
	if last["action"] != auditRegisterDenied {
		t.Errorf("last entry = %v, want register-denied", last)
	}

	// ?limit=1 returns exactly the tail.
	if got := getAudit(t, base, "?limit=1"); len(got) != 1 {
		t.Errorf("?limit=1 returned %d entries, want 1", len(got))
	} else if fmt.Sprint(got[0]) != fmt.Sprint(last) {
		t.Errorf("?limit=1 = %v, want tail %v", got[0], last)
	}
}
