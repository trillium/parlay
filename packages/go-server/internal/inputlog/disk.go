// File I/O for the ledger: the append primitive, the startup replay, and
// the byte-cap compaction. Every function here runs on the writer goroutine
// (write, appendToFile, compact) or before it starts (loadFromDisk), so the
// append handle is never shared.
package inputlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"parlay/go-server/internal/atomicfile"
)

// appendToFile opens the append handle on first use and writes one
// already-marshaled line. Called only from the writer goroutine, so the
// handle is not shared.
func (l *Log) appendToFile(line []byte) error {
	if l.file == nil {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("inputlog: open %s: %w", l.path, err)
		}
		l.file = f
	}
	_, err := l.file.Write(line)
	return err
}

// loadFromDisk replays the JSONL file into the ring. Only the last
// maxEvents lines are kept in memory; the file is the durable record.
func (l *Log) loadFromDisk() error {
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inputlog: open %s: %w", l.path, err)
	}
	defer f.Close()

	var ring []Event
	var maxSeq uint64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip a corrupt line rather than fail startup
		}
		ring = append(ring, e)
		if len(ring) > l.maxEvents {
			ring = ring[1:]
		}
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("inputlog: read %s: %w", l.path, err)
	}
	l.ring = ring
	l.nextSeq = maxSeq + 1
	return nil
}

// write appends one event to the ring and the file, compacting when the
// file outgrows its cap. A compaction failure is logged, never propagated:
// the append itself already succeeded.
func (l *Log) write(e Event) {
	e.Seq = l.nextSeq
	l.nextSeq++
	if e.Ts == "" {
		e.Ts = time.Now().UTC().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(e)
	if err != nil {
		l.rejected.Add(1)
		return
	}
	line = append(line, '\n')
	if err := l.appendLine(line); err != nil {
		log.Printf("inputlog: append failed: %v", err)
		return
	}
	l.mu.Lock()
	l.ring = append(l.ring, e)
	if len(l.ring) > l.maxEvents {
		l.ring = l.ring[1:]
	}
	l.appended++
	l.mu.Unlock()

	if l.overByteCap() {
		if err := l.compact(); err != nil {
			log.Printf("inputlog: compaction failed: %v", err)
		}
	}
}

func (l *Log) overByteCap() bool {
	if l.file == nil || l.maxBytes <= 0 {
		return false
	}
	info, err := l.file.Stat()
	return err == nil && info.Size() > l.maxBytes
}

// compact rewrites the file to hold exactly the retained ring, discarding
// what the ring already pruned.
func (l *Log) compact() error {
	l.mu.RLock()
	var b []byte
	for _, e := range l.ring {
		line, err := json.Marshal(e)
		if err != nil {
			l.mu.RUnlock()
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	l.mu.RUnlock()

	if err := atomicfile.Write(l.path, b, 0o644); err != nil {
		return err
	}
	// The atomic rewrite swapped the directory entry, not the inode the
	// append handle points at — reopen so later writes land in the new file.
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	return nil
}
