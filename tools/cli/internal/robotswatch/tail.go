// parlay robots-tail — the PUSH fast path (task-jif2). A byte-offset tailer
// of ~/data/robots/events.jsonl (the emit stream the robots create-wrapper
// appends to), modeled on the server's hook-tailer.ts: every ~1s it reads
// only the bytes past a persisted offset, parses each new line for a robots
// bead id, and calls mechanic-dispatch immediately — sub-~1s create→dispatch
// latency instead of the poll interval. The poll daemon (robots-watch) stays
// the reconciler fallback for any emit that was missed; mechanic-dispatch
// idempotency makes a double-fire safe.
//
// Ported from tail.ts.
package robotswatch

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/help"
)

func eventsPath() string {
	if p := os.Getenv("ROBOTS_EVENTS_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "data", "robots", "events.jsonl")
}

func offsetPath() string {
	return filepath.Join(stateDir(), "tail-offset")
}

var robotsCreatedIDRe = regexp.MustCompile(`^robots-[a-z0-9]+$`)

// parseCreatedID parses one emit line → a robots bead id, or ok=false
// (malformed / not a robots id).
func parseCreatedID(line string) (id string, ok bool) {
	var ev struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(ev.ID)
	if !robotsCreatedIDRe.MatchString(trimmed) {
		return "", false
	}
	return trimmed, true
}

// readNewLines reads the bytes of path past offset. Returns the new lines
// and the new offset. Handles truncation/rotation (size < offset → restart
// from 0). Pure I/O, no dispatch.
func readNewLines(path string, offset int64) (lines []string, newOffset int64) {
	info, err := os.Stat(path)
	if err != nil {
		return []string{}, offset
	}
	size := info.Size()
	if size < offset {
		offset = 0 // rotated/truncated — restart
	}
	if size <= offset {
		return []string{}, offset
	}

	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		panic(err)
	}

	lines = []string{}
	for _, part := range strings.Split(string(buf), "\n") {
		if part != "" {
			lines = append(lines, part)
		}
	}
	return lines, size
}

func readOffset(fallback int64) int64 {
	data, err := os.ReadFile(offsetPath())
	if err != nil {
		return fallback
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func writeOffset(n int64) {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".tail-offset.%d.tmp", os.Getpid()))
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(n, 10)), 0o644); err != nil {
		panic(err)
	}
	if err := os.Rename(tmp, offsetPath()); err != nil {
		panic(err)
	}
}

// tick is one tail pass: dispatch every new robots-created id, persist the
// advanced offset.
func tick(verbose bool) {
	path := eventsPath()
	fallback := int64(0)
	if info, err := os.Stat(path); err == nil {
		fallback = info.Size()
	}
	start := readOffset(fallback)
	lines, offset := readNewLines(path, start)
	for _, line := range lines {
		if id, ok := parseCreatedID(line); ok {
			dispatchMechanic(id, verbose)
		} else if verbose {
			fmt.Fprintln(os.Stderr, "robots-tail: skip unparseable line")
		}
	}
	if offset != start {
		writeOffset(offset)
	}
}

// CmdRobotsTail is `parlay robots-tail`'s entry point.
func CmdRobotsTail(argv []string) {
	if help.Wanted("robots-tail", argv) {
		return
	}
	r := args.Parse("robots-tail", argv, []string{"--once", "--verbose"}, nil)
	verbose := r.Bool("--verbose")
	once := r.Bool("--once")

	path := eventsPath()
	// First-ever run (no persisted offset) starts at EOF so history is not
	// replayed; a persisted offset resumes there, catching emits that landed
	// while we were down.
	if _, err := os.Stat(offsetPath()); os.IsNotExist(err) {
		size := int64(0)
		if info, statErr := os.Stat(path); statErr == nil {
			size = info.Size()
		}
		writeOffset(size)
	}

	mode := "tailing every 1s"
	if once {
		mode = "single pass"
	}
	fmt.Fprintf(os.Stderr, "parlay robots-tail — %s %s (fast path → mechanic-dispatch)\n", mode, path)

	tick(verbose)
	if once {
		return
	}
	for {
		time.Sleep(time.Second)
		tickIsolated(verbose)
	}
}

// tickIsolated: a single bad pass must never kill the daemon — log and continue.
func tickIsolated(verbose bool) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(os.Stderr, "robots-tail: pass failed (continuing): %v\n", rec)
		}
	}()
	tick(verbose)
}
