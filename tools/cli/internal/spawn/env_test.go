package spawn

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSourceDotEnv(t *testing.T) {
	dir := t.TempDir()
	content := "" +
		"# a comment\n" +
		"\n" +
		"FOO=bar\n" +
		"export BAZ=qux\n" +
		"QUOTED=\"a=b\"\n" + // quoting is NOT stripped — parity with bash's naive parser
		"1bad=nope\n" + // invalid identifier, silently dropped
		"bad-key=nope\n" + // hyphen invalid in shell identifier, dropped
		"TRAILING#not-a-comment=weird\n" // '#' mid-line is not comment-stripped either

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, count, err := sourceDotEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"FOO=bar", "BAZ=qux", `QUOTED="a=b"`}
	// TRAILING#not-a-comment=weird has key "TRAILING#not-a-comment" which
	// fails the identifier regex (contains '#'), so it's dropped too.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sourceDotEnv() = %#v, want %#v", got, want)
	}
	if count != len(want) {
		t.Errorf("count = %d, want %d", count, len(want))
	}
}

func TestSourceDotEnvMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, count, err := sourceDotEnv(dir)
	if err != nil {
		t.Fatalf("expected no error for missing .env, got %v", err)
	}
	if got != nil || count != 0 {
		t.Errorf("expected empty result for missing .env, got %#v count=%d", got, count)
	}
}

func TestFilterDirenvEnv(t *testing.T) {
	lines := []string{
		"DIRENV_DIFF=abc",
		"HOME=/Users/x",
		"PATH=/usr/bin",
		"PWD=/tmp",
		"SHLVL=2",
		"_=direnv",
		"",
		"PROJECT_VAR=hello",
		"ANOTHER=world",
	}
	got, count := filterDirenvEnv(lines)
	want := []string{"PROJECT_VAR=hello", "ANOTHER=world"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterDirenvEnv() = %#v, want %#v", got, want)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}
