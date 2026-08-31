package worktreeliveness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// stubEnumerator replaces the lsof seam for one test. Fixtures are canned
// `lsof -F0pn` output — fields NUL-terminated, records newline-separated —
// exactly what the real enumerator produces, so the parser under test is the
// production parser end to end. No subprocess runs and no timing is asserted
// (docs/agent-notes/a-timing-assertion-loose-enough-not.md).
func stubEnumerator(t *testing.T, out string, err error) {
	t.Helper()
	prev := cwdEnumerator
	cwdEnumerator = func() ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { cwdEnumerator = prev })
}

// rec renders one lsof -F0pn process record.
func rec(pid, cwd string) string {
	return "p" + pid + "\x00fcwd\x00n" + cwd + "\x00\n"
}

func TestCollectParsesRecordsAndLiveAt(t *testing.T) {
	// Paths deliberately do not exist: normalizePath falls back to the
	// cleaned form, keeping the table deterministic on any host.
	stubEnumerator(t, rec("101", "/nonesuch/pool/1/repo")+rec("202", "/nonesuch/elsewhere"), nil)
	s := Collect()
	if !s.Scanned {
		t.Fatal("Scanned = false, want true")
	}
	if s.Source != SourceLsof {
		t.Fatalf("Source = %q, want %q", s.Source, SourceLsof)
	}

	cases := []struct {
		name string
		path string
		live bool
	}{
		{"exact match", "/nonesuch/pool/1/repo", true},
		{"process in a subdirectory still counts", "/nonesuch/pool/1", true},
		{"sibling path", "/nonesuch/pool/2/repo", false},
		{"prefix-sibling is not containment", "/nonesuch/else", false},
		{"unrelated root", "/somewhere", false},
		{"empty path", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live, reason := s.LiveAt(tc.path)
			if live != tc.live {
				t.Fatalf("LiveAt(%q) = %v, want %v", tc.path, live, tc.live)
			}
			if live && reason == "" {
				t.Fatalf("LiveAt(%q) live with empty reason", tc.path)
			}
		})
	}

	if live, reason := s.LiveAt("/nonesuch/pool/1/repo"); !live || !strings.Contains(reason, "101") {
		t.Fatalf("refusal reason must name the pid, got live=%v reason=%q", live, reason)
	}
}

func TestCollectFailClosed(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want bool // Scanned
	}{
		{"empty listing means the scan failed, not an idle host", "", nil, false},
		{"exec error with no output", "", errors.New("exec: lsof: not found"), false},
		{"deadline exceeded discards even a partial listing",
			rec("101", "/nonesuch/a"), fmt.Errorf("lsof: %w", context.DeadlineExceeded), false},
		{"partial listing with an ordinary non-zero exit still counts",
			rec("101", "/nonesuch/a"), errors.New("exit status 1"), true},
		{"error annotations are not records",
			rec("1", "/proc/1/cwd (readlink: Permission denied)") + rec("2", "/x/cwd (stat: No such file or directory)"),
			nil, false},
		{"non-absolute paths are not records", rec("101", "relative/path"), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubEnumerator(t, tc.out, tc.err)
			s := Collect()
			if s.Scanned != tc.want {
				t.Fatalf("Scanned = %v, want %v", s.Scanned, tc.want)
			}
			if !tc.want {
				if s.Source != "" {
					t.Fatalf("unscanned State carries Source %q, want empty", s.Source)
				}
				if live, reason := s.LiveAt("/nonesuch/a"); live || reason != "" {
					t.Fatalf("LiveAt on unscanned State = (%v, %q), want (false, \"\")", live, reason)
				}
			}
		})
	}
}

func TestCollectDedupKeepsFirstPid(t *testing.T) {
	stubEnumerator(t, rec("101", "/nonesuch/shared")+rec("202", "/nonesuch/shared"), nil)
	s := Collect()
	if !s.Scanned {
		t.Fatal("Scanned = false, want true")
	}
	if len(s.records) != 1 {
		t.Fatalf("records = %d, want 1 (deduplicated)", len(s.records))
	}
	if _, reason := s.LiveAt("/nonesuch/shared"); !strings.Contains(reason, "101") {
		t.Fatalf("dedup should keep the first pid, reason = %q", reason)
	}
}

func TestCollectPreservesNewlineInPath(t *testing.T) {
	// The whole point of -F0: a path containing a newline stays one field.
	// Truncating it would leave a cwd that no longer matches the worktree it
	// is inside — under-protection in a path that deletes directories.
	evil := "/nonesuch/evil\ndir"
	stubEnumerator(t, rec("101", evil), nil)
	s := Collect()
	if !s.Scanned {
		t.Fatal("Scanned = false, want true")
	}
	if live, _ := s.LiveAt(evil); !live {
		t.Fatalf("LiveAt(%q) = false, want true: embedded newline was truncated", evil)
	}
	if live, _ := s.LiveAt("/nonesuch/evil"); live {
		t.Fatal("truncated prefix of a newline path must not match")
	}
}

func TestZeroStateIsUnscannedAndInert(t *testing.T) {
	var s State
	if s.Scanned {
		t.Fatal("zero State must be unscanned")
	}
	if live, reason := s.LiveAt("/anything"); live || reason != "" {
		t.Fatalf("zero State LiveAt = (%v, %q), want (false, \"\")", live, reason)
	}
}
