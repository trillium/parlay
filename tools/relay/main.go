// Command relay is the single central fan-out for Parlay agent channels.
//
// Instead of N independent bun poll loops (one ~40MB process per agent), ONE
// relay process holds one upstream long-poll loop per registered agent against
// the Pulse chat server, and appends each inbound user message to that agent's
// private spool file as a CHAT_MSG line. Each agent's `parlay monitor` then just
// tails its spool file — a ~1.2MB `tail -F`, not a 40MB poller.
//
// Wire contract (see tools/RELAY_MONITOR.md):
//
//	Upstream poll : GET  {server}/api/chat/poll?after=<lastId>&channel=<agent>
//	                → {"timeout":true}  or  {"id","role","text","ts",...}
//	Spool line    : CHAT_MSG|<id>|<role>|<text>\n               (captain messages, no attribution)
//	              : CHAT_MSG|<id>|<role>|<text>|from:<sender>\n (agent→agent messages, 5th field)
//	Spool path    : {runtime-dir}/<agent>.chan       (runtime-dir defaults to $TMPDIR/parlay)
//	Control socket : Unix domain socket at {runtime-dir}/relay.sock
//	  POST /register {"agent":"<id>"}     → {"ok":true,"agent":"<id>","spool":"<path>"}   (idempotent)
//	  POST /unregister {"agent":"<id>"}   → {"ok":true}
//	  GET  /agents                        → {"agents":[...],"server":"...","runtime":"..."}
//	  GET  /health                        → {"ok":true}
//
// Exit codes: 0 clean shutdown (SIGINT/SIGTERM), 1 fatal startup error.
package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("relay: ")

	var (
		serverFlag  = flag.String("server", envOr("PARLAY_SERVER", defaultServer), "Pulse chat server base URL")
		runtimeFlag = flag.String("runtime-dir", defaultRuntimeDir(), "directory for spool files and the control socket")
		agentsFlag  = flag.String("agents", "", "comma-separated agent ids to register at startup (optional)")
	)
	flag.Parse()

	server := strings.TrimRight(*serverFlag, "/")
	if server == "" {
		log.Fatal("server URL is empty")
	}
	runtimeDir := *runtimeFlag
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		log.Fatalf("cannot create runtime dir %s: %v", runtimeDir, err)
	}

	r := &relay{
		server:     server,
		runtimeDir: runtimeDir,
		// One client, no total timeout (the per-request context bounds polls).
		client: &http.Client{},
		loops:  make(map[string]*agentLoop),
	}

	// Register any startup agents before serving so they are live immediately.
	// Bounded by the flag's own size, so this cannot delay the bind below.
	for _, id := range splitAgents(*agentsFlag) {
		if _, err := r.register(id); err != nil {
			log.Fatalf("startup register %q: %v", id, err)
		}
		log.Printf("registered agent %q at startup", id)
	}

	// Bind + serve the control socket BEFORE replaying the spool (robots-mpr3).
	//
	// The replay below is O(agents on disk) and on a real fleet is not fast: 206
	// spools took ~7s on 2026-08-05. While it ran, nothing was bound, so
	// /health was unanswerable and `ensure-up.sh` — which only waited 10s and
	// force-restarted on a miss — declared a perfectly healthy, mid-startup
	// relay dead and killed it, restarting the replay from scratch. Binding
	// first makes /health answerable in milliseconds regardless of fleet size.
	//
	// Serving during the replay is safe: register() is mutex-guarded and
	// idempotent, so a control-socket register racing the replay either wins
	// (and the replay's own call is a no-op) or loses (and returns the same
	// spool path). Binding first also surfaces a duplicate-relay bind failure
	// before doing any replay work, rather than after.
	sockPath := filepath.Join(runtimeDir, "relay.sock")
	ln, err := listenControl(sockPath)
	if err != nil {
		log.Fatalf("cannot bind control socket %s: %v", sockPath, err)
	}

	srv := &http.Server{Handler: r.controlMux()}

	// Serve the control socket until shutdown.
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	log.Printf("up — server=%s runtime=%s socket=%s", server, runtimeDir, sockPath)

	// Resume agents from existing spools, now that /health already answers.
	resumed := resumeFromSpools(r, runtimeDir)
	log.Printf("spool resume complete — %d agent(s) resumed", resumed)

	// Wait for a termination signal or a fatal serve error.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Printf("received %s — shutting down", s)
	case err := <-serveErr:
		if err != nil {
			log.Printf("control socket failed: %v — shutting down", err)
		}
	}

	r.shutdown(srv)
	_ = os.Remove(sockPath) // best-effort: leave no stale socket behind
	log.Print("stopped")
}

// resumeFromSpools re-registers every agent that has a spool file in runtimeDir
// and returns how many were resumed.
//
// The registry is in-memory only, so a relay restart would otherwise silently
// stop every enrolled agent's upstream poll loop while their monitors keep
// tailing dead spools — observed fleet-wide on 2026-07-17 (19 agents deaf until
// hand re-enrolled). A spool file is durable evidence of enrollment, so it is
// re-registered at boot. register() is idempotent, so overlap with -agents (or
// with a concurrent control-socket register) is harmless.
//
// Callers must have already bound the control socket — see the comment in
// main(); this walk is O(agents on disk) and must not gate /health.
func resumeFromSpools(r *relay, runtimeDir string) int {
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return 0
	}
	resumed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".chan") {
			continue
		}
		id := strings.TrimSuffix(name, ".chan")
		if !validAgentID(id) {
			continue
		}
		if _, err := r.register(id); err != nil {
			log.Printf("spool-resume register %q: %v", id, err)
			continue
		}
		log.Printf("resumed agent %q from spool", id)
		resumed++
	}
	return resumed
}
