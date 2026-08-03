package commands

import (
	"os"
	"testing"
)

func appendRaw(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestUnattendedQueueRoundTrip(t *testing.T) {
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	if got := ReadUnattendedQueue("agent-q"); len(got) != 0 {
		t.Fatalf("ReadUnattendedQueue() on empty queue = %v, want empty", got)
	}

	EnqueueUnattended("agent-q", "done", "task complete")
	EnqueueUnattended("agent-q", "blocked", "")

	got := ReadUnattendedQueue("agent-q")
	if len(got) != 2 {
		t.Fatalf("ReadUnattendedQueue() = %d entries, want 2", len(got))
	}
	if got[0].Verb != "done" || got[0].Detail != "task complete" {
		t.Errorf("entry[0] = %+v, want verb=done detail=\"task complete\"", got[0])
	}
	if got[1].Verb != "blocked" || got[1].Detail != "" {
		t.Errorf("entry[1] = %+v, want verb=blocked detail=\"\"", got[1])
	}
	if got[0].Ts == 0 {
		t.Error("entry[0].Ts is zero, want a real timestamp")
	}

	drained := DrainUnattendedQueue("agent-q")
	if len(drained) != 2 {
		t.Errorf("DrainUnattendedQueue() = %d entries, want 2 (drain must not delete)", len(drained))
	}
	if got2 := ReadUnattendedQueue("agent-q"); len(got2) != 2 {
		t.Errorf("queue after drain = %d entries, want still 2 (drain must not delete)", len(got2))
	}

	ClearUnattendedQueue("agent-q")
	if got3 := ReadUnattendedQueue("agent-q"); len(got3) != 0 {
		t.Errorf("queue after clear = %v, want empty", got3)
	}
}

func TestReadUnattendedQueueSkipsUnparseableLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)

	EnqueueUnattended("agent-r", "done", "ok")
	// Append a garbage line directly, bypassing EnqueueUnattended.
	f := queueFile("agent-r")
	appendRaw(t, f, "not json at all\n")
	EnqueueUnattended("agent-r", "failed", "bad")

	got := ReadUnattendedQueue("agent-r")
	if len(got) != 2 {
		t.Fatalf("ReadUnattendedQueue() = %d entries, want 2 (garbage line skipped)", len(got))
	}
	if got[0].Verb != "done" || got[1].Verb != "failed" {
		t.Errorf("ReadUnattendedQueue() = %+v, want [done, failed]", got)
	}
}

func TestReadUnattendedQueueMissingFileIsEmpty(t *testing.T) {
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	if got := ReadUnattendedQueue("never-enqueued"); len(got) != 0 {
		t.Errorf("ReadUnattendedQueue() on missing file = %v, want empty", got)
	}
}
