package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// ── The load path: file source, fs-watch, fail-closed hot-reload ────────────────
//
// The engine resolves its command set from three layers, highest wins (contract
// §Loading precedence): per-request override > loaded file > embedded default. This
// file owns the middle layer — a manifest file (PARLAY_COMMANDS env or a
// commands.json next to the binary), watched for changes and hot-swapped ATOMICALLY
// on a valid parse.
//
// FAIL-CLOSED is the invariant: an invalid or unreadable manifest is rejected with
// a logged reason and the PRIOR GOOD command set stays live. The engine never falls
// open to zero commands. NewEngine already seeded the embedded default, so even a
// first-load failure leaves a working set.
//
// The watcher polls os.Stat (mtime+size) rather than pulling in an fsnotify
// dependency — this service is deliberately stdlib-only (see main.go), and a poll
// is trivially reversible and portable. A change is detected within one interval.

const watchInterval = 500 * time.Millisecond

// loadManifestFile reads and fully validates a manifest file. Any problem (missing,
// unreadable, malformed, closed-vocabulary violation) returns an error and no
// manifest — the caller keeps its prior good set.
func loadManifestFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseManifest(data)
}

// resolveCommandsPath picks the file layer's source: PARLAY_COMMANDS if set,
// otherwise a commands.json sitting next to the binary if one exists. Returns
// ("", false) when neither is configured (embedded default is the whole story).
// When PARLAY_COMMANDS is set the path is returned even if the file is absent, so
// the watcher can pick it up if it appears later.
func resolveCommandsPath() (path string, watch bool) {
	if p := os.Getenv("PARLAY_COMMANDS"); p != "" {
		return p, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	cand := filepath.Join(filepath.Dir(exe), "commands.json")
	if _, err := os.Stat(cand); err == nil {
		return cand, true
	}
	return "", false
}

// manifestWatcher polls a manifest file and hot-swaps the engine's command set when
// the file changes AND parses cleanly. It tracks the last-seen mtime+size to detect
// changes; a parse failure is logged and skipped (prior good set stays live).
type manifestWatcher struct {
	engine  *Engine
	path    string
	lastMod time.Time
	lastLen int64
	primed  bool // whether lastMod/lastLen reflect an already-applied load
}

// newManifestWatcher makes a watcher. If applied is true, the current file state is
// treated as already loaded (the startup load applied it), so the first check won't
// redundantly reload an unchanged file.
func newManifestWatcher(engine *Engine, path string, applied bool) *manifestWatcher {
	w := &manifestWatcher{engine: engine, path: path}
	if applied {
		if fi, err := os.Stat(path); err == nil {
			w.lastMod = fi.ModTime()
			w.lastLen = fi.Size()
			w.primed = true
		}
	}
	return w
}

// check performs one poll. It returns reloaded=true only when the file changed and
// its new content parsed+validated and was atomically applied. A stat error (file
// gone) or a parse error returns err and leaves the live set untouched — the
// fail-closed guarantee. check is the deterministic unit the tests drive directly.
func (w *manifestWatcher) check() (reloaded bool, err error) {
	fi, statErr := os.Stat(w.path)
	if statErr != nil {
		return false, statErr
	}
	if w.primed && fi.ModTime().Equal(w.lastMod) && fi.Size() == w.lastLen {
		return false, nil // unchanged since last poll
	}
	// Record the observed state up front so a repeatedly-invalid file is not
	// re-read every tick (we only retry once its mtime/size move again).
	w.lastMod = fi.ModTime()
	w.lastLen = fi.Size()
	w.primed = true

	man, perr := loadManifestFile(w.path)
	if perr != nil {
		return false, perr // invalid — prior good set stays live (fail-closed)
	}
	w.engine.SetCommands(man)
	return true, nil
}

// run polls the file on a ticker until stop is closed (or forever if stop is nil).
// It is the long-lived goroutine started from main; check() holds the real logic.
func (w *manifestWatcher) run(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			reloaded, err := w.check()
			switch {
			case err != nil:
				log.Printf("[manifest-watch] reload skipped, prior command set stays live: %v", err)
			case reloaded:
				e := w.engine
				e.mu.Lock()
				n := len(e.commands)
				e.mu.Unlock()
				log.Printf("[manifest-watch] hot-reloaded %s (%d commands live)", w.path, n)
			}
		}
	}
}

// installFileSource wires the file layer into an engine at startup: it loads the
// configured manifest (if any), applies it over the embedded default on success,
// and starts the fs-watch goroutine. It returns the resolved path ("" if none).
// Fail-closed: a first-load failure logs and leaves the embedded default live.
func installFileSource(engine *Engine, stop <-chan struct{}) string {
	path, watch := resolveCommandsPath()
	if !watch {
		return ""
	}
	applied := false
	if man, err := loadManifestFile(path); err == nil {
		engine.SetCommands(man)
		applied = true
		log.Printf("loaded command manifest from %s", path)
	} else {
		log.Printf("command manifest %s not loaded (embedded default stays live): %v", path, err)
	}
	w := newManifestWatcher(engine, path, applied)
	go w.run(watchInterval, stop)
	return path
}
