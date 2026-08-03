// Frontmatter parse/write — the identity.md launch-spec format shared by
// identity's --register/--launch/--rename/--reap-ephemeral verbs.
//
// Ported from commands-identity/store.ts's readFrontmatter/writeFrontmatter.
// docs/scope-go-cli.md §4 flags this exact regex-based format as duplicated
// ad hoc in commands-teardown.ts and commands-variant.ts too — this package
// is the single Go home for it; those commands' future ports (B4) should
// import this instead of re-implementing the parse/write.
package identity

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

var (
	frontmatterBlockRe = regexp.MustCompile(`^---\n([\s\S]*?)\n---\n`)
	frontmatterStripRe = regexp.MustCompile(`^---\n[\s\S]*?\n---\n`)
	needsQuoteRe       = regexp.MustCompile(`[:#'"\s]`)
)

// Frontmatter is an insertion-ordered string map: JS objects preserve
// property-insertion order, and writeFrontmatter.ts's field order is
// semantically meaningful (a test asserts "ephemeral" lands after "cwd" in
// the written file) — a plain Go map would randomize that order, so this
// type tracks it explicitly.
type Frontmatter struct {
	keys []string
	vals map[string]string
}

func newFrontmatter() *Frontmatter {
	return &Frontmatter{vals: make(map[string]string)}
}

// Get returns the value for key, or "" if unset.
func (f *Frontmatter) Get(key string) string {
	return f.vals[key]
}

// Has reports whether key is set (present even if its value is "").
func (f *Frontmatter) Has(key string) bool {
	_, ok := f.vals[key]
	return ok
}

// Set assigns key = val. A key that already exists keeps its original
// position; a new key is appended — matching plain JS property assignment.
func (f *Frontmatter) Set(key, val string) {
	if _, ok := f.vals[key]; !ok {
		f.keys = append(f.keys, key)
	}
	f.vals[key] = val
}

// Delete removes key, matching JS's `delete fm.key`.
func (f *Frontmatter) Delete(key string) {
	if _, ok := f.vals[key]; !ok {
		return
	}
	delete(f.vals, key)
	for i, k := range f.keys {
		if k == key {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			break
		}
	}
}

// Keys returns the set keys in insertion order.
func (f *Frontmatter) Keys() []string {
	out := make([]string, len(f.keys))
	copy(out, f.keys)
	return out
}

// unquoteEdge strips at most one leading and one trailing quote character
// (' or "), independently — matching store.ts's
// `.replace(/^["']|["']$/g, "")`, which does not require the two to match.
func unquoteEdge(s string) string {
	if len(s) > 0 && (s[0] == '"' || s[0] == '\'') {
		s = s[1:]
	}
	if len(s) > 0 && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
		s = s[:len(s)-1]
	}
	return s
}

// ReadFrontmatter parses the --- … --- block at the top of file, if any. A
// missing file or missing block yields an empty Frontmatter — never an
// error, matching store.ts's existsSync-guarded read.
func ReadFrontmatter(file string) *Frontmatter {
	fm := newFrontmatter()
	data, err := os.ReadFile(file)
	if err != nil {
		return fm
	}
	m := frontmatterBlockRe.FindStringSubmatch(string(data))
	if m == nil {
		return fm
	}
	for _, line := range strings.Split(m[1], "\n") {
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := unquoteEdge(strings.TrimSpace(line[i+1:]))
		fm.Set(key, val)
	}
	return fm
}

// WriteFrontmatter rewrites file's --- … --- block from fm, preserving any
// body content below it (or seeding a default "# Identity — <id>" body for a
// brand-new file). A value is only emitted when non-empty (JS's `filter(([,
// v]) => v)`), and is JSON-quoted when it contains a character in
// [:#'"\s] — matching writeFrontmatter.ts exactly.
func WriteFrontmatter(file string, fm *Frontmatter) error {
	existing := ""
	if data, err := os.ReadFile(file); err == nil {
		existing = string(data)
	}
	rest := frontmatterStripRe.ReplaceAllString(existing, "")

	lines := make([]string, 0, len(fm.keys))
	for _, k := range fm.keys {
		v := fm.vals[k]
		if v == "" {
			continue
		}
		rendered := v
		if needsQuoteRe.MatchString(v) {
			b, _ := json.Marshal(v)
			rendered = string(b)
		}
		lines = append(lines, k+": "+rendered)
	}

	if rest == "" {
		rest = "# Identity — " + fm.Get("id") + "\n\n"
	}

	out := "---\n" + strings.Join(lines, "\n") + "\n---\n" + rest
	return os.WriteFile(file, []byte(out), 0o644)
}
