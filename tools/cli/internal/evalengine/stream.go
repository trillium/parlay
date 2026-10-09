package evalengine

import "time"

func (e *Engine) stream(id string) *streamState {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.streams[id]
	if !ok {
		st = &streamState{}
		e.streams[id] = st
	}
	return st
}

// armSubmit starts (or re-arms) the SERVER-OWNED submit timer (delayMs from the
// handler's config, 1000ms by default). This is the crux of the pure model — the
// countdown that in the client build is a local setTimeout now runs on the server,
// one network hop away from the live buffer.
func (e *Engine) armSubmit(req EvalRequest, m *matchResult, out *actionList, delayMs int) {
	st := e.stream(req.StreamID)
	st.mu.Lock()
	// Re-arm: clear any prior timer (builtins.ts:22 clearTimeout(submitTimer)).
	if st.submitTimer != nil {
		st.submitTimer.Stop()
	}
	st.timerGen++
	gen := st.timerGen
	st.submitTail = m.matchedText
	st.submitBaseVer = req.Version
	streamID := req.StreamID

	st.submitTimer = time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
		e.fireSubmit(streamID, gen)
	})
	st.mu.Unlock()

	e.stats.mu.Lock()
	e.stats.SubmitsArmed++
	e.stats.mu.Unlock()

	// Advisory armTimer so the client can render a "submitting in 1s…" countdown.
	// The AUTHORITATIVE timer is the server one above; the client timer never
	// submits on its own.
	out.add(actArmTimer("submit", delayMs))
	out.add(actShowHint("submit-countdown", "auto-sending in 1s…", "info"))
}

// fireSubmit runs when the SERVER-OWNED timer elapses. It cannot see the live
// client buffer (that is the fundamental limitation of the pure model), so it
// re-verifies against the version it armed with and hands the client a submitNow
// carrying requireTail. The client does the FINAL re-verify against its truly
// current buffer before sending — the irreversibility guard.
func (e *Engine) fireSubmit(streamID string, gen int64) {
	st := e.stream(streamID)
	st.mu.Lock()
	// Generation guard: if a newer arm/cancel happened, this fire is stale.
	if gen != st.timerGen {
		st.mu.Unlock()
		e.stats.mu.Lock()
		e.stats.StaleTimerFires++
		e.stats.mu.Unlock()
		return
	}
	tail := st.submitTail
	base := st.submitBaseVer
	platform := st.platform // the surface this fire must land on
	st.submitTimer = nil
	st.timerGen++ // consume this generation
	seq := st.seq // the submitNow will get its own seq from pushSubmit
	_ = seq
	st.mu.Unlock()

	e.stats.mu.Lock()
	e.stats.SubmitsFired++
	e.stats.mu.Unlock()

	// The stripped text is computed by the CLIENT at apply time against its live
	// buffer (it knows the true current text); the server supplies the tail to
	// strip and re-verify. We pass text="" to mean "strip requireTail from your
	// current buffer and send the remainder" — see dispatcher.ts submitNow.
	if e.onSubmit != nil {
		// seq is assigned inside onSubmit via nextSeq to keep ordering correct.
		e.onSubmit(streamID, e.nextSeq(streamID), base, tail, "", platform)
	}
}

// cancelSubmit disarms the server-owned timer and tells the client to drop its
// advisory countdown. In the pure model this cancel must beat the fire across the
// network — the race brain-v4vje §3 design-A flagged. On a slow link the client's
// advisory armTimer may already have visually "fired" before this arrives.
func (e *Engine) cancelSubmit(streamID string, out *actionList, reason string) {
	st := e.stream(streamID)
	st.mu.Lock()
	had := st.submitTimer != nil
	if had {
		st.submitTimer.Stop()
		st.submitTimer = nil
		st.timerGen++ // invalidate any in-flight fire
	}
	st.mu.Unlock()
	if had {
		e.stats.mu.Lock()
		e.stats.SubmitsCancelled++
		e.stats.mu.Unlock()
		out.add(actCancelTimer("submit"))
		out.add(actClearHint("submit-countdown"))
	}
}

func (e *Engine) nextSeq(streamID string) int64 {
	st := e.stream(streamID)
	st.mu.Lock()
	st.seq++
	s := st.seq
	st.mu.Unlock()
	return s
}

// finish stamps the envelope (seq, baseVersion, protocol version) and records
// the eval-time stat. Every /eval response gets a fresh seq so the client's
// strict-ordering dispatcher can detect gaps.
func (e *Engine) finish(req EvalRequest, st *streamState, out *actionList, fired string, start time.Time) EvalResponse {
	ns := time.Since(start).Nanoseconds()
	e.stats.recordEval(ns)

	st.mu.Lock()
	st.seq++
	seq := st.seq
	st.mu.Unlock()

	return EvalResponse{
		StreamID:     req.StreamID,
		Actions:      out.items,
		BaseVersion:  req.Version,
		Seq:          seq,
		ProtocolV:    ProtocolVersion,
		EngineEvalNs: ns,
		Fired:        fired,
		Platform:     requestPlatform(req),
	}
}
