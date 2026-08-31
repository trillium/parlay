// Command parlay-server is the Go rewrite of packages/server, Pulse's
// HTTP/SSE chat server. C0 laid the process skeleton, mux wiring, and
// storage layer (internal/store); C1 (internal/handlers) adds messaging,
// the agent registry, and the legacy long-poll endpoint on top of it; C2
// (also internal/handlers — see events.go) adds the SSE hub behind GET
// /api/chat/events; C3 (also internal/handlers, registered separately via
// RegisterData) adds drafts, uploads, and settings. The rest of
// docs/api-contract.md remains out of scope for later tickets.
//
// Everything registered on the mux is served through internal/guard — the
// cross-origin boundary this server previously lacked entirely (task-6ai1 /
// defect D7). See that package's doc comment for the policy.
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
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"parlay/go-server/internal/bus"
	"parlay/go-server/internal/guard"
	"parlay/go-server/internal/handlers"
	"parlay/go-server/internal/static"
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
		addrFlag   = flag.String("addr", envOr("PARLAY_SERVER_ADDR", defaultAddr), "address to listen on")
		dirFlag    = flag.String("state-dir", envOr("PARLAY_STATE_HOME", defaultStateHome()), "directory for persisted state (messages/agents/drafts/settings/uploads)")
		assetsFlag = flag.String("assets-dir", envOr("PARLAY_ASSETS_DIR", defaultAssetsDir()), "directory containing the built packages/client/dist bundle (serves the panel HTML)")
		paiDirFlag = flag.String("pai-dir", envOr("PAI_DIR", defaultPAIDir()), "PAI directory for TTS cache and substitutions")

		// events-lift U1: dual-write observability events onto the Gas City
		// event bus. Default OFF — flag off must be byte-identical to a
		// build without the sink.
		busEmitFlag = flag.Bool("bus-emit", envBool("PARLAY_BUS_EMIT"), "dual-write observability SSE events onto the Gas City event bus (default off)")
		// events-lift U2: consume the bus back into the SSE hub. Also
		// default OFF; needs the city's streaming API (a running gc
		// supervisor) and degrades to backoff-retry without one.
		busConsumeFlag = flag.Bool("bus-consume", envBool("PARLAY_BUS_CONSUME"), "consume Gas City bus events into the SSE hub (default off)")
		gcBinFlag      = flag.String("gc-bin", envOr("PARLAY_GC", ""), "path to the gc binary; empty resolves from PATH (only used with -bus-emit/-bus-consume)")
		gcCityFlag     = flag.String("gc-city", envOr("PARLAY_GC_CITY", ""), "parlay-owned Gas City city root; empty means <state-dir>/gascity/city (only used with -bus-emit/-bus-consume)")
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
	hub := handlers.Register(mux, st)
	if *busEmitFlag {
		emitter, err := newBusEmitter(*gcBinFlag, *gcCityFlag, *dirFlag)
		if err != nil {
			// Loud on purpose: the flag is off by default, so whoever turned
			// it on deserves a hard error over a silently dead dual-write.
			log.Fatalf("-bus-emit enabled but unusable: %v", err)
		}
		defer emitter.Close()
		hub.SetBusSink(emitter.Emit)
	}
	if *busConsumeFlag {
		consumer, err := newBusConsumer(*gcBinFlag, *gcCityFlag, *dirFlag, hub)
		if err != nil {
			log.Fatalf("-bus-consume enabled but unusable: %v", err)
		}
		defer consumer.Close()
	}
	handlers.RegisterData(mux, st)
	handlers.RegisterTTS(mux, *paiDirFlag, hub)
	handlers.RegisterPages(mux, hub)
	handlers.RegisterPlugins(mux, hub)
	// Static file serving — registered last so /api/* routes are never shadowed.
	// Serves index.html at / and falls back to it for any unknown path (SPA).
	// /fleet/ serves the packages/webview fleet dashboard from <assets-dir>/fleet/.
	fleetDir := filepath.Join(*assetsFlag, "fleet")
	mux.Handle("/fleet/", http.StripPrefix("/fleet", static.Handler(fleetDir)))
	mux.Handle("/", static.Handler(*assetsFlag))

	// One guard in front of the whole mux (task-6ai1 / defect D7): this server
	// previously had no origin boundary at all, so a hostile page could drive
	// /send, /alert, /register-agent and /unregister with a CORS simple
	// request. Wrapping the mux rather than individual handlers means a route
	// registered later cannot land outside the boundary — see
	// internal/guard/guard.go for the policy and for where it deliberately
	// differs from packages/server/src/guard/.
	srv := &http.Server{Addr: *addrFlag, Handler: guard.Wrap(mux)}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on http://%s (state dir: %s, assets: %s)", *addrFlag, *dirFlag, *assetsFlag)
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

