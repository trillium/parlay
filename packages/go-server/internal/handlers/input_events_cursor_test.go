// The cursor form of the input-seam read route: a live tail reads FORWARD
// from `afterSeq`, and `limit` means the OLDEST N of that set rather than the
// newest N. That asymmetry is the point — a reader paging forward must never
// have a page silently omitted out from under it — so it is pinned here
// rather than left to the description.
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"parlay/go-server/internal/store"
)

func getInputEvents(t *testing.T, st *store.Store, query string) inputEventsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, inputEventsPath+query, nil)
	rec := httptest.NewRecorder()
	handleInputEvents(st)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s%s = %d, want 200", inputEventsPath, query, rec.Code)
	}
	var resp inputEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}
	return resp
}

func TestInputEventsCursorReadsForwardAndPagesFromTheOldest(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	for _, text := range []string{"one", "two", "three"} {
		postSend(t, st, b, `{"text":"`+text+`","toAgent":"c0"}`)
	}
	waitForEvents(t, st, 3)

	all := getInputEvents(t, st, "")
	if len(all.Events) != 3 {
		t.Fatalf("no cursor returned %d events, want the whole retained window", len(all.Events))
	}
	if all.Stats.NewestSeq != all.Events[2].Seq {
		t.Errorf("stats.newestSeq = %d, want %d", all.Stats.NewestSeq, all.Events[2].Seq)
	}

	forward := getInputEvents(t, st, "?afterSeq="+seqStr(all.Events[0].Seq))
	if len(forward.Events) != 2 || forward.Events[0].Seq != all.Events[1].Seq || forward.Events[1].Seq != all.Events[2].Seq {
		t.Fatalf("afterSeq(%d) = %+v, want the two newer hops, oldest first", all.Events[0].Seq, forward.Events)
	}

	// The load-bearing asymmetry: with a cursor, `limit` keeps the OLDEST
	// page, so a paging reader advances without skipping. Without one it
	// keeps the newest, exactly as before.
	oldest := getInputEvents(t, st, "?afterSeq=0&limit=1")
	if len(oldest.Events) != 1 || oldest.Events[0].Seq != all.Events[0].Seq {
		t.Errorf("afterSeq=0&limit=1 returned %+v, want the oldest hop (%d)", oldest.Events, all.Events[0].Seq)
	}
	newest := getInputEvents(t, st, "?limit=1")
	if len(newest.Events) != 1 || newest.Events[0].Seq != all.Events[2].Seq {
		t.Errorf("limit=1 with no cursor returned %+v, want the newest hop (%d)", newest.Events, all.Events[2].Seq)
	}

	// At the live edge: an empty list, not an error, and still 200.
	edge := getInputEvents(t, st, "?afterSeq="+seqStr(all.Stats.NewestSeq))
	if len(edge.Events) != 0 {
		t.Errorf("afterSeq=newest returned %+v, want none", edge.Events)
	}

	// An unreadable cursor degrades to the WIDEST answer: dropping the page
	// the reader asked for and returning the newest instead would look calm.
	bad := getInputEvents(t, st, "?afterSeq=not-a-number")
	if len(bad.Events) != 3 {
		t.Errorf("afterSeq=garbage returned %d events, want the whole retained window", len(bad.Events))
	}
}

func seqStr(v uint64) string { return strconv.FormatUint(v, 10) }
