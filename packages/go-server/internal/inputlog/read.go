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
	var out []Event
	for _, e := range l.ring {
		if e.InputID == inputID {
			out = append(out, e)
		}
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
		Retained: retained,
		Written:  written,
		Dropped:  l.dropped.Load(),
		Rejected: l.rejected.Load(),
		Queue:    len(l.queue),
	}
}
