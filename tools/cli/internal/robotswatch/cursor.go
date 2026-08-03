// Persisted per-bead status cursor for the poll-daemon: the "what we last
// saw" half of the diff. { "<store>": { "<bead-id>": "<status>" } }.
//
// Ported from cursor.ts. A corrupt or missing cursor file is treated as
// empty — every store re-seeds (fires nothing) rather than replaying
// history, the safe failure mode: we lose one diff, never replay stale
// transitions.
package robotswatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// Cursor is the whole persisted diff state, keyed by store name.
type Cursor map[string]StoreState

// stateDir is the shared state dir for both the poll cursor and the
// tailer's offset file. Honors $PARLAY_STATE_HOME the same as
// internal/config.StateHome (default ~/.parlay).
func stateDir() string {
	return filepath.Join(config.StateHome(), "robots-watch")
}

func cursorPath() string {
	return filepath.Join(stateDir(), "cursor.json")
}

func readCursor() Cursor {
	data, err := os.ReadFile(cursorPath())
	if err != nil {
		return Cursor{}
	}
	var cursor Cursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor == nil {
		return Cursor{}
	}
	return cursor
}

// writeCursor persists cursor via a same-directory temp file + atomic
// rename. Any failure here panics rather than swallowing the error — the
// caller (pollOnce, under runPollOnce's recover) is the one boundary that
// decides a bad pass must not kill the daemon, matching how cursor.ts's
// unguarded mkdirSync/writeFileSync exceptions would bubble to index.ts's
// outer try/catch.
func writeCursor(cursor Cursor) {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	data, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	tmp := filepath.Join(dir, fmt.Sprintf(".cursor.%d.tmp", os.Getpid()))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		panic(err)
	}
	if err := os.Rename(tmp, cursorPath()); err != nil { // atomic swap
		panic(err)
	}
}
