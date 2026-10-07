// Package relayctl is the parlay CLI's READ-ONLY client for the relay's local
// control socket, plus the three local artifacts that live in the same runtime
// directory: each agent's spool file (`<agent>.chan`), the delivery ledger
// (`delivery.log`), and the socket itself.
//
// Why this exists: the relay is the only component that witnesses delivery —
// it is the process that appends a message to an agent's spool and the only
// one that knows when that channel stopped being polled. `tools/cli/internal/
// monitor` learns about delivery by tailing the spool, which tells it nothing
// about why a spool stopped growing, and `parlay-monitor.sh` already reads the
// control socket with curl. The CLI had no way to ask, so an operator
// diagnosing a silent agent had to read source.
//
// Every read here is a GET. /register and /unregister exist on the same socket
// and are deliberately NOT exposed: an observability surface that can enroll
// or retire an agent is not an observability surface.
//
// Nothing here ever calls httpc.Die and nothing here ever fails a caller: each
// function reports "I could not ask" as ok=false (or a zero value) so the
// caller can name the degradation instead of inventing a healthy answer. The
// relay may simply not be running — that is the normal state on a machine
// using `parlay monitor --legacy-poll`.
//
// The split is by plane: this file holds the TYPES, the runtime-dir resolution,
// and the spool reads (filesystem); relayctl_socket.go holds the control-socket
// client and its three typed reads.
package relayctl

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Env vars. These are the SAME two `tools/monitor/parlay-monitor.sh` resolves,
// deliberately: one runtime dir, one socket path, one answer. A second
// resolution scheme would let the CLI report on a relay the monitor never talks
// to (tools/monitor/NOTES.md owns the table).
const (
	RuntimeEnv = "PARLAY_RELAY_RUNTIME"
	SockEnv    = "PARLAY_RELAY_SOCK"
)

// ControlTimeout bounds one control-socket GET. The relay answers these from
// memory or from a bounded tail read, so a peer still silent after this is
// wedged, not slow — and an observability read that can hang forever is worse
// than one that says "could not ask".
const ControlTimeout = 3 * time.Second

// spoolScanCap bounds the line count and cursor read of one spool. The monitor
// still tails the whole file; this is a diagnostic scan, and a fleet whose
// spool exceeds 8 MiB is past the point where an exact count is the useful
// answer. Truncated=true says the number is a floor.
const spoolScanCap int64 = 8 << 20

// RuntimeDir resolves the relay runtime directory: $PARLAY_RELAY_RUNTIME, else
// $TMPDIR/parlay, else /tmp/parlay. Trailing slashes are trimmed so a caller
// can join paths without producing "//".
func RuntimeDir() string {
	dir := strings.TrimSpace(os.Getenv(RuntimeEnv))
	if dir == "" {
		base := os.Getenv("TMPDIR")
		if base == "" {
			base = "/tmp"
		}
		dir = filepath.Join(base, "parlay")
	}
	return strings.TrimRight(dir, "/")
}

// SockPath resolves the relay control socket: $PARLAY_RELAY_SOCK, else
// <runtime>/relay.sock.
func SockPath() string {
	if p := strings.TrimSpace(os.Getenv(SockEnv)); p != "" {
		return p
	}
	return filepath.Join(RuntimeDir(), "relay.sock")
}

// SpoolPath is agentID's spool file in the resolved runtime dir.
func SpoolPath(agentID string) string {
	return filepath.Join(RuntimeDir(), agentID+".chan")
}

// LedgerPath is the active delivery ledger in the resolved runtime dir.
func LedgerPath() string {
	return filepath.Join(RuntimeDir(), "delivery.log")
}

// Health is GET /health: {ok,server,runtime}. Server is WHICH upstream this
// relay polls, which is what lets a caller notice the registered-but-deaf case
// (a relay bound to a different chat server) instead of reporting "relay up".
type Health struct {
	OK      bool   `json:"ok"`
	Server  string `json:"server"`
	Runtime string `json:"runtime"`
}

// Agents is GET /agents: the ids this relay currently holds a poll loop for.
type Agents struct {
	Agents  []string `json:"agents"`
	Server  string   `json:"server"`
	Runtime string   `json:"runtime"`
}

