// Package chathistory reads the chat server's OWN durable history file,
// messages.jsonl, as a bounded tail of RECORDS.
//
// The problem it exists to fix, in one sentence: the relay's delivery trail
// only knows about messages the relay was handed, so a message the server
// accepted and nothing ever picked up leaves no trace anywhere an operator
// looks. The server's history is the other half of that story — it says the
// message exists — and until now the only way to read it was GET
// /api/chat/history, which is exactly the route that cannot answer when the
// server is the thing that is broken.
//
// This is a FILE reader, deliberately, for the same reason the relay's trails
// are read off disk: at 2am the process that would answer over HTTP is often
// the process that is dead.
//
// # What it reads, and what it refuses to read
//
// Record carries five identifiers — id, ts, channel, role, from — and NOT the
// message body. That is structural, not a convention: the decode target has no
// Text field, so json.Unmarshal drops the body on the floor and no caller of
// this package can accidentally hold one. Message text already has a home (the
// server's own history, under the server's own retention), and a second copy
// in a diagnostic surface would be new retention this package has no mandate
// to create. TestReadNeverHoldsABody pins it.
//
// # Bounded, and honest about the bound
//
// A tail read keeps memory flat on a file the server will happily grow to
// 32 MiB before compacting, and it is lossy in exactly two ways — both of them
// REPORTED rather than silent:
//
//   - the byte cap (tailBudget): the read starts partway into the file, so one
//     line at the seek boundary may be lost and every older record is absent.
//   - the record cap (max): only the newest max records are kept.
//
// Truncated is true for either, and the caller says so. A reader that returned
// a prefix while looking complete is the failure this package exists to
// prevent.
//
// Nothing here writes, and nothing here is on a delivery path: it opens one
// file read-only.
package chathistory

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// FileName is the server's history file inside its state directory
// (packages/go-server/internal/store). One ChatMessage per line.
const FileName = "messages.jsonl"

// State is how the history file came back. "absent" and "unreadable" stay
// distinct from "read, and empty": a server that has never persisted a message
// and a server whose history cannot be opened are different answers, and
// collapsing them is how a broken read becomes a quiet fleet.
type State string

const (
	StateRead       State = "read"
	StateAbsent     State = "absent"
	StateUnreadable State = "unreadable"
)

// Record is one persisted message WITHOUT its body. The field set is the
// privacy boundary; see the package doc.
type Record struct {
	ID      string `json:"id"`
	Ts      string `json:"ts"`
	Channel string `json:"channel"`
	Role    string `json:"role"`
	From    string `json:"from"`
	// There is deliberately no Text here, so a body is never decoded.
}

const (
	// tailBudget bounds ONE read, in bytes. The server compacts the file down
	// to its in-memory ring at 32 MiB, so this is a bound on the reader, not
	// on the file.
	tailBudget int64 = 8 << 20
	// DefaultMaxRecords bounds how many records one read returns. A timeline
	// shows the newest events; older ones are named as truncated rather than
	// silently dropped.
	DefaultMaxRecords = 2000
)

// Result is one bounded read of the history file, oldest record first.
type Result struct {
	Path  string
	State State

	// Records is the newest max records, oldest first.
	Records     []Record
	ChannelLess int // messages with no channel: not addressed to an agent
	Corrupt     int // lines that are not this shape; skipped, never fatal
	Truncated   bool
	// OldestByte is where the read started: 0 means the whole file, anything
	// else means the prefix was not read.
	OldestByte int64
	Size       int64
	Err        error
}

// Read returns the newest max records in path. max <= 0 means
// DefaultMaxRecords. It never fails the caller: absence is StateAbsent and a
// read error is StateUnreadable with Err set, both of which the caller reports
// rather than mistaking for "no messages".
func Read(path string, max int) Result {
	if max <= 0 {
		max = DefaultMaxRecords
	}
	r := Result{Path: path, Records: []Record{}}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			r.State = StateAbsent
			return r
		}
		r.State, r.Err = StateUnreadable, err
		return r
	}
	defer f.Close()

	if st, statErr := f.Stat(); statErr == nil {
		r.Size = st.Size()
	}
	if r.Size > tailBudget {
		if _, seekErr := f.Seek(r.Size-tailBudget, io.SeekStart); seekErr != nil {
			r.State, r.Err = StateUnreadable, seekErr
			return r
		}
		r.Truncated, r.OldestByte = true, r.Size-tailBudget
	}

	br := bufio.NewReaderSize(f, 64<<10)
	// A tail that starts mid-file starts mid-LINE. Drop everything up to the
	// first newline: that partial line cannot be decoded, and counting it as
	// corrupt would be a lie about the file.
	if r.OldestByte > 0 {
		if _, err := br.ReadString('\n'); err != nil && err != io.EOF {
			r.State, r.Err = StateUnreadable, err
			return r
		}
	}

	for {
		line, readErr := br.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			var rec Record
			switch {
			case json.Unmarshal([]byte(line), &rec) != nil || rec.ID == "":
				r.Corrupt++
			case rec.Channel == "":
				// A message with no channel is fleet-wide (a hook firing, a
				// system update): real, and not addressed to any agent, so it
				// cannot be a delivery event. Counted, never listed.
				r.ChannelLess++
			default:
				r.Records = append(r.Records, rec)
				if len(r.Records) > max {
					// Keep the newest, and say so: a truncated list that reads
					// like a complete one is the failure this file exists to
					// prevent.
					r.Records = append(r.Records[:0], r.Records[len(r.Records)-max:]...)
					r.Truncated = true
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			r.State, r.Err = StateUnreadable, readErr
			return r
		}
	}

	r.State = StateRead
	return r
}
