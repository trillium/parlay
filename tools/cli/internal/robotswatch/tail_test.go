// Mirrors packages/cli/src/commands-robots-watch/tail.test.ts.
package robotswatch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseCreatedIDExtractsRobotsID(t *testing.T) {
	id, ok := parseCreatedID(`{"id":"robots-7pg","created_at":"2026-07-22T00:00:00Z","source":"robots-create"}`)
	if !ok || id != "robots-7pg" {
		t.Fatalf("got (%q, %v)", id, ok)
	}
}

func TestParseCreatedIDRejectsInvalid(t *testing.T) {
	cases := []string{
		`{"id":"task-abc"}`,
		"not json",
		`{"created_at":"x"}`,
		`{"id":"robots-"}`,
	}
	for _, c := range cases {
		if _, ok := parseCreatedID(c); ok {
			t.Fatalf("expected reject for %q", c)
		}
	}
}

func TestReadNewLinesReadsOnlyBytesPastOffset(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "events.jsonl")
	mustWriteFile(t, f, `{"id":"robots-aaa"}`+"\n")

	first, firstOffset := readNewLines(f, 0)
	if !reflect.DeepEqual(first, []string{`{"id":"robots-aaa"}`}) {
		t.Fatalf("got %v", first)
	}

	// From the advanced offset, nothing new until we append.
	if lines, _ := readNewLines(f, firstOffset); len(lines) != 0 {
		t.Fatalf("expected no new lines, got %v", lines)
	}

	mustAppendFile(t, f, `{"id":"robots-bbb"}`+"\n")
	second, _ := readNewLines(f, firstOffset)
	if !reflect.DeepEqual(second, []string{`{"id":"robots-bbb"}`}) { // only the NEW line, not robots-aaa
		t.Fatalf("got %v", second)
	}
}

func TestReadNewLinesRestartsFromZeroOnTruncation(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "events.jsonl")
	mustWriteFile(t, f, `{"id":"robots-xxx"}`+"\n"+`{"id":"robots-yyy"}`+"\n")

	_, big := readNewLines(f, 0)
	mustWriteFile(t, f, `{"id":"robots-zzz"}`+"\n") // truncate + rewrite (size < big)

	lines, _ := readNewLines(f, big)
	if !reflect.DeepEqual(lines, []string{`{"id":"robots-zzz"}`}) {
		t.Fatalf("got %v", lines)
	}
}

func TestReadNewLinesOnMissingFileIsNoop(t *testing.T) {
	lines, offset := readNewLines(filepath.Join(t.TempDir(), "nope-does-not-exist.jsonl"), 5)
	if len(lines) != 0 || offset != 5 {
		t.Fatalf("got (%v, %d)", lines, offset)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustAppendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}
