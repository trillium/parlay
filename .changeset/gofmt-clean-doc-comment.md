---
"@parlay/cli": patch
---

Make the repo gofmt-clean and stop gofmt from corrupting the shell-quoting doc comment (robots-dqle).

`gofmt -l tools/cli/internal/commands/claim.go` reported dirty on an untouched `main`. Since Go 1.19, gofmt reformats **doc comment prose** through `go/doc/comment`, which rewrites a doubled apostrophe into a curly quote — and the comment above `claimShellQuote` spells the POSIX escape `'\''` out in running text. So the one hunk gofmt wanted was to mangle the exact escape sequence the comment exists to explain.

That made `gofmt -l` a false positive for any agent running it as a pre-commit check: it flagged a file the agent never touched, and "fixing" it with `gofmt -w` silently corrupted the documentation.

- The escape now lives in an **indented block** in the doc comment. `go/doc/comment` leaves preformatted blocks verbatim, so the sequence survives byte-for-byte and gofmt is satisfied. A short note in the comment says why it must stay indented.
- Two other pre-existing gofmt violations are cleared so the check is actually trustworthy: a missing blank line before a paragraph in `internal/resolvehandoff` (same doc-comment class), and whitespace-only alignment in `packages/eval-engine`. `gofmt -l .` is now empty repo-wide.
- New `TestGofmtClean` in `internal/commands` fails when anything in the `tools/cli` module needs gofmt, and its failure message tells the reader to check `gofmt -d` before running `gofmt -w` — so the next occurrence of this class is caught rather than blindly formatted away.

No behavior change: `claimShellQuote` itself is untouched.