// resolveGC resolves the gc binary (flag/$PARLAY_GC, else PATH — the same
// order doctor's gcResolve uses) and the city root (flag/$PARLAY_GC_CITY,
// else <state-dir>/gascity/city, where `parlay city-scaffold` materialises
// parlay's own city). Shared by the emit (U1) and consume (U2) wiring.
func resolveGC(gcBin, cityPath, stateDir string) (string, string, error) {
	if gcBin == "" {
		p, err := exec.LookPath("gc")
		if err != nil {
			return "", "", errors.New("no gc binary: set -gc-bin/$PARLAY_GC or put gc on PATH (build one with tools/gc-build/build-gc.sh)")
		}
		gcBin = p
	}
	if cityPath == "" {
		cityPath = filepath.Join(stateDir, "gascity", "city")
	}
	return gcBin, cityPath, nil
}

func newBusEmitter(gcBin, cityPath, stateDir string) (*bus.Emitter, error) {
	gcBin, cityPath, err := resolveGC(gcBin, cityPath, stateDir)
	if err != nil {
		return nil, err
	}
	e, err := bus.New(bus.Config{GCBin: gcBin, CityPath: cityPath})
	if err != nil {
		return nil, err
	}
	log.Printf("bus dual-write ON (gc: %s, city: %s)", gcBin, cityPath)
	return e, nil
}

// newBusConsumer wires the bus's read side onto the hub. The cursor lives
// under the state dir next to the rest of the persisted server state.
func newBusConsumer(gcBin, cityPath, stateDir string, hub *handlers.Hub) (*bus.Consumer, error) {
	gcBin, cityPath, err := resolveGC(gcBin, cityPath, stateDir)
	if err != nil {
		return nil, err
	}
	c, err := bus.StartConsumer(bus.ConsumerConfig{
		GCBin:      gcBin,
		CityPath:   cityPath,
		CursorPath: filepath.Join(stateDir, "bus", "consumer-cursor.json"),
		Broadcast: func(name string, data json.RawMessage) bool {
			return hub.BroadcastFromBus(name, data)
		},
	})
	if err != nil {
		return nil, err
	}
	log.Printf("bus consume ON (gc: %s, city: %s)", gcBin, cityPath)
	return c, nil
}

// envBool reads a boolean env toggle: "1" or "true" (any case) is on,
// everything else — including unset — is off.
func envBool(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true")
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

// defaultAssetsDir resolves the packages/client/dist directory relative to
// the executable's own location so the server can be run from any cwd.
// Falls back to a bare "dist" (relative to cwd) if resolution fails.
func defaultAssetsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "dist"
	}
	// Walk up from <repo>/packages/go-server/bin/parlay-server to repo root,
	// then descend into packages/client/dist.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	candidate := filepath.Join(repoRoot, "packages", "client", "dist")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "dist"
}

// defaultPAIDir returns the default PAI directory for TTS cache and substitutions.
func defaultPAIDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude", "PAI")
	}
	return ".claude/PAI"
}
