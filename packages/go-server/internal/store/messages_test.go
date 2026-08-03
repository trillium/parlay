package store

import (
	"path/filepath"
	"testing"
)

func TestMessageStoreAppendAssignsIDAndTs(t *testing.T) {
	ms, err := openMessageStore(filepath.Join(t.TempDir(), "messages.jsonl"), 0, 0)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	defer ms.Close()

	got, err := ms.Append(ChatMessage{Role: "user", Text: "hello"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got.ID == "" {
		t.Error("expected a generated id, got empty string")
	}
	if got.Ts == "" {
		t.Error("expected a generated timestamp, got empty string")
	}
}

func TestMessageStoreHistoryOrderAndLimit(t *testing.T) {
	ms, err := openMessageStore(filepath.Join(t.TempDir(), "messages.jsonl"), 0, 0)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	defer ms.Close()

	for i := 0; i < 5; i++ {
		if _, err := ms.Append(ChatMessage{Role: "user", Text: string(rune('a' + i))}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	full := ms.History(0)
	if len(full) != 5 {
		t.Fatalf("History(0) len = %d, want 5", len(full))
	}
	if full[0].Text != "a" || full[4].Text != "e" {
		t.Errorf("History(0) order = %q..%q, want a..e (oldest first)", full[0].Text, full[4].Text)
	}

	last2 := ms.History(2)
	if len(last2) != 2 || last2[0].Text != "d" || last2[1].Text != "e" {
		t.Errorf("History(2) = %+v, want last 2 messages (d, e)", last2)
	}
}

func TestMessageStorePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")

	ms1, err := openMessageStore(path, 0, 0)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := ms1.Append(ChatMessage{Role: "agent", Text: "msg"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := ms1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ms2, err := openMessageStore(path, 0, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ms2.Close()

	if got := ms2.Count(); got != 3 {
		t.Fatalf("after reopen Count() = %d, want 3 (durability across restart)", got)
	}

	// A message appended after reopen must not collide ids with what was
	// replayed from disk — this is what seqFromID/loadFromDisk protects.
	next, err := ms2.Append(ChatMessage{Role: "user", Text: "after restart"})
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	for _, m := range ms2.History(0)[:3] {
		if m.ID == next.ID {
			t.Fatalf("id collision after reopen: new message reused id %q", next.ID)
		}
	}
}

func TestMessageStorePrunesRingBufferToMax(t *testing.T) {
	ms, err := openMessageStore(filepath.Join(t.TempDir(), "messages.jsonl"), 3, 0)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	defer ms.Close()

	for i := 0; i < 10; i++ {
		if _, err := ms.Append(ChatMessage{Role: "user", Text: "x"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if got := ms.Count(); got != 3 {
		t.Fatalf("Count() = %d, want 3 (capped by maxMessages)", got)
	}
}

func TestMessageStoreCompactsFileWhenOverBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	// Tiny byte budget so a handful of appends forces compaction.
	ms, err := openMessageStore(path, 5, 200)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	defer ms.Close()

	for i := 0; i < 20; i++ {
		if _, err := ms.Append(ChatMessage{Role: "user", Text: "some reasonably sized message text here"}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// The ring buffer (in-memory) should still be intact and cap-honoring.
	if got := ms.Count(); got != 5 {
		t.Fatalf("Count() after compaction = %d, want 5", got)
	}

	// Reopening from disk must reflect the compacted file, not the full
	// 20-message history — proves compaction actually rewrote the file
	// rather than just tracking state in memory.
	if err := ms.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ms2, err := openMessageStore(path, 5, 200)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ms2.Close()
	if got := ms2.Count(); got != 5 {
		t.Fatalf("after reopen post-compaction Count() = %d, want 5", got)
	}
}

func TestMessageStoreHistorySince(t *testing.T) {
	ms, err := openMessageStore(filepath.Join(t.TempDir(), "messages.jsonl"), 0, 0)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	defer ms.Close()

	var ids []string
	for i := 0; i < 4; i++ {
		m, err := ms.Append(ChatMessage{Role: "user", Text: "x"})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		ids = append(ids, m.ID)
	}

	since := ms.HistorySince(ids[1])
	if len(since) != 2 {
		t.Fatalf("HistorySince(ids[1]) len = %d, want 2", len(since))
	}
	if since[0].ID != ids[2] || since[1].ID != ids[3] {
		t.Errorf("HistorySince(ids[1]) = %+v, want messages after ids[1]", since)
	}

	full := ms.HistorySince("")
	if len(full) != 4 {
		t.Fatalf("HistorySince(\"\") len = %d, want 4 (full replay)", len(full))
	}

	unknown := ms.HistorySince("does-not-exist")
	if len(unknown) != 4 {
		t.Fatalf("HistorySince(unknown id) len = %d, want 4 (fall back to full replay)", len(unknown))
	}
}
