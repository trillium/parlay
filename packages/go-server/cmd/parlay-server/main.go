// Command parlay-server is the Go rewrite of packages/server, Pulse's
// HTTP/SSE chat server. C0 laid the process skeleton, mux wiring, and
// storage layer (internal/store); C1 (internal/handlers) adds messaging,
// the agent registry, and the legacy long-poll endpoint on top of it. SSE,
// drafts, settings, uploads, and the rest of docs/api-contract.md remain
// out of scope for later tickets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"parlay/go-server/internal/handlers"
	"parlay/go-server/internal/store"
)

// defaultAddr matches packages/cli's own coded fallback
// (packages/cli/src/config.ts: PARLAY_SERVER env > persisted config >
// http://localhost:4242) — deliberately NOT port 31337, the captain's live
// production Pulse instance (see this repo's CLAUDE.md): a server built for
// real-world testing must never default to, or accidentally bind, :31337.
const defaultAddr = "127.0.0.1:4242"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("parlay-server: ")

	var (
		addrFlag = flag.String("addr", envOr("PARLAY_SERVER_ADDR", defaultAddr), "address to listen on")
		dirFlag  = flag.String("state-dir", envOr("PARLAY_STATE_HOME", defaultStateHome()), "directory for persisted state (messages/agents/drafts/settings)")
	)
	flag.Parse()

	if err := refuseProductionPort(*addrFlag); err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(store.Config{Dir: *dirFlag})
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Messages.Close()

	mux := http.NewServeMux()
	registerHealth(mux, st)
	handlers.Register(mux, st)

	srv := &http.Server{Addr: *addrFlag, Handler: mux}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on http://%s (state dir: %s)", *addrFlag, *dirFlag)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Printf("received %s — shutting down", s)
	case err := <-serveErr:
		if err != nil {
			log.Fatalf("listen failed: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Print("stopped")
}

// registerHealth wires GET /health, the one route this ticket owns. It
// reports enough store state to be a useful liveness+sanity check for later
// tickets to build on top of.
func registerHealth(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"messages": st.Messages.Count(),
			"agents":   len(st.Registry.List()),
		})
	})
}

// refuseProductionPort is a hard stop against ever binding :31337 from this
// binary — that port is the captain's live, currently-connected Pulse
// server, not something a dev/test run of this rewrite may touch (see
// this repo's CLAUDE.md).
func refuseProductionPort(addr string) error {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err == nil && port == 31337 {
		return errors.New("refusing to bind :31337 — that is the captain's live production Pulse server (see this repo's CLAUDE.md)")
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultStateHome mirrors packages/server/src/debug-log.ts's own default:
// PARLAY_STATE_HOME, falling back to ~/.parlay.
func defaultStateHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".parlay")
	}
	return ".parlay"
}
