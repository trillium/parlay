# Go CLI ticket B7: `doctor`/`health`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`tools/cli/internal/commands/doctor.go` ports both `cmdDoctor` and `cmdHealth`
from `commands-doctor.ts` (they share one TS file, so they share one Go
file). One deliberate quirk carried over: the `identity.md` frontmatter check
inside `Doctor` uses its own ad hoc regex pair (`doctorFrontmatterRe`,
`doctorIDRe`) instead of `internal/identity.ReadFrontmatter` — the TS
original does the same (a local `txt.match(/^---\n([\s\S]*?)\n---/)` plus a
separate `id:` extraction), and its block regex has no required trailing
newline after the closing `---`, unlike `ReadFrontmatter`'s stricter one.
Matching this exactly means `doctor`'s launch-spec-presence check can behave
differently from `identity`'s own frontmatter parsing on a malformed file —
intentional fidelity to the TS source, not an oversight. `commands-doctor.ts`
has no dedicated TS test file to mirror; `doctor_test.go`'s cases were
derived directly from reading the implementation.
