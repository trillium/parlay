// Bead-mode capture (task-r887x): the same submit path captures the
// incoming text as a bead instead of typing it. The Talon path is fully
// bypassed — no focus resolution, no actions.insert, no target needed.
//
// Two hard rules govern the exec path:
//  1. The wrapper is resolved to an explicit path (env override > known
//     install locations > PATH) and recorded on the outcome, never run
//     through an inherited PATH blindly. A launchd-spawned server has a
//     minimal PATH, so explicit resolution is a correctness property.
//  2. The text travels as a single argv element. The input is arbitrary
//     user content from a phone dictation box: no sh -c, no string
//     concatenation into a command line, ever.
package remoteinput

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// beadExecTimeout bounds one wrapper invocation. Bead creation is fast;
// this is a transport guard so a hung wrapper cannot stall the FIFO
// worker behind it forever.
const beadExecTimeout = 15 * time.Second

// storeNameRe is the store rule: any registered wrapper name works, so
// the set is never hard-coded (a hard-coded set would rot as stores are
// added). The name must be a safe single path element — lowercase start,
// then letters, digits, dash, underscore — so it can only ever resolve
// to <dir>/<name>, never a path traversal or flag injection.
var storeNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// NormalizeMode maps "" to inject; anything else passes through so the
// caller gets a typed rejection for unknown modes.
func NormalizeMode(m string) string {
	if m == "" {
		return ModeInject
	}
	return m
}

// NormalizeStore maps "" to the default capture queue.
func NormalizeStore(s string) string {
	if s == "" {
		return DefaultBeadStore
	}
	return s
}

// ValidStore reports whether name may resolve to a wrapper binary.
func ValidStore(name string) bool {
	return storeNameRe.MatchString(name)
}

// BeadCreator is the seam bead-mode creation goes through. The service
// orchestrates (validate → dry-run report → create → outcome);
// implementations only resolve + invoke the wrapper.
type BeadCreator interface {
	// Resolve returns the explicit wrapper path for store, or a
	// *WrapperMissingError naming everything that was tried.
	Resolve(store string) (string, error)
	// Create captures text via the store wrapper and returns the new
	// bead id. The text is passed as one argv element, never shelled.
	Create(store, text string) (beadID, wrapper string, err error)
}

// WrapperMissingError is the typed failure when no wrapper resolves.
type WrapperMissingError struct {
	Store string
	Tried []string
}

func (e *WrapperMissingError) Error() string {
	return fmt.Sprintf("bead wrapper not found for store %q (tried: %s)",
		e.Store, strings.Join(e.Tried, ", "))
}

// ExecBeadCreator shells nothing: it execs the resolved wrapper with
// argv ["q", text] directly.
type ExecBeadCreator struct {
	// dirs overrides the known install locations (tests point it at a
	// temp dir); nil means the production locations.
	dirs []string
}

// NewExecBeadCreator builds the production creator.
func NewExecBeadCreator() *ExecBeadCreator { return &ExecBeadCreator{} }

var _ BeadCreator = (*ExecBeadCreator)(nil)

// storeEnvVar maps a store to its explicit override, e.g. inbox →
// FM_INBOX_BIN (task → FM_TASK_BIN). Dashes become underscores.
func storeEnvVar(store string) string {
	return "FM_" + strings.ToUpper(strings.ReplaceAll(store, "-", "_")) + "_BIN"
}

// Resolve implements BeadCreator: env override > known install
// locations > PATH, with a typed error naming every path tried.
func (c *ExecBeadCreator) Resolve(store string) (string, error) {
	var tried []string
	if p := os.Getenv(storeEnvVar(store)); p != "" {
		tried = append(tried, fmt.Sprintf("%s=%s", storeEnvVar(store), p))
		if isExecutable(p) {
			return p, nil
		}
	}
	dirs := c.dirs
	if dirs == nil {
		home, _ := os.UserHomeDir()
		dirs = []string{
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".pi", "agent", "bin"),
		}
	}
	for _, d := range dirs {
		p := filepath.Join(d, store)
		tried = append(tried, p)
		if isExecutable(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(store); err == nil {
		return p, nil
	}
	tried = append(tried, "PATH:"+store)
	return "", &WrapperMissingError{Store: store, Tried: tried}
}

// isExecutable reports whether p is a runnable regular file.
func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode()&0o111 != 0
}

// beadArgv builds the wrapper invocation. Pure function so tests prove
// the text stays a single argv element without running anything.
func beadArgv(text string) []string { return []string{"q", text} }

// Create implements BeadCreator. Stdout must be exactly one token (the
// new id, e.g. "inbox-abc12"); anything else is a typed failure — an id
// is never invented from half-formed output.
func (c *ExecBeadCreator) Create(store, text string) (string, string, error) {
	wrapper, err := c.Resolve(store)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), beadExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, wrapper, beadArgv(text)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", wrapper, fmt.Errorf("bead create failed (store %q via %s): %v: %s",
			store, wrapper, err, strings.TrimSpace(stderr.String()))
	}
	id := strings.TrimSpace(stdout.String())
	if fields := strings.Fields(id); len(fields) != 1 || id == "" {
		return "", wrapper, fmt.Errorf(
			"bead created but id unparseable (store %q via %s, no id invented): output %q stderr %q",
			store, wrapper, stdout.String(), strings.TrimSpace(stderr.String()))
	}
	return id, wrapper, nil
}
