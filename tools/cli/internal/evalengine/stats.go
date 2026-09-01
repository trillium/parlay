package evalengine

func (s *Stats) recordEval(ns int64) {
	s.mu.Lock()
	s.Evals++
	s.TotalEvalNs += ns
	if ns > s.MaxEvalNs {
		s.MaxEvalNs = ns
	}
	s.mu.Unlock()
}

func (s *Stats) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	avg := int64(0)
	if s.Evals > 0 {
		avg = s.TotalEvalNs / s.Evals
	}
	return map[string]any{
		"evals":            s.Evals,
		"submitsArmed":     s.SubmitsArmed,
		"submitsFired":     s.SubmitsFired,
		"submitsCancelled": s.SubmitsCancelled,
		"staleTimerFires":  s.StaleTimerFires,
		"avgEvalNs":        avg,
		"maxEvalNs":        s.MaxEvalNs,
	}
}