// DeliveryEntry is one delivery.log line, field-for-field the relay's own
// deliveryEntry (tools/relay/relay_delivery.go). SpoolLines is a pointer so an
// explicit 0 survives: "nothing was waiting" and "not recorded" are different
// facts. Agent is empty on the fleet-wide events (`started`, `rotated`) — the
// reader names which is which rather than inventing an agent for them.
type DeliveryEntry struct {
	Ts         string `json:"ts"`
	Event      string `json:"event"`
	Agent      string `json:"agent,omitempty"`
	Msg        string `json:"msg,omitempty"`
	Role       string `json:"role,omitempty"`
	From       string `json:"from,omitempty"`
	Reason     string `json:"reason,omitempty"`
	SpoolLines *int   `json:"spoolLines,omitempty"`
}

// Delivery is GET /delivery: the data-plane trail. Enabled and Exists are the
// two fields that keep absence from reading as health — Exists=false is a
// ledger that was never written, Enabled=false is one switched off with
// PARLAY_RELAY_DELIVERY_LOG=0, and both differ from an empty trail.
//
// The relay's ?agent= narrows to that agent's own rows, so the fleet-wide
// `started` and `rotated` markers are ABSENT from an agent-filtered answer.
// That is the route's contract, not a property of the trail: ReadLedger on the
// disk path returns every row and the caller decides what to keep.
type Delivery struct {
	OK      bool            `json:"ok"`
	Enabled bool            `json:"enabled"`
	Exists  bool            `json:"exists"`
	Ledger  string          `json:"ledger"`
	Count   int             `json:"count"`
	Entries []DeliveryEntry `json:"entries"`
}

// SpoolInfo is one agent's spool file as this process can see it. Exists is
// separate from Lines because "no spool" (nothing was ever queued, or the
// relay is not running) and "an empty spool" (the relay created it and has
// spooled nothing) are different answers, and Err is separate from both
// because "unreadable" is a third.
type SpoolInfo struct {
	Path      string
	Exists    bool
	Lines     int
	Truncated bool // the scan hit spoolScanCap; Lines is a floor
	Err       error
}

// Spool inspects agentID's spool file: does it exist, how many lines does it
// hold, and could it be read at all. A missing file is Exists=false with no
// error; a file that exists but cannot be opened carries Err.
func Spool(agentID string) SpoolInfo {
	info := SpoolInfo{Path: SpoolPath(agentID)}
	f, err := os.Open(info.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return info
		}
		info.Exists = true
		info.Err = err
		return info
	}
	defer f.Close()
	info.Exists = true

	if st, err := f.Stat(); err == nil {
		info.Truncated = st.Size() > spoolScanCap
	}
	// io.LimitReader keeps a spool that grew past the cap from turning a
	// diagnostic into an unbounded read; the count is then a floor and says so.
	sc := bufio.NewScanner(io.LimitReader(f, spoolScanCap))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			info.Lines++
		}
	}
	if err := sc.Err(); err != nil {
		info.Err = err
	}
	return info
}

// SpoolCursor returns the message id a monitor resuming from this spool would
// start after — "" when there is none.
//
// This duplicates tools/relay/relay_poll.go's lastSpooledID on purpose: the
// relay is a separate Go module, so the CLI cannot import it, and the operator
// needs the SAME answer the relay would compute. The rules are copied exactly:
// read only the tail (64 KiB), take the last line with the exact "CHAT_MSG|"
// prefix, and accept it only when its role is "user" or "agent" — a spool whose
// last line is a non-chat event (tts_event) must not seed a cursor the poll
// after-index cannot resolve, which would replay the channel's backlog.
// relayctl_test.go pins each rule.
func SpoolCursor(spoolPath string) string {
	f, err := os.Open(spoolPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	const window = 64 * 1024
	size := info.Size()
	start := int64(0)
	if size > window {
		start = size - window
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return ""
	}
	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(line, "CHAT_MSG|") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) >= 3 && parts[1] != "" && (parts[2] == "user" || parts[2] == "agent") {
			return parts[1]
		}
	}
	return ""
}
