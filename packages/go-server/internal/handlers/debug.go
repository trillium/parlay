package handlers

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Debug telemetry (port of router-debug.ts) ──────────────────────────────
// /api/debug/input-timing — lightweight, in-memory, ephemeral per-device
// input-timing samples (autoResize cost, inter-keystroke cadence). Restarts
// clear the window; nothing is persisted.

type timingSample struct {
	CostMs    float64 `json:"costMs"`
	SinceLast float64 `json:"sinceLastMs"`
	At        int64   `json:"-"`
}

type deviceSamples struct {
	ua      string
	samples []timingSample
}

const maxTimingSamples = 200
const timingWindow = 10 * time.Minute

var timingDevices struct {
	sync.Mutex
	m map[string]*deviceSamples
}

func init() { timingDevices.m = make(map[string]*deviceSamples) }

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func shortUA(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		return "Safari/iOS"
	case strings.Contains(ua, "Android"):
		return "Chrome/Android"
	case strings.Contains(ua, "Safari") && !strings.Contains(ua, "Chrome"):
		return "Safari/macOS"
	case strings.Contains(ua, "Firefox"):
		return "Firefox"
	case strings.Contains(ua, "Chrome"):
		return "Chrome"
	default:
		if len(ua) > 24 {
			return ua[:24]
		}
		return ua
	}
}

// handleDebugInputTiming implements GET and POST /api/debug/input-timing.
func handleDebugInputTiming() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ingestInputTiming(w, r)
			return
		}
		if r.Method == http.MethodGet {
			reportInputTiming(w)
			return
		}
		methodNotAllowed(w, "GET, POST")
	}
}

func ingestInputTiming(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Device  string         `json:"device"`
		UA      string         `json:"ua"`
		Samples []timingSample `json:"samples"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	device := strings.TrimSpace(body.Device)
	if len(device) > 40 {
		device = device[:40]
	}
	if device == "" || len(body.Samples) == 0 {
		writeStatusError(w, http.StatusBadRequest, "device + samples required")
		return
	}
	ua := strings.TrimSpace(body.UA)
	if len(ua) > 200 {
		ua = ua[:200]
	}

	now := time.Now().UnixMilli()
	timingDevices.Lock()
	entry := timingDevices.m[device]
	if entry == nil {
		entry = &deviceSamples{ua: ua}
		timingDevices.m[device] = entry
	}
	entry.ua = ua
	// Prune stale, then append valid samples.
	kept := entry.samples[:0]
	for _, s := range entry.samples {
		if now-s.At < timingWindow.Milliseconds() {
			kept = append(kept, s)
		}
	}
	entry.samples = kept
	for _, s := range body.Samples {
		if !isFinite(s.CostMs) || !isFinite(s.SinceLast) {
			continue
		}
		s.At = now
		entry.samples = append(entry.samples, s)
		if len(entry.samples) > maxTimingSamples {
			entry.samples = entry.samples[1:]
		}
	}
	stored := len(entry.samples)
	timingDevices.Unlock()

	writeJSON(w, map[string]any{"ok": true, "stored": stored})
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func reportInputTiming(w http.ResponseWriter) {
	now := time.Now().UnixMilli()
	out := map[string]any{}

	timingDevices.Lock()
	defer timingDevices.Unlock()
	for id, entry := range timingDevices.m {
		var costs, cadence []float64
		for _, s := range entry.samples {
			if now-s.At >= timingWindow.Milliseconds() {
				continue
			}
			costs = append(costs, s.CostMs)
			if s.SinceLast > 0 && s.SinceLast < 1000 {
				cadence = append(cadence, s.SinceLast)
			}
		}
		if len(costs) == 0 {
			continue
		}
		sortedCosts := append([]float64(nil), costs...)
		sort.Float64s(sortedCosts)
		entryReport := map[string]any{
			"ua":      shortUA(entry.ua),
			"samples": len(costs),
			"cost": map[string]any{
				"p50": percentile(sortedCosts, 0.5),
				"p95": percentile(sortedCosts, 0.95),
				"max": sortedCosts[len(sortedCosts)-1],
			},
		}
		if len(cadence) > 0 {
			sortedCadence := append([]float64(nil), cadence...)
			sort.Float64s(sortedCadence)
			entryReport["cadence"] = map[string]any{
				"p50": percentile(sortedCadence, 0.5),
				"p95": percentile(sortedCadence, 0.95),
			}
		} else {
			entryReport["cadence"] = nil
		}
		out[id] = entryReport
	}
	writeJSON(w, out)
}
