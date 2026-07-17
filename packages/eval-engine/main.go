package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// ── parlay-eval-engine: the compiled string-evaluation service ─────────────────
//
// A standalone Go HTTP service that owns the ENTIRE input evaluation for Parlay's
// chat box in the pure server-side model. The TS Pulse server delegates
// POST /api/chat/eval to this service; this service computes actions with
// compiled RE2 regexes and returns them. SSE delivery stays in the TS server
// (this service pushes server-owned submit fires back to it via a callback URL).
//
// WHY GO, NOT TYPESCRIPT (the captain's core requirement):
//   Moving evaluation into the TS/bun server would still be interpreted JS — no
//   compiled-speed win, plus bun's ~40MB runtime. This service is a single static
//   Go binary using only the stdlib (net/http, regexp/RE2, encoding/json). RE2 is
//   compiled, linear-time regex matching — exactly the primitive the registry
//   needs. Build is one `go build`, no network dependency resolution, trivially
//   reversible.
//
// WHY GO, NOT RUST (documented trade-off):
//   Rust would edge out Go on raw regex throughput, but the captain named "Go for
//   dev ease" and this is a reversible experiment. Go gives a single static binary
//   with zero external crates (no Cargo dependency-fetch failure mode), a
//   near-instant build, and stdlib RE2 that covers the whole need. For an on/off
//   experiment the reversibility + zero-deps win dominates the marginal throughput
//   gap. The eval loop is small and pure; if raw speed ever becomes the binding
//   constraint, the same matcher.go/commands.go logic ports to Rust unchanged.

// PushClient posts server-owned submit fires to the TS relay's push endpoint.
type PushClient struct {
	url  string
	http *http.Client
}

func (p *PushClient) pushSubmit(streamID string, seq, base int64, tail, text string) {
	if p.url == "" {
		log.Printf("[submit-fire] stream=%s seq=%d base=%d tail=%q — NO PUSH URL (dropped)", streamID, seq, base, tail)
		return
	}
	body, _ := json.Marshal(map[string]any{
		"streamId":    streamID,
		"seq":         seq,
		"baseVersion": base,
		"v":           ProtocolVersion,
		"action": map[string]any{
			"verb": "submitNow",
			"args": map[string]any{"requireTail": tail, "text": text},
		},
	})
	req, err := http.NewRequest(http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[submit-fire] build request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		log.Printf("[submit-fire] push failed stream=%s: %v", streamID, err)
		return
	}
	resp.Body.Close()
	log.Printf("[submit-fire] pushed stream=%s seq=%d base=%d tail=%q status=%d", streamID, seq, base, tail, resp.StatusCode)
}

func main() {
	addr := envOr("PARLAY_EVAL_ADDR", "127.0.0.1:4343")
	pushURL := envOr("PARLAY_EVAL_PUSH_URL", "http://127.0.0.1:31337/api/chat/eval-push")

	push := &PushClient{url: pushURL, http: &http.Client{Timeout: 3 * time.Second}}
	engine := NewEngine()
	engine.onSubmit = func(streamID string, seq, base int64, tail, text string) {
		push.pushSubmit(streamID, seq, base, tail, text)
	}

	// File layer: load a manifest from PARLAY_COMMANDS (or a commands.json next to
	// the binary) over the embedded default, and fs-watch it for fail-closed
	// hot-reload. No configured file ⇒ the embedded default is the whole story.
	if path := installFileSource(engine, nil); path != "" {
		log.Printf("  manifest source: %s (fs-watched, fail-closed)", path)
	} else {
		log.Printf("  manifest source: embedded default")
	}

	mux := http.NewServeMux()

	// POST /eval — the hot path. Body: EvalRequest. Returns EvalResponse.
	mux.HandleFunc("/eval", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req EvalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.StreamID == "" {
			http.Error(w, "streamId required", http.StatusBadRequest)
			return
		}
		resp := engine.Eval(req)
		w.Header().Set("Content-Type", "application/json")
		// Expose the compiled eval time as a header too, so a curl/Interceptor
		// probe sees it without parsing the body.
		w.Header().Set("X-Engine-Eval-Ns", itoa(resp.EngineEvalNs))
		json.NewEncoder(w).Encode(resp)
	})

	// GET /stats — the observable cost surface.
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(engine.stats.snapshot())
	})

	// GET /commands — the registered command table (debug/observe).
	mux.HandleFunc("/commands", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(engine.describeCommands())
	})

	// GET /health — liveness.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "protocol": ProtocolVersion})
	})

	log.Printf("parlay-eval-engine (compiled Go) listening on http://%s", addr)
	log.Printf("  push URL: %s", pushURL)
	log.Printf("  commands: %d compiled", len(engine.commands))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen failed: %v", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// itoa avoids importing strconv twice; keeps main.go self-contained. It works in
// the NEGATIVE domain so math.MinInt64 is representable — negating it into the
// positive domain overflows back to MinInt64 and drops every digit.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	// Fold positive inputs into the negative domain, then emit digits from there.
	if !neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n < 0 {
		i--
		buf[i] = byte('0' - n%10) // -(n%10) is the digit; n is ≤ 0 here
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
