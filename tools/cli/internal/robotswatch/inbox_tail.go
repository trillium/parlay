// parlay inbox-tail — the PUSH fast path for the inbox store. A byte-offset
// tailer of ~/data/inbox/events.jsonl (the emit stream the inbox
// create-wrapper appends to): every ~1s it reads only the bytes past a
// persisted offset, parses each new line for an inbox bead id, and calls
// inbox-dispatch immediately — sub-~1s create→dispatch latency instead of
// the poll interval. Mirrors robots-tail (tail.go); the poll daemon
// (robots-watch handler a2) stays the reconciler fallback, and
// inbox-dispatch idempotency makes a double-fire safe.
package robotswatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/help"
)

func inboxEventsPath() string {
	if p := os.Getenv("INBOX_EVENTS_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "data", "inbox", "events.jsonl")
}

func inboxOffsetPath() string {
	return filepath.Join(stateDir(), "inbox-tail-offset")
}

var inboxCreatedIDRe = regexp.MustCompile(`^inbox-[a-z0-9]+$`)

// parseInboxCreatedID parses one emit line → an inbox bead id, or ok=false
// (malformed / not an inbox id).
func parseInboxCreatedID(line string) (id string, ok bool) {
	var ev struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(ev.ID)
	if !inboxCreatedIDRe.MatchString(trimmed) {
		return "", false
	}
	return trimmed, true
}

func readInboxOffset(fallback int64) int64 {
	data, err := os.ReadFile(inboxOffsetPath())
	if err != nil {
		return fallback
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func writeInboxOffset(n int64) {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".inbox-tail-offset.%d.tmp", os.Getpid()))
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(n, 10)), 0o644); err != nil {
		panic(err)
	}
	if err := os.Rename(tmp, inboxOffsetPath()); err != nil {
		panic(err)
	}
}

// inboxTick is one tail pass: dispatch every new inbox-created id, persist
// the advanced offset.
func inboxTick(verbose bool) {
	path := inboxEventsPath()
	fallback := int64(0)
	if info, err := os.Stat(path); err == nil {
		fallback = info.Size()
	}
	start := readInboxOffset(fallback)
	lines, offset := readNewLines(path, start)
	for _, line := range lines {
		if id, ok := parseInboxCreatedID(line); ok {
			dispatchInbox(id, verbose)
		} else if verbose {
			fmt.Fprintln(os.Stderr, "inbox-tail: skip unparseable line")
		}
	}
	if offset != start {
		writeInboxOffset(offset)
	}
}

// CmdInboxTail is `parlay inbox-tail`'s entry point.
func CmdInboxTail(argv []string) {
	if help.Wanted("inbox-tail", argv) {
		return
	}
	r := args.Parse("inbox-tail", argv, []string{"--once", "--verbose"}, nil)
	verbose := r.Bool("--verbose")
	once := r.Bool("--once")

	path := inboxEventsPath()
	// First-ever run (no persisted offset) starts at EOF so history is not
	// replayed; a persisted offset resumes there, catching emits that landed
	// while we were down.
	if _, err := os.Stat(inboxOffsetPath()); os.IsNotExist(err) {
		size := int64(0)
		if info, statErr := os.Stat(path); statErr == nil {
			size = info.Size()
		}
		writeInboxOffset(size)
	}

	mode := "tailing every 1s"
	if once {
		mode = "single pass"
	}
	fmt.Fprintf(os.Stderr, "parlay inbox-tail — %s %s (fast path → inbox-dispatch)\n", mode, path)

	inboxTick(verbose)
	if once {
		return
	}
	for {
		time.Sleep(time.Second)
		inboxTickIsolated(verbose)
	}
}

// inboxTickIsolated: a single bad pass must never kill the daemon — log and continue.
func inboxTickIsolated(verbose bool) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(os.Stderr, "inbox-tail: pass failed (continuing): %v\n", rec)
		}
	}()
	inboxTick(verbose)
}
