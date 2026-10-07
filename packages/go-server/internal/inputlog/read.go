// Read side of the ledger: the retained ring, narrowed either to everything
// or to one input's hops. All of it is RWMutex-guarded against the writer.
package inputlog

// Events returns a copy of the retained events, oldest first.
func (l *Log) Events() []Event {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Event, len(l.ring))
	copy(out, l.ring)
	return out
}

// EventsFor returns the retained hops of one input, oldest first — the raw
// material a replay renders. An unknown id returns nil, which is an honest
// empty answer, not an error: the input may predate the retained window.
func (l *Log) EventsFor(inputID string) []Event {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return After(l.ring, 0, func(e Event) bool { return e.InputID == inputID })
}

// EventsAfter returns the retained hops with Seq strictly greater than after,
// oldest first — the read a live tail needs so it reads FORWARD from where it
// stopped instead of re-reading a fixed newest-N window that a burst can
// silently outrun.
//
// A cursor older than the retained window is not an error and not a lie. The
// caller gets the oldest hops still retained, and the first Seq it sees names
// the span that was evicted in between (nothing else could be missing, because
// seqs are dense). Only the reader can tell "nothing new" from "I fell off the
// end", and it can only do that if the answer carries its own oldest seq.
func (l *Log) EventsAfter(after uint64) []Event {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return After(l.ring, after, nil)
}

// After narrows a hop slice to those strictly newer than seq, preserving
// order; an optional keep predicate narrows it further. It is exported beside
// EventsAfter so the handler can compose a cursor with an id narrowing without
// a second copy of the rule.
func After(events []Event, seq uint64, keep func(Event) bool) []Event {
	var out []Event
	for _, e := range events {
		if e.Seq <= seq || (keep != nil && !keep(e)) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Stats reports the ledger's own counters plus the current queue depth.
func (l *Log) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	l.mu.RLock()
	retained := uint64(len(l.ring))
	written := l.appended
	l.mu.RUnlock()
	return Stats{
		Retained:      retained,
		Written:       written,
		Dropped:       l.dropped.Load(),
		Rejected:      l.rejected.Load(),
		Queue:         len(l.queue),
		NewestSeq:     l.lastSeq.Load(),
		MinConfidence: l.minConfidence,
	}
}
