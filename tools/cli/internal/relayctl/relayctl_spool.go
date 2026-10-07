// The spool half of package relayctl's durable-trail readers: what message ids
// one agent's spool currently holds, which is the only thing in the fleet that
// can turn "the relay spooled this" into "and it is still waiting".
//
// Only ids are parsed out. The spool's text is chat history, and a diagnostic
// must not become a second copy of it.
package relayctl

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// SpoolIDs is the message ids currently present in one agent's spool, counted
// by id. It answers "is this line still sitting in the spool?" — the one fact
// that separates a message still queued from one that has left, which no
// other surface can say.
//
// It parses the spool's existing line format and nothing else: ids only,
// never the message text, so a diagnostic read cannot become a second copy of
// the chat history. The format (`CHAT_MSG|<id>|<role>|<text>[|from:<sender>]`)
// is the same one SpoolCursor and the monitor's line reader depend on.
type SpoolIDs struct {
	Path string
	// Exists is true when the file that was read is there; Retired says it was
	// the retired generation rather than the live spool.
	Exists    bool
	Retired   bool
	IDs       map[string]int
	Lines     int  // CHAT_MSG lines read; not a count of every line in the file
	Truncated bool // the scan hit spoolScanCap; IDs is a prefix
	Err       error
}

// Known reports whether the spool could be read at all. False means the
// absence of a message id from IDs proves nothing.
func (s SpoolIDs) Known() bool { return s.Exists && s.Err == nil }

// SpoolMessageIDs reads agentID's spool and returns the ids in it, falling
// back to the retired generation when the live spool is gone (a channel that
// ended is exactly when its queued lines matter most, and the relay parks them
// rather than deleting them). A missing file is Exists=false with no error;
// a file that cannot be opened carries Err.
func SpoolMessageIDs(agentID string) SpoolIDs {
	out := SpoolIDs{Path: SpoolPath(agentID), IDs: map[string]int{}}
	if f, err := os.Open(out.Path); err == nil {
		_ = f.Close()
	} else if !os.IsNotExist(err) {
		out.Exists, out.Err = true, err
		return out
	} else {
		retired := out.Path + spoolRetiredSuffix
		if _, statErr := os.Stat(retired); statErr == nil {
			out.Path, out.Retired = retired, true
		}
	}
	f, err := os.Open(out.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		out.Exists, out.Err = true, err
		return out
	}
	defer f.Close()
	out.Exists = true
	if st, statErr := f.Stat(); statErr == nil {
		out.Truncated = st.Size() > spoolScanCap
	}
	sc := bufio.NewScanner(io.LimitReader(f, spoolScanCap))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if !strings.HasPrefix(line, "CHAT_MSG|") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) >= 3 && parts[1] != "" {
			out.Lines++
			out.IDs[parts[1]]++
		}
	}
	if scErr := sc.Err(); scErr != nil {
		out.Err = scErr
	}
	return out
}
